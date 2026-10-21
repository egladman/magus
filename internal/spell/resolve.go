package spell

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	"github.com/egladman/magus/internal/observability"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
)

// builtinCtxKey marks a resolve running under the embedded built-in loader, so the
// magus.buzz.spell.resolve "builtin" attribute distinguishes a built-in spell's
// resolve from a workspace-local one.
type builtinCtxKey struct{}

// withBuiltinResolve marks ctx as resolving a built-in spell.
func withBuiltinResolve(ctx context.Context) context.Context {
	return context.WithValue(ctx, builtinCtxKey{}, true)
}

// isBuiltinResolve reports whether ctx was marked by withBuiltinResolve.
func isBuiltinResolve(ctx context.Context) bool {
	v, _ := ctx.Value(builtinCtxKey{}).(bool)
	return v
}

// providerFrom returns ctx's telemetry provider only when it is enabled, else nil
// so spell instrumentation stays a true no-op off the server path.
func providerFrom(ctx context.Context) observability.Provider {
	p := observability.FromContext(ctx)
	if p == nil || !p.Enabled() {
		return nil
	}
	return p
}

// ErrNotASpell signals that a Buzz module is not a spell (it exports no
// mgs_getName) rather than a malformed one. Speculative discovery (a local import
// tried as a spell before falling back to a plain module) treats this as a quiet
// "not a spell, move on"; an explicit spell load still surfaces it as an error.
var ErrNotASpell = errors.New("magus/spell: a spell module must `export fun " + NameFunc + "`")

// NameFunc is the export that makes a Buzz module a spell.
const NameFunc = "mgs_getName"

// Resolved is what a spell's mgs_ functions answered, decoded strictly but not yet
// checked for meaning. Descriptor does the semantic checks.
type Resolved struct {
	Contract Contract
	// TargetDocs maps an op name, as authored, to the doc comment of its handler.
	// Empty for a spell loaded from bytecode, which does not carry doc comments.
	TargetDocs map[string]string
}

// Resolve calls each mgs_ function src's module exports once and decodes the answer
// through decode.ContractFuncs, so a spell whose exports differ from the contract in
// signature or in shape fails here with MGS1051 or a field path.
//
// It takes an already-executed session so a caller whose spell body imports host
// modules can register them and run its own Exec first. src is that session's own
// source, which the contract check reads for the declared signatures.
func Resolve(ctx context.Context, sess *buzz.Session, src string) (Resolved, error) {
	p := providerFrom(ctx)
	if p == nil {
		return resolveSpell(ctx, sess, src)
	}
	start := time.Now()
	r, err := resolveSpell(ctx, sess, src)
	p.RecordBuzzSpellResolve(ctx, observability.BuzzSpellResolve{
		Spell:    r.Contract.Name,
		Builtin:  isBuiltinResolve(ctx),
		Duration: time.Since(start).Seconds(),
	})
	return r, err
}

func resolveSpell(ctx context.Context, sess *buzz.Session, src string) (Resolved, error) {
	exports := sess.Exports()
	if _, ok := exports[NameFunc]; !ok {
		return Resolved{}, ErrNotASpell
	}
	if err := checkContract(src); err != nil {
		return Resolved{}, err
	}
	r := Resolved{TargetDocs: map[string]string{}}
	for _, fn := range decode.ContractFuncs {
		export, ok := exports[fn.Name]
		if !ok {
			continue
		}
		answer, err := sess.CallValue(ctx, export, nil)
		if err != nil {
			return Resolved{}, fmt.Errorf("magus/spell: %s: %w", fn.Name, err)
		}
		if fn.Handler {
			answer, err = r.callHandlers(ctx, sess, answer)
			if err != nil {
				return Resolved{}, fmt.Errorf("magus/spell: %s: %w", fn.Name, err)
			}
		}
		decoded, err := fn.Decode(answer)
		if err != nil {
			return Resolved{}, fmt.Errorf("magus/spell: %w", err)
		}
		reflect.ValueOf(&r.Contract).Elem().FieldByName(fn.Field).Set(reflect.ValueOf(decoded))
	}
	return r, nil
}

// callHandlers calls each op handler of a {str: fun(Target) T} answer once with a null
// Target and returns the map of what they returned, recording each handler's doc
// comment. A handler must be straight-line and must not read its Target: a value
// pulled from the null one would silently cache a wrong command.
func (r Resolved) callHandlers(ctx context.Context, sess *buzz.Session, handlers vm.Value) (vm.Value, error) {
	if !handlers.IsMap() {
		return handlers, nil
	}
	out := vm.NewMap()
	for _, op := range handlers.MapKeys() {
		handler, _ := handlers.MapGet(op)
		if !handler.IsFun() {
			return vm.Null, fmt.Errorf("op %q is a %s, want a handler `fun(Target)`", op, handler.Kind())
		}
		declared, err := sess.CallValue(ctx, handler, []vm.Value{vm.Null})
		if err != nil {
			return vm.Null, fmt.Errorf("op %q: %w", op, err)
		}
		if doc := handler.FunDoc(); doc != "" {
			r.TargetDocs[op] = doc
		}
		out.MapSet(op, declared)
	}
	return out, nil
}

// CheckCommand decodes a Buzz Command value held by a caller that has no session to
// resolve (the playground's dry run) with the strictness and the checks a spell op
// gets, so the sandbox and the engine agree on what a command is.
func CheckCommand(v vm.Value) (spells.Command, error) {
	c, err := decode.DecodeCommand(v)
	if err != nil {
		return spells.Command{}, err
	}
	return c, validateCommand("", "", c)
}
