package spell

import (
	"context"
	"errors"
	"fmt"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"

	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/types"
)

// builtinCtxKey marks a resolve running under the embedded built-in loader, so the
// magus.buzz.spell.resolve "builtin" attribute distinguishes a built-in spell's
// resolve from a workspace-local one.
type builtinCtxKey struct{}

// withBuiltinResolve marks ctx as resolving a built-in spell.
func withBuiltinResolve(ctx context.Context) context.Context {
	return context.WithValue(ctx, builtinCtxKey{}, true)
}

// builtinLabel returns "true"/"false" for the resolve "builtin" attribute.
func builtinLabel(ctx context.Context) string {
	if v, _ := ctx.Value(builtinCtxKey{}).(bool); v {
		return "true"
	}
	return "false"
}

// providerFrom returns ctx's telemetry provider only when it is enabled, else nil
// so spell instrumentation stays a true no-op off the daemon path.
func providerFrom(ctx context.Context) observability.Provider {
	p := observability.FromContext(ctx)
	if p == nil || !p.Enabled() {
		return nil
	}
	return p
}

// ErrNotASpell signals that a Buzz module is simply not a spell - it exports no
// mgs_getName - rather than a malformed one. Speculative discovery (a local import
// tried as a spell before falling back to a plain module) treats this as a quiet
// "not a spell, move on"; an explicit spell load still surfaces it as an error.
var ErrNotASpell = errors.New("magus/spell: a spell module must `export fun mgs_getName`")

// Resolve calls a Buzz spell module's exported mgs_ functions once and assembles the
// definition map the shared decoder reads (keyed by the decoder's field names),
// returning the decoded Descriptor. Centralizing it here keeps the mgs_ naming in one
// place and lets the decoder, bind-time handles, and embedded built-ins all read plain
// data uniformly.
//
// It takes an already-executed session so a caller whose spell body imports host
// modules can register them and run its own Exec before resolving; Extract-style
// helpers in the buzz engine wrap it for the bare-session case. Each function-valued
// op in mgs_listTargets is reduced to its declared command (see resolveOps); a spell
// that does in-VM work (a cache backend) exports plain functions and declares no ops.
func Resolve(ctx context.Context, sess *buzz.Session) (Descriptor, error) {
	if p := providerFrom(ctx); p != nil {
		start := time.Now()
		d, err := resolveSpell(ctx, sess)
		p.RecordBuzzSpellResolve(ctx, time.Since(start).Seconds(), d.Name, builtinLabel(ctx))
		return d, err
	}
	return resolveSpell(ctx, sess)
}

func resolveSpell(ctx context.Context, sess *buzz.Session) (Descriptor, error) {
	ex := sess.Exports()

	nameFn, ok := ex["mgs_getName"]
	if !ok {
		return Descriptor{}, ErrNotASpell
	}
	def := vm.NewMap()
	nv, err := sess.CallValue(ctx, nameFn, nil)
	if err != nil {
		return Descriptor{}, fmt.Errorf("magus/spell: mgs_getName: %w", err)
	}
	def.MapSet("name", nv)

	// Optional mgs_ functions to resolved values under the decoder's keys.
	// OptionalContract is the canonical list this loop stays in sync with.
	for _, f := range OptionalContract {
		fn, ok := ex[f.Name]
		if !ok {
			continue
		}
		var args []vm.Value
		if f.TakesDir {
			args = []vm.Value{vm.StrValue("")}
		}
		rv, err := sess.CallValue(ctx, fn, args)
		if err != nil {
			return Descriptor{}, fmt.Errorf("magus/spell: %s: %w", f.Name, err)
		}
		// ops is post-processed here because its handlers can be function-valued
		// (a Buzz-only form) and need resolving to data. See contract.go.
		if f.Field == "ops" {
			rv, err = resolveOps(ctx, sess, rv)
			if err != nil {
				return Descriptor{}, fmt.Errorf("magus/spell: %s: %w", f.Name, err)
			}
		}
		def.MapSet(f.Field, rv)
	}
	return Decode(buzzSpellObj{v: def})
}

// resolveOps reduces a function-valued mgs_listTargets - the op handlers a spell
// returns by value, each `fun(Target) > Run` (or the legacy
// `fun(Target, fun(Run)) void`) - into the {cmd, args, charms} records the shared
// decoder reads. Each handler is called once (recordCommandRun) to capture the
// command it declares, so the built-in command path, BuiltinsHash, and charm
// enumeration all read plain data. Record-shaped entries (a bare {cmd, args} map)
// pass through untouched, so every shape resolves identically.
func resolveOps(ctx context.Context, sess *buzz.Session, ops vm.Value) (vm.Value, error) {
	if !ops.IsMap() {
		return ops, nil
	}
	out := vm.NewMap()
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
		spec, err := recordOp(ctx, sess, v)
		if err != nil {
			return vm.Null, fmt.Errorf("op %q: %w", k, err)
		}
		spec.MapSet("handler", vm.True)
		if doc != "" {
			spec.MapSet("doc", vm.StrValue(doc))
		}
		out.MapSet(k, spec)
	}
	return out, nil
}

// recordOp calls an op handler once with a null Target and returns the Command or
// Service it declares as a field map, for the decoder to read. The op is
// `fun(Target) > Command` or `fun(Target) > Service`: it must be straight-line and
// must not branch on or read the Target (passed as null here, so a value pulled from
// it would be null). A Service op is recognized by its `command` field (the process);
// a Command op by validating directly. Bin/Args, when present, must be strings (an
// empty Command is allowed - a no-op marker op), so a null value read from the Target
// fails at resolution rather than silently caching a wrong command.
func recordOp(ctx context.Context, sess *buzz.Session, fn vm.Value) (vm.Value, error) {
	rv, err := sess.CallValue(ctx, fn, []vm.Value{vm.Null})
	if err != nil {
		return vm.Null, err
	}
	// The handler returns a Command or Service object (magus/target); MapView yields
	// its field map. MapView also accepts a plain map, so a spell or test that returns
	// a bare record still resolves identically.
	mv, ok := rv.MapView()
	if !ok {
		return vm.Null, fmt.Errorf("op handler must return `Command{...}` or `Service{...}`")
	}
	if _, ok := mv.MapGet("command"); ok {
		// A service op: validate its `command` plus the optional `readiness`/`stop`
		// commands, each a Command (an empty Command validates as a no-op).
		for _, field := range []string{"command", "readiness", "stop"} {
			sub, ok := mv.MapGet(field)
			if !ok {
				continue
			}
			sc, ok := sub.MapView()
			if !ok {
				return vm.Null, fmt.Errorf("service op's Service `%s` must be a `Command{...}`", field)
			}
			if err := validateCmdFields(sc); err != nil {
				return vm.Null, fmt.Errorf("service op %s: %w", field, err)
			}
		}
		return mv, nil
	}
	if err := validateCmdFields(mv); err != nil {
		return vm.Null, err
	}
	return mv, nil
}

// validateCmdFields checks a Command field map: bin is a string and args are all
// strings, when present.
func validateCmdFields(m vm.Value) error {
	if bin, ok := m.MapGet("bin"); ok && !bin.IsStr() {
		return fmt.Errorf("command bin must be a string")
	}
	if args, ok := m.MapGet("args"); ok && args.IsList() {
		for _, a := range args.ListItems() {
			if !a.IsStr() {
				return fmt.Errorf("command args must all be strings")
			}
		}
	}
	return nil
}

// DecodeHandle decodes a bind-time spell handle - a map of resolved native data
// built by a workspace-local spell import - into a Descriptor, so a workspace-local
// Buzz spell can be registered by value at bind time.
func DecodeHandle(v vm.Value) (Descriptor, error) {
	return Decode(buzzSpellObj{v: v})
}

// DecodeCommandValue decodes a single Buzz Command value (bin + args + the charm
// JSON-Patch table) into a types.Command, reusing the same reader the engine uses
// for a spell op. It is the by-value entrypoint for a caller holding a raw Command
// map - the playground's dry run - so the sandbox and the engine agree on a
// command's shape without a second decoder. v must be a map or object instance
// (MapView'd form); an invalid charm patch is an error, as it is for the engine.
func DecodeCommandValue(v vm.Value) (types.Command, error) {
	return decodeCommand("", "", buzzSpellObj{v: v})
}

// buzzSpellObj adapts a Buzz data map (a resolved definition or a bound handle)
// to Obj. All fields are plain data - needs/provides/ops were already resolved by
// Resolve or marshalled into the handle - so there is no function-calling here.
type buzzSpellObj struct {
	v vm.Value
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
	if !ok {
		return nil, false
	}
	// MapView accepts both a map and an object instance (a Run/Charm/PatchOp literal
	// a command handler built), yielding the field map either way, so the decoder reads
	// the typed-object and bare-record forms identically.
	mv, ok := x.MapView()
	if !ok {
		return nil, false
	}
	return buzzSpellObj{v: mv}, true
}

func (o buzzSpellObj) Objs(key string) []Obj {
	x, ok := o.v.MapGet(key)
	if !ok || !x.IsList() {
		return nil
	}
	var out []Obj
	for _, it := range x.ListItems() {
		if mv, ok := it.MapView(); ok {
			out = append(out, buzzSpellObj{v: mv})
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
func mapStrSlice(m vm.Value, key string) []string {
	v, ok := m.MapGet(key)
	if !ok {
		return nil
	}
	return valStrSlice(v)
}

func valStrSlice(v vm.Value) []string {
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
