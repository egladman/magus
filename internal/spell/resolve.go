package spell

import (
	"context"
	"fmt"

	"github.com/egladman/gopherbuzz"
)

// ResolveMode tells Resolve how to reduce a function-valued
// mgs_listTargets (see resolveOps).
type ResolveMode int

const (
	// FunctionOps keeps each op handler for invoke-time dispatch — the form a
	// spell that imports host modules (e.g. github) needs, since its handlers do
	// host work rather than declare a command.
	FunctionOps ResolveMode = iota
	// ForkExtract calls each op handler once to record the command it declares,
	// so a self-contained fork spell decodes as ordinary fork targets.
	ForkExtract
)

// ForkOrFunctionOps returns the ResolveMode for a spell source: ForkExtract for
// a self-contained fork spell (imports only magus/target, or nothing),
// FunctionOps otherwise.
func ForkOrFunctionOps(src string) ResolveMode {
	if IsSelfContained(src) {
		return ForkExtract
	}
	return FunctionOps
}

// Resolve calls a Buzz spell module's exported mgs_ functions
// once and assembles the definition map the shared decoder reads (keyed by the
// decoder's field names), returning the decoded Spec. Centralizing it here
// keeps the mgs_ naming in one place and lets the decoder, bind-time handles, and
// the embedded built-ins all read plain data uniformly.
//
// It takes an already-executed session so a caller whose spell body imports host
// modules (e.g. a function-op spell using magus/std/http) can register those
// modules and run its own Exec before resolving; Extract-style helpers in the
// buzz engine wrap it for the bare-session case.
//
// mode selects how a function-valued mgs_listTargets is reduced (see resolveOps).
func Resolve(ctx context.Context, sess *buzz.Session, mode ResolveMode) (Spec, error) {
	ex := sess.Exports()

	nameFn, ok := ex["mgs_getName"]
	if !ok {
		return Spec{}, fmt.Errorf("magus/spell: a spell module must `export fun mgs_getName`")
	}
	def := buzz.NewMap()
	nv, err := sess.CallValue(ctx, nameFn, nil)
	if err != nil {
		return Spec{}, fmt.Errorf("magus/spell: mgs_getName: %w", err)
	}
	def.MapSet("name", nv)

	// Optional mgs_ functions → resolved values under the decoder's keys.
	// OptionalContract is the canonical list this loop stays in sync with.
	for _, f := range OptionalContract {
		fn, ok := ex[f.Name]
		if !ok {
			continue
		}
		var args []buzz.Value
		if f.TakesDir {
			args = []buzz.Value{buzz.StrValue("")}
		}
		rv, err := sess.CallValue(ctx, fn, args)
		if err != nil {
			return Spec{}, fmt.Errorf("magus/spell: %s: %w", f.Name, err)
		}
		// ops is post-processed here because its handlers can be function-valued
		// (a Buzz-only form) and need resolving to data. See contract.go.
		if f.Field == "ops" {
			rv, err = resolveOps(ctx, sess, rv, mode)
			if err != nil {
				return Spec{}, fmt.Errorf("magus/spell: %s: %w", f.Name, err)
			}
		}
		def.MapSet(f.Field, rv)
	}
	return Decode(buzzSpellObj{v: def})
}

// resolveOps reduces a function-valued mgs_listTargets — the strictly-typed
// {str: fun(Target, fun(any)) bool} form a spell returns, referencing its op
// handlers directly by value — into the records the shared decoder reads. A function
// value becomes either:
//
//   - ForkExtract: the {cmd, args, charms} spec the handler passes to its injected
//     cb-callback, captured by calling the handler once (recordForkSpec). The
//     decoder then reads it as an ordinary fork target — so the built-in fork path,
//     BuiltinsHash, and charm enumeration are all unchanged.
//   - FunctionOps: a {"fn": <handler>} record naming the handler for invoke-time
//     dispatch (function-op spells like github, whose handlers do host work, not
//     run a command).
//
// Record-shaped entries (a legacy {cmd, args} map or explicit {"fn": "..."}) pass
// through untouched, so every shape resolves identically.
func resolveOps(ctx context.Context, sess *buzz.Session, ops buzz.Value, mode ResolveMode) (buzz.Value, error) {
	if !ops.IsMap() {
		return ops, nil
	}
	ex := sess.Exports()
	out := buzz.NewMap()
	for _, k := range ops.MapKeys() {
		v, _ := ops.MapGet(k)
		if !v.IsFun() {
			out.MapSet(k, v)
			continue
		}
		// The handler's doc comment is its declaration's comment block, recovered
		// from the function value; "" when undocumented or when the spell was
		// loaded from bytecode (built-ins, whose Doc is not serialized). The
		// "handler" marker distinguishes a function-authored target (which the
		// doctor doc-comment check applies to) from a plain {cmd,args} record op.
		doc := v.FunDoc()
		if mode == ForkExtract {
			spec, err := recordForkSpec(ctx, sess, v)
			if err != nil {
				return buzz.Null, fmt.Errorf("op %q: %w", k, err)
			}
			spec.MapSet("handler", buzz.True)
			if doc != "" {
				spec.MapSet("doc", buzz.StrValue(doc))
			}
			out.MapSet(k, spec)
			continue
		}
		// Dispatch is by the handler's own exported name — recovered from the
		// function value, not the op key — so `"foo": bar` invokes bar, and a
		// handler that isn't an exported function fails here, not at invoke time
		// (callBuzzSpellFunc looks it up in Exports by this name).
		name := v.FunName()
		if name == "" {
			return buzz.Null, fmt.Errorf("op %q: handler must be a named function", k)
		}
		if _, ok := ex[name]; !ok {
			return buzz.Null, fmt.Errorf("op %q: handler %q is not exported", k, name)
		}
		rec := buzz.NewMap()
		rec.MapSet("fn", buzz.StrValue(name))
		rec.MapSet("handler", buzz.True)
		if doc != "" {
			rec.MapSet("doc", buzz.StrValue(doc))
		}
		out.MapSet(k, rec)
	}
	return out, nil
}

// recordForkSpec calls a fork op handler once with a recording cb-callback and
// returns the single {cmd, args, charms} spec the handler hands to cb(...). A
// fork handler must be straight-line: `cb({...}); return true;`, calling cb exactly
// once with a constant command — it must not branch on or read the Target (passed
// as null here, so a value pulled from it would be null). The recorded cmd/args,
// when present, must be strings (an empty record is allowed — a no-op marker op),
// so a second cb(...) call or a null value read from the Target fails at
// resolution rather than silently caching a wrong spec.
func recordForkSpec(ctx context.Context, sess *buzz.Session, fn buzz.Value) (buzz.Value, error) {
	captured := buzz.Null
	calls := 0
	cb := buzz.DirectValue("magus.cb", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		calls++
		if calls > 1 {
			return buzz.Null, fmt.Errorf("fork op handler must call cb(...) exactly once")
		}
		if len(args) > 0 {
			captured = args[0]
		}
		return buzz.Null, nil
	})
	if _, err := sess.CallValue(ctx, fn, []buzz.Value{buzz.Null, cb}); err != nil {
		return buzz.Null, err
	}
	if !captured.IsMap() {
		return buzz.Null, fmt.Errorf("fork op handler must call `cb({...})` with a command record")
	}
	if cmd, ok := captured.MapGet("cmd"); ok && !cmd.IsStr() {
		return buzz.Null, fmt.Errorf("fork op handler's cb({...}) cmd must be a string")
	}
	if args, ok := captured.MapGet("args"); ok && args.IsList() {
		for _, a := range args.ListItems() {
			if !a.IsStr() {
				return buzz.Null, fmt.Errorf("fork op handler's cb({...}) args must all be strings")
			}
		}
	}
	return captured, nil
}

// DecodeHandle decodes a bind-time spell handle — a map of resolved native data
// built by a workspace-local spell import — into a Spec, so a workspace-local
// Buzz spell can be registered by value at bind time.
func DecodeHandle(v buzz.Value) (Spec, error) {
	return Decode(buzzSpellObj{v: v})
}

// buzzSpellObj adapts a Buzz data map (a resolved definition or a bound handle)
// to Obj. All fields are plain data — needs/provides/ops were already resolved
// by Resolve or marshalled into the handle — so there is no
// function-calling here.
type buzzSpellObj struct {
	v buzz.Value
}

func (o buzzSpellObj) Str(key string) (string, bool) {
	x, ok := o.v.MapGet(key)
	if !ok || !x.IsStr() {
		return "", false
	}
	return x.AsString(), true
}

func (o buzzSpellObj) Bool(key string) bool {
	x, ok := o.v.MapGet(key)
	return ok && x.Bool()
}

func (o buzzSpellObj) Strs(key string) []string { return mapStrSlice(o.v, key) }

func (o buzzSpellObj) Obj(key string) (Obj, bool) {
	x, ok := o.v.MapGet(key)
	if !ok || !x.IsMap() {
		return nil, false
	}
	return buzzSpellObj{v: x}, true
}

func (o buzzSpellObj) Objs(key string) []Obj {
	x, ok := o.v.MapGet(key)
	if !ok || !x.IsList() {
		return nil
	}
	var out []Obj
	for _, it := range x.ListItems() {
		if it.IsMap() {
			out = append(out, buzzSpellObj{v: it})
		}
	}
	return out
}

func (o buzzSpellObj) Keys() []string { return o.v.MapKeys() }

func (o buzzSpellObj) CallStrs(key string, _ ...string) ([]string, error) {
	v, ok := o.v.MapGet(key)
	if !ok {
		return nil, nil
	}
	return valStrSlice(v), nil
}

// mapStrSlice reads key from a map value as a string slice, or nil when absent.
func mapStrSlice(m buzz.Value, key string) []string {
	v, ok := m.MapGet(key)
	if !ok {
		return nil
	}
	return valStrSlice(v)
}

func valStrSlice(v buzz.Value) []string {
	if !v.IsList() {
		return nil
	}
	items := v.ListItems()
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, it := range items {
		if it.IsStr() {
			out = append(out, it.AsString())
		}
	}
	return out
}
