package spell

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/types"
)

// Obj is a read view over a host-language record — a Lua table or a Buzz map.
// Each engine wraps its native value in a small adapter (~15 lines), so Decode
// reads a spell definition once, identically, regardless of authoring language.
// It is the marshalling boundary: the single place that knows a spell's shape.
type Obj interface {
	// Str returns the string at key and whether it was present as a string.
	Str(key string) (string, bool)
	// Bool returns the bool at key; absent or non-bool yields false.
	Bool(key string) bool
	// Strs returns the list-of-strings at key; absent or empty yields nil.
	Strs(key string) []string
	// Obj returns the nested record at key and whether it was present as one.
	Obj(key string) (Obj, bool)
	// Objs returns the list of nested records at key, for a field that is an
	// array of objects (a charm's JSON Patch ops). Absent or non-array yields nil.
	Objs(key string) []Obj
	// Keys returns this record's keys, for iterating ops and charms.
	Keys() []string
	// CallStrs resolves the field at key to a []string. It accepts either form
	// the field takes across a spell's life: a function (in a definition —
	// needs()/provides() are called with args) or an already-resolved list (in a
	// bound handle, where define/load marshalled the result back as data so
	// project.register can decode the spell by value at bind time). Absent yields
	// (nil, nil). Calling a function is the one genuinely engine-specific act:
	// Lua calls through its runtime, Buzz through its session.
	CallStrs(key string, args ...string) ([]string, error)
}

// Decode marshals a spell definition record into the canonical Spec,
// resolving needs()/provides() and validating op names and charm strategies. It
// is the single reader both the Lua/Teal and Buzz engines route through, so the
// two languages cannot drift. Decode is pure: it neither registers the spell nor
// touches any global state.
func Decode(src Obj) (Spec, error) {
	name, _ := src.Str("name")
	if name == "" {
		return Spec{}, fmt.Errorf("spell: name is required")
	}
	m := Spec{
		Name:       name,
		Claims:     src.Strs("claims"),
		VersionCmd: src.Strs("version_cmd"),
		Opaque:     src.Bool("opaque"),
	}

	needs, err := src.CallStrs("needs", "")
	if err != nil {
		return Spec{}, fmt.Errorf("spell %q: needs(): %w", name, err)
	}
	m.Needs = needs

	provides, err := src.CallStrs("provides")
	if err != nil {
		return Spec{}, fmt.Errorf("spell %q: provides(): %w", name, err)
	}
	m.Provides = provides

	if ops, ok := src.Obj("ops"); ok {
		targets := map[string]Target{}
		var docTargets []string
		for _, op := range ops.Keys() {
			spec, ok := ops.Obj(op)
			if !ok {
				continue
			}
			if err := types.ValidateTargetName(op); err != nil {
				return Spec{}, fmt.Errorf("spell %q op %q: %w", name, op, err)
			}
			t := Target{Capture: spec.Bool("capture"), Args: spec.Strs("args")}
			if doc, ok := spec.Str("doc"); ok {
				t.Doc = doc
			}
			// A function-authored target (vs a plain {cmd,args} record op) is a
			// candidate for the doctor doc-comment check; see Spec.DocTargets.
			if spec.Bool("handler") {
				docTargets = append(docTargets, op)
			}
			// An op declared with "fn" is a function-op dispatched in-VM; otherwise
			// it is a fork target running the declared command. Mutually exclusive.
			if fn, ok := spec.Str("fn"); ok && fn != "" {
				t.Func = fn
			} else if cmd, ok := spec.Str("cmd"); ok {
				t.Cmd = cmd
			}
			if charms, ok := spec.Obj("charms"); ok {
				cm := map[string]Charm{}
				for _, cn := range charms.Keys() {
					ce, ok := charms.Obj(cn)
					if !ok {
						continue
					}
					// A charm value is { ops = [ {op, path, value?, from?}, ... ] },
					// an RFC 6902 patch over the base argv (built by the charm.*
					// constructors at author time).
					var ch Charm
					for _, opObj := range ce.Objs("ops") {
						po := PatchOp{}
						po.Op, _ = opObj.Str("op")
						po.Path, _ = opObj.Str("path")
						if v, ok := opObj.Str("value"); ok {
							po.Value = v
						}
						if f, ok := opObj.Str("from"); ok {
							po.From = f
						}
						ch.Ops = append(ch.Ops, po)
					}
					if err := ValidatePatch(ch.Ops); err != nil {
						return Spec{}, fmt.Errorf("spell %q op %q charm %q: %w", name, op, cn, err)
					}
					cm[cn] = ch
				}
				if len(cm) > 0 {
					t.Charms = cm
				}
			}
			targets[op] = t
		}
		if len(targets) > 0 {
			m.Targets = targets
		}
		if len(docTargets) > 0 {
			slices.Sort(docTargets)
			m.DocTargets = docTargets
		}
	}
	return m, nil
}
