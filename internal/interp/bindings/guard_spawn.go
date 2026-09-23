package bindings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/interp"
	bindinggen "github.com/egladman/magus/internal/interp/bindings/gen"
	"github.com/egladman/magus/internal/workspace"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/types"
)

// spawnFactsKey carries the calling session's marker gate into a rule call, which is
// the only place once and count have a session to count in.
type spawnFactsKey struct{}

// registerSpawnRule binds magus\guard.spawn and the members a rule builds its answer
// with onto guardMap:
//
//	magus\guard.spawn(fun(req: SpawnRequest) > SpawnVerdict {
//	    if (req.model == "") { return magus\guard.deny("name a model"); }
//	    return magus\guard.allow();
//	});
//
// The function is kept, not called: the agent guard calls it on each spawn and
// continuation it sees, long after this load. Refusals are MGS1045 at load, because a
// rule that silently failed to register leaves a workspace that reads as guarded.
func registerSpawnRule(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, guardMap vm.Value) {
	// Per session, not per registry: a worker session re-running the same root
	// magusfile registers again legitimately, while two calls in one load do not.
	registered := false
	guardMap.MapSet("spawn", directVal(obs, "magus.guard.spawn", func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) != 1 || !args[0].IsFun() {
			return vm.Null, types.DiagnosticErrorf(types.GuardSpawnMisdeclared,
				`magus\guard.spawn: expected one function, fun(req: SpawnRequest) > SpawnVerdict`)
		}
		if path, ok := interp.ProjectPathFromContext(ctx); ok && path != "" && path != "." {
			return vm.Null, types.DiagnosticErrorf(types.GuardSpawnMisdeclared,
				`magus\guard.spawn: called from the magusfile of %s; the spawn rule answers for every agent in the workspace, so register it in the root magusfile`, path)
		}
		if registered {
			return vm.Null, types.DiagnosticErrorf(types.GuardSpawnMisdeclared,
				`magus\guard.spawn: already registered in this magusfile; a workspace has one spawn rule, so fold the second one into the first`)
		}
		registered = true
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil && sess != nil {
			reg.SetSpawnRule(buzzSpawnRule(sess, args[0]))
		}
		return vm.Null, nil
	}))
	guardMap.MapSet("allow", directVal(obs, "magus.guard.allow", func(context.Context, []vm.Value) (vm.Value, error) {
		return bindinggen.ObjectSpawnVerdict(types.SpawnVerdict{Decision: types.SpawnAllow}), nil
	}))
	for _, decision := range []types.SpawnDecision{types.SpawnAdvise, types.SpawnDeny} {
		member := string(decision)
		guardMap.MapSet(member, directVal(obs, "magus.guard."+member, func(_ context.Context, args []vm.Value) (vm.Value, error) {
			if len(args) != 1 || !args[0].IsStr() || strings.TrimSpace(args[0].AsString()) == "" {
				return vm.Null, fmt.Errorf(`magus\guard.%s: expected the text the agent is shown, a non-empty string`, member)
			}
			return bindinggen.ObjectSpawnVerdict(types.SpawnVerdict{Decision: decision, Reason: args[0].AsString()}), nil
		}))
	}
	guardMap.MapSet("once", directVal(obs, "magus.guard.once", func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		facts, kind, err := spawnFactsArg(callCtx, "once", args)
		if err != nil {
			return vm.Null, err
		}
		return vm.BoolValue(!facts.MarkFired(kind)), nil
	}))
	guardMap.MapSet("count", directVal(obs, "magus.guard.count", func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		facts, kind, err := spawnFactsArg(callCtx, "count", args)
		if err != nil {
			return vm.Null, err
		}
		return vm.IntValue(int64(facts.Count(kind))), nil
	}))
}

// spawnFactsArg resolves once/count's session and turns the author's key into a marker
// kind. The key is hashed because a marker kind is a filename component, and prefixed so
// no workspace key can collide with a compiled advisory's marker.
func spawnFactsArg(ctx context.Context, member string, args []vm.Value) (hint.Gate, hint.MarkerKind, error) {
	facts, ok := ctx.Value(spawnFactsKey{}).(hint.Gate)
	if !ok {
		return hint.Gate{}, "", fmt.Errorf(`magus\guard.%s: only callable inside a magus\guard.spawn rule while the guard runs it, since it counts per agent session`, member)
	}
	if len(args) != 1 || !args[0].IsStr() || args[0].AsString() == "" {
		return hint.Gate{}, "", fmt.Errorf(`magus\guard.%s: expected one non-empty key string`, member)
	}
	sum := sha256.Sum256([]byte(args[0].AsString()))
	return facts, hint.MarkerKind("spawn-" + member + "-" + hex.EncodeToString(sum[:8])), nil
}

// buzzSpawnRule adapts the registered Buzz function to the guard's rule type.
//
// It runs after the load that registered it has finished and closed its session. That
// holds because Session.Close only cancels the session's own context, which CallValue
// does not use: each call runs on a fresh VM under the caller's context, over the
// globals the load left behind.
func buzzSpawnRule(sess *buzz.Session, rule vm.Value) workspace.SpawnRule {
	return func(ctx context.Context, req types.SpawnRequest, facts hint.Gate) (verdict types.SpawnVerdict, err error) {
		// Workspace code runs on every spawn; an interpreter fault inside it must fail
		// open like any other broken rule rather than take the hook process down.
		defer func() {
			if r := recover(); r != nil {
				verdict, err = types.SpawnVerdict{}, fmt.Errorf(`magus\guard.spawn: the rule faulted the interpreter: %v`, r)
			}
		}()
		ctx = context.WithValue(ctx, spawnFactsKey{}, facts)
		rv, err := sess.CallValue(ctx, rule, []vm.Value{bindinggen.ObjectSpawnRequest(req)})
		if err != nil {
			return types.SpawnVerdict{}, fmt.Errorf(`magus\guard.spawn: the rule raised: %w`, err)
		}
		return decodeSpawnVerdict(rv)
	}
}

// decodeSpawnVerdict reads a rule's answer. MapView rather than IsMap, because a
// SpawnVerdict built as an object literal is an instance, not a map.
func decodeSpawnVerdict(v vm.Value) (types.SpawnVerdict, error) {
	fields, ok := v.MapView()
	if !ok {
		return types.SpawnVerdict{}, fmt.Errorf(`magus\guard.spawn: the rule returned %s, not a SpawnVerdict; return magus\guard.allow(), magus\guard.advise(text) or magus\guard.deny(text)`, v.Kind())
	}
	var out types.SpawnVerdict
	if d, ok := fields.MapGet("decision"); ok && !d.IsNull() {
		if !d.IsStr() {
			return types.SpawnVerdict{}, fmt.Errorf(`magus\guard.spawn: the verdict's decision is %s, not a str`, d.Kind())
		}
		if err := out.Decision.UnmarshalText([]byte(d.AsString())); err != nil {
			return types.SpawnVerdict{}, fmt.Errorf(`magus\guard.spawn: %w`, err)
		}
	}
	if r, ok := fields.MapGet("reason"); ok && !r.IsNull() {
		if !r.IsStr() {
			return types.SpawnVerdict{}, fmt.Errorf(`magus\guard.spawn: the verdict's reason is %s, not a str`, r.Kind())
		}
		out.Reason = strings.TrimSpace(r.AsString())
	}
	if (out.Decision == types.SpawnDeny || out.Decision == types.SpawnAdvise) && out.Reason == "" {
		return types.SpawnVerdict{}, fmt.Errorf(`magus\guard.spawn: the rule returned %s with no reason, which tells the agent nothing`, out.Decision)
	}
	return out, nil
}
