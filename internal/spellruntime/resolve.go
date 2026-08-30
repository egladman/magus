package spellruntime

import (
	"context"
	"errors"
	"fmt"
	"time"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"

	"github.com/egladman/magus/internal/observability"
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
// returning the decoded spells.Descriptor. Centralizing it here keeps the mgs_ naming in one
// place and lets the decoder, bind-time handles, and embedded built-ins all read plain
// data uniformly.
//
// It takes an already-executed session so a caller whose spell body imports host
// modules can register them and run its own Exec before resolving; Extract-style
// helpers in the buzz engine wrap it for the bare-session case. Each function-valued
// op in mgs_listTargets is reduced to its declared command (see resolveOps); a spell
// that does in-VM work (a cache backend) exports plain functions and declares no ops.
func Resolve(ctx context.Context, sess *buzz.Session) (spells.Descriptor, error) {
	if p := providerFrom(ctx); p != nil {
		start := time.Now()
		d, err := resolveSpell(ctx, sess)
		p.RecordBuzzSpellResolve(ctx, time.Since(start).Seconds(), d.Name, builtinLabel(ctx))
		return d, err
	}
	return resolveSpell(ctx, sess)
}

func resolveSpell(ctx context.Context, sess *buzz.Session) (spells.Descriptor, error) {
	ex := sess.Exports()

	nameFn, ok := ex["mgs_getName"]
	if !ok {
		return spells.Descriptor{}, ErrNotASpell
	}
	def := vm.NewMap()
	nv, err := sess.CallValue(ctx, nameFn, nil)
	if err != nil {
		return spells.Descriptor{}, fmt.Errorf("magus/spell: mgs_getName: %w", err)
	}
	def.MapSet("name", nv)

	// Optional mgs_ functions to resolved values under the decoder's keys.
	// OptionalContract is the canonical list this loop stays in sync with.
	for _, f := range OptionalContract {
		fn, ok := ex[f.Name]
		if !ok {
			continue
		}
		rv, err := sess.CallValue(ctx, fn, nil)
		if err != nil {
			return spells.Descriptor{}, fmt.Errorf("magus/spell: %s: %w", f.Name, err)
		}
		// ops is post-processed here because its handlers can be function-valued
		// (a Buzz-only form) and need resolving to data. See contract.go.
		if f.Field == "ops" {
			rv, err = resolveOps(ctx, sess, rv)
			if err != nil {
				return spells.Descriptor{}, fmt.Errorf("magus/spell: %s: %w", f.Name, err)
			}
		}
		switch f.Shape {
		case ShapePaths:
			rv, err = pathValues(f.Name, rv)
		case ShapeManifests:
			rv, err = manifestValues(f.Name, rv)
		case ShapeStrs:
		}
		if err != nil {
			return spells.Descriptor{}, fmt.Errorf("magus/spell: %w", err)
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
	// The handler returns a Command or Service object (magus/spell); MapView yields
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

// validateCmdFields checks a Command field map: bin is a string and args/sources
// are all strings, when present.
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
	if sources, ok := m.MapGet("sources"); ok && sources.IsList() {
		for _, s := range sources.ListItems() {
			if !s.IsStr() {
				return fmt.Errorf("command sources must all be strings")
			}
		}
	}
	return nil
}

// DecodeHandle decodes a bind-time spell handle - a map of resolved native data
// built by a workspace-local spell import - into a spells.Descriptor, so a workspace-local
// Buzz spell can be registered by value at bind time.
func DecodeHandle(v vm.Value) (spells.Descriptor, error) {
	return Decode(buzzSpellObj{v: v})
}

// DecodeCommandValue decodes a single Buzz Command value (bin + args + the charm
// JSON-Patch table) into a spells.Command, reusing the same reader the engine uses
// for a spell op. It is the by-value entrypoint for a caller holding a raw Command
// map - the playground's dry run - so the sandbox and the engine agree on a
// command's shape without a second decoder. v must be a map or object instance
// (MapView'd form); an invalid charm patch is an error, as it is for the engine.
func DecodeCommandValue(v vm.Value) (spells.Command, error) {
	return decodeCommand("", "", buzzSpellObj{v: v})
}

// buzzSpellObj adapts a Buzz data map (a resolved definition or a bound handle)
// to obj. All fields are plain data - needs/provides/ops were already resolved by
// Resolve or marshaled into the handle - so there is no function-calling here.
type buzzSpellObj struct {
	v vm.Value
}

func (o buzzSpellObj) Str(key string) (string, bool) {
	x, ok := o.v.MapGet(key)
	if !ok {
		return "", false
	}
	// An enum case reads as its backing value. A field the mirror types as an
	// `enum<str>` arrives as a case object, not a string, so without this a spell
	// writing `upTo = VersionComponent.patch` would decode as absent - the silent
	// miss the enum was adopted to prevent. A plain string still passes through, so
	// both spellings decode identically.
	if ev, isEnum := x.EnumValue(); isEnum {
		x = ev
	}
	if !x.IsStr() {
		return "", false
	}
	return x.AsString(), true
}

func (o buzzSpellObj) Bool(key string) bool {
	x, ok := o.v.MapGet(key)
	return ok && x.Bool()
}

func (o buzzSpellObj) Strs(key string) ([]string, error) { return mapStrSlice(o.v, key) }

// StrMap reads key as a string-to-string map. Buzz's map backing store only ever
// holds string keys (see vm.Value.MapKeys), so the only reachable type error is a
// wrong-typed VALUE - checked here so a mistyped entry fails loudly at load rather
// than silently zeroing. Absent-vs-empty is NOT this method's problem: decodeCommand
// normalizes both to nil once, for every obj implementation.
func (o buzzSpellObj) StrMap(key string) (map[string]string, error) {
	x, ok := o.v.MapGet(key)
	if !ok {
		return nil, nil //nolint:nilnil // absent key means "declared nothing"; a nil map is that value, not an error
	}
	mv, ok := x.MapView()
	if !ok {
		return nil, fmt.Errorf("%q must be a map", key)
	}
	keys := mv.MapKeys()
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		v, _ := mv.MapGet(k)
		if !v.IsStr() {
			return nil, fmt.Errorf("%q[%q] must be a string", key, k)
		}
		out[k] = v.AsString()
	}
	return out, nil
}

func (o buzzSpellObj) Obj(key string) (obj, bool) {
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

func (o buzzSpellObj) Objs(key string) []obj {
	x, ok := o.v.MapGet(key)
	if !ok || !x.IsList() {
		return nil
	}
	var out []obj
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
	return valStrSlice(key, v)
}

// mapStrSlice reads key from a map value as a string slice, or nil when absent.
func mapStrSlice(m vm.Value, key string) ([]string, error) {
	v, ok := m.MapGet(key)
	if !ok {
		return nil, nil
	}
	return valStrSlice(key, v)
}

// valStrSlice reads a list of strings, rejecting a non-string element by index
// rather than dropping it. Dropping is what a shortened argv is made of: the
// command runs missing a flag, succeeds, and caches as if it were complete. Same
// posture as StrMap above.
func valStrSlice(key string, v vm.Value) ([]string, error) {
	if !v.IsList() {
		return nil, nil
	}
	items := v.ListItems()
	if len(items) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		if !it.IsStr() {
			return nil, fmt.Errorf("%q[%d] must be a string", key, i)
		}
		out = append(out, it.AsString())
	}
	return out, nil
}

// manifestValues validates a [Manifest] and passes it through UNREDUCED, which is
// the whole reason ShapeManifests exists: pathValues next door reduces each object to
// its .value string, and a manifest's lockCandidates would be discarded by it in
// silence, since that function only reads the keys it knows about.
//
// It validates rather than merely passing through because the alternative is the
// failure this package keeps relearning - a malformed declaration decoding to empty
// with nothing naming the cause. decodeManifests reads the structure back out with
// obj.Objs.
//
// A spell still returning [Path] passes: this checks only for a .value string, which
// both objects carry, so the pre-Manifest contract keeps loading and simply declares
// no lockfile.
func manifestValues(name string, v vm.Value) (vm.Value, error) {
	if !v.IsList() {
		return vm.Null, fmt.Errorf("%s must return [Manifest]", name)
	}
	for i, item := range v.ListItems() {
		m, ok := item.MapView()
		if !ok {
			return vm.Null, fmt.Errorf("%s[%d] must be Manifest", name, i)
		}
		value, ok := m.MapGet("value")
		if !ok || !value.IsStr() {
			return vm.Null, fmt.Errorf("%s[%d].value must be str", name, i)
		}
		// A backstop, not the primary check: an annotated `> [Manifest]` return puts the
		// object's shape in front of the checker, which rejects a mistyped field at compile
		// time with a better message than this one (BZZ1005). What the checker does NOT
		// verify is the annotation itself against this contract, so the reachable failure is
		// a wrong return type - caught by the MapView and .value checks above - and this
		// guards the same class one level down.
		locks, ok := m.MapGet("lockCandidates")
		if !ok {
			continue
		}
		if !locks.IsList() {
			return vm.Null, fmt.Errorf("%s[%d].lockCandidates must be [str]", name, i)
		}
		for j, lock := range locks.ListItems() {
			if !lock.IsStr() {
				return vm.Null, fmt.Errorf("%s[%d].lockCandidates[%d] must be str", name, i, j)
			}
		}
	}
	return v, nil
}

// pathValues is the sole MGS metadata boundary. Buzz authors use the generated
// Path object; the cache descriptor intentionally stores its lexical value because
// glob matching does not resolve filesystem paths. No reflection participates in
// this conversion: generated values are read from their fixed field map directly.
func pathValues(name string, v vm.Value) (vm.Value, error) {
	if !v.IsList() {
		return vm.Null, fmt.Errorf("%s must return [Path]", name)
	}
	items := v.ListItems()
	values := make([]vm.Value, 0, len(items))
	for i, item := range items {
		path, ok := item.MapView()
		if !ok {
			return vm.Null, fmt.Errorf("%s[%d] must be Path", name, i)
		}
		value, ok := path.MapGet("value")
		if !ok || !value.IsStr() {
			return vm.Null, fmt.Errorf("%s[%d].value must be str", name, i)
		}
		if name == "mgs_listIgnoreDirs" {
			isDir, ok := path.MapGet("isDir")
			if !ok || !isDir.IsBool() || !isDir.AsBool() {
				return vm.Null, fmt.Errorf("%s[%d] must set isDir = true", name, i)
			}
		}
		values = append(values, value)
	}
	return vm.ListValue(values), nil
}
