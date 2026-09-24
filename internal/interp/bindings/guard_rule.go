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

// ruleFactsKey carries the calling session's marker gate into a rule call, which is the
// only place once and count have a session to count in.
type ruleFactsKey struct{}

// functionRule is one function-valued guard seam: magus\guard.spawn or
// magus\guard.command. Both register the same way and answer with the same verdict; they
// differ only in the request they are handed.
type functionRule struct {
	// member is the magus\guard member that registers the rule.
	member string
	// signature is the function shape the member expects, for its refusals.
	signature string
	// judges names what the rule answers for, for the refusal of a project's call.
	judges string
	// set records the registered function on the load's registry.
	set func(reg *workspace.WorkspaceRegistry, sess *buzz.Session, fn vm.Value)
}

// registerFunctionRule binds one function-valued seam onto guardMap. The function is kept,
// not called: the agent guard calls it on each event it sees, long after this load.
// Refusals are MGS1045 at load, because a rule that silently failed to register leaves a
// workspace that reads as guarded.
func registerFunctionRule(ctx context.Context, sess *buzz.Session, obs buzz.DirectObserver, guardMap vm.Value, r functionRule) {
	// Per session, not per registry: a worker session re-running the same root magusfile
	// registers again legitimately, while two calls in one load do not.
	registered := false
	name := `magus\guard.` + r.member
	guardMap.MapSet(r.member, directVal(obs, "magus.guard."+r.member, func(_ context.Context, args []vm.Value) (vm.Value, error) {
		if len(args) != 1 || !args[0].IsFun() {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`%s: expected one function, %s`, name, r.signature)
		}
		if path, ok := interp.ProjectPathFromContext(ctx); ok && path != "" && path != "." {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`%s: called from the magusfile of %s; the rule answers for %s in the workspace, so register it in the root magusfile`, name, path, r.judges)
		}
		if registered {
			return vm.Null, types.DiagnosticErrorf(types.GuardRuleMisdeclared,
				`%s: already registered in this magusfile; a workspace has one such rule, so fold the second one into the first`, name)
		}
		registered = true
		if reg := workspace.WorkspaceRegistryFromContext(ctx); reg != nil && sess != nil {
			r.set(reg, sess, args[0])
		}
		return vm.Null, nil
	}))
}

// registerVerdictMembers binds the members every function rule builds its answer with:
// allow/advise/deny, and once/count for state that lasts the calling session.
func registerVerdictMembers(obs buzz.DirectObserver, guardMap vm.Value) {
	guardMap.MapSet("allow", directVal(obs, "magus.guard.allow", func(context.Context, []vm.Value) (vm.Value, error) {
		return bindinggen.ObjectGuardVerdict(types.GuardVerdict{Decision: types.GuardAllow}), nil
	}))
	for _, decision := range []types.GuardDecision{types.GuardAdvise, types.GuardDeny} {
		member := string(decision)
		guardMap.MapSet(member, directVal(obs, "magus.guard."+member, func(_ context.Context, args []vm.Value) (vm.Value, error) {
			if len(args) != 1 || !args[0].IsStr() || strings.TrimSpace(args[0].AsString()) == "" {
				return vm.Null, fmt.Errorf(`magus\guard.%s: expected the text the agent is shown, a non-empty string`, member)
			}
			return bindinggen.ObjectGuardVerdict(types.GuardVerdict{Decision: decision, Reason: args[0].AsString()}), nil
		}))
	}
	guardMap.MapSet("once", directVal(obs, "magus.guard.once", func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		facts, kind, err := ruleFactsArg(callCtx, "once", args)
		if err != nil {
			return vm.Null, err
		}
		return vm.BoolValue(!facts.MarkFired(kind)), nil
	}))
	guardMap.MapSet("count", directVal(obs, "magus.guard.count", func(callCtx context.Context, args []vm.Value) (vm.Value, error) {
		facts, kind, err := ruleFactsArg(callCtx, "count", args)
		if err != nil {
			return vm.Null, err
		}
		return vm.IntValue(int64(facts.Count(kind))), nil
	}))
}

// ruleFactsArg resolves once/count's session and turns the author's key into a marker
// kind. The key is hashed because a marker kind is a filename component, and prefixed so
// no workspace key can collide with a compiled advisory's marker. The prefix is shared by
// both rules, so a key means one thing to the whole policy.
func ruleFactsArg(ctx context.Context, member string, args []vm.Value) (hint.Gate, hint.MarkerKind, error) {
	facts, ok := ctx.Value(ruleFactsKey{}).(hint.Gate)
	if !ok {
		return hint.Gate{}, "", fmt.Errorf(`magus\guard.%s: only callable inside a magus\guard.spawn or magus\guard.command rule while the guard runs it, since it counts per agent session`, member)
	}
	if len(args) != 1 || !args[0].IsStr() || args[0].AsString() == "" {
		return hint.Gate{}, "", fmt.Errorf(`magus\guard.%s: expected one non-empty key string`, member)
	}
	sum := sha256.Sum256([]byte(args[0].AsString()))
	return facts, hint.MarkerKind("rule-" + member + "-" + hex.EncodeToString(sum[:8])), nil
}

// callFunctionRule runs a registered rule on req and reads its answer.
//
// It runs after the load that registered it has finished and closed its session. That
// holds because Session.Close only cancels the session's own context, which CallValue
// does not use: each call runs on a fresh VM under the caller's context, over the
// globals the load left behind.
func callFunctionRule(ctx context.Context, sess *buzz.Session, rule vm.Value, member string, req vm.Value, facts hint.Gate) (verdict types.GuardVerdict, err error) {
	name := `magus\guard.` + member
	// Workspace code runs on every call the guard judges; an interpreter fault inside it
	// must fail open like any other broken rule rather than take the hook process down.
	defer func() {
		if r := recover(); r != nil {
			verdict, err = types.GuardVerdict{}, fmt.Errorf(`%s: the rule faulted the interpreter: %v`, name, r)
		}
	}()
	ctx = context.WithValue(ctx, ruleFactsKey{}, facts)
	rv, err := sess.CallValue(ctx, rule, []vm.Value{req})
	if err != nil {
		return types.GuardVerdict{}, fmt.Errorf(`%s: the rule raised: %w`, name, err)
	}
	return decodeGuardVerdict(name, rv)
}

// decodeGuardVerdict reads a rule's answer. MapView rather than IsMap, because a
// GuardVerdict built as an object literal is an instance, not a map.
func decodeGuardVerdict(name string, v vm.Value) (types.GuardVerdict, error) {
	fields, ok := v.MapView()
	if !ok {
		return types.GuardVerdict{}, fmt.Errorf(`%s: the rule returned %s, not a GuardVerdict; return magus\guard.allow(), magus\guard.advise(text) or magus\guard.deny(text)`, name, v.Kind())
	}
	var out types.GuardVerdict
	if d, ok := fields.MapGet("decision"); ok && !d.IsNull() {
		if !d.IsStr() {
			return types.GuardVerdict{}, fmt.Errorf(`%s: the verdict's decision is %s, not a str`, name, d.Kind())
		}
		if err := out.Decision.UnmarshalText([]byte(d.AsString())); err != nil {
			return types.GuardVerdict{}, fmt.Errorf(`%s: %w`, name, err)
		}
	}
	if r, ok := fields.MapGet("reason"); ok && !r.IsNull() {
		if !r.IsStr() {
			return types.GuardVerdict{}, fmt.Errorf(`%s: the verdict's reason is %s, not a str`, name, r.Kind())
		}
		out.Reason = strings.TrimSpace(r.AsString())
	}
	if (out.Decision == types.GuardDeny || out.Decision == types.GuardAdvise) && out.Reason == "" {
		return types.GuardVerdict{}, fmt.Errorf(`%s: the rule returned %s with no reason, which tells the agent nothing`, name, out.Decision)
	}
	return out, nil
}
