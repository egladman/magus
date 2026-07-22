package spell

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// Obj is a read view over a spell record (a Buzz map, wrapped in the buzzSpellObj
// adapter). Decoupling Decode from the concrete value type keeps the marshalling in
// one place: Obj is the single boundary that knows a spell's shape, and the seam a
// second authoring backend would implement.
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
	// the field takes across a spell's life: a function (in a definition,
	// needs()/provides() are called with args) or an already-resolved list (in a
	// bound handle, where define/load marshalled the result back as data so
	// magus.project can decode the spell by value at bind time). Absent yields
	// (nil, nil). Calling a function is the one genuinely engine-specific act:
	// Buzz calls through its session.
	CallStrs(key string, args ...string) ([]string, error)
}

// Decode marshals a spell definition record into the canonical Descriptor,
// resolving needs()/provides() and validating op names and charm strategies. It
// is the single reader the Buzz engine routes through, so a spell's shape is
// known in exactly one place. Decode is pure: it neither registers the spell nor
// touches any global state.
func Decode(src Obj) (Descriptor, error) {
	name, _ := src.Str("name")
	if name == "" {
		return Descriptor{}, fmt.Errorf("spell: name is required")
	}
	language, _ := src.Str("language")
	m := Descriptor{
		Name:       name,
		Claims:     src.Strs("claims"),
		IgnoreDirs: src.Strs("ignore_dirs"),
		VersionCmd: src.Strs("version_cmd"),
		Language:   language,
		Opaque:     src.Bool("opaque"),
	}

	needs, err := src.CallStrs("needs", "")
	if err != nil {
		return Descriptor{}, fmt.Errorf("spell %q: needs(): %w", name, err)
	}
	m.Needs = needs

	provides, err := src.CallStrs("provides")
	if err != nil {
		return Descriptor{}, fmt.Errorf("spell %q: provides(): %w", name, err)
	}
	m.Provides = provides

	if ops, ok := src.Obj("ops"); ok {
		opMap := map[string]types.SpellOp{}
		var docOps []string
		for _, op := range ops.Keys() {
			spec, ok := ops.Obj(op)
			if !ok {
				continue
			}
			if err := types.ValidateTargetName(op); err != nil {
				return Descriptor{}, fmt.Errorf("spell %q op %q: %w", name, op, err)
			}
			t := types.SpellOp{Capture: spec.Bool("capture")}
			if doc, ok := spec.Str("doc"); ok {
				t.Doc = doc
			}
			// A function-authored op (vs a plain {cmd,args} record op) is a candidate
			// for the doctor doc-comment check; see Descriptor.DocOps.
			if spec.Bool("handler") {
				docOps = append(docOps, op)
			}
			// A service op is recognized by its `command` field: a Service whose
			// `command` is the long-running process, with optional `readiness` and
			// `stop` commands. The op's embedded Command mirrors `command` so the
			// fork/render/cache paths read every op uniformly. A command op (the
			// default) decodes its Command directly.
			if cmdObj, ok := spec.Obj("command"); ok {
				cmd, err := decodeCommand(name, op, cmdObj)
				if err != nil {
					return Descriptor{}, err
				}
				svc := &types.Service{Command: cmd}
				if readinessObj, ok := spec.Obj("readiness"); ok {
					readiness, err := decodeCommand(name, op, readinessObj)
					if err != nil {
						return Descriptor{}, err
					}
					svc.Readiness = readiness
				}
				if stopObj, ok := spec.Obj("stop"); ok {
					stop, err := decodeCommand(name, op, stopObj)
					if err != nil {
						return Descriptor{}, err
					}
					svc.Stop = stop
				}
				if distinct, ok := spec.Str("distinct"); ok {
					svc.Distinct = distinct
				}
				if idle, ok := spec.Str("idle"); ok {
					svc.Idle = idle
				}
				t.Kind = types.OpKindService
				t.Service = svc
				t.Command = cmd
			} else {
				cmd, err := decodeCommand(name, op, spec)
				if err != nil {
					return Descriptor{}, err
				}
				t.Command = cmd
			}
			// Kind-coherence wards: reject an op whose argv contradicts its kind
			// (a detached service, a never-exiting command) at resolution time,
			// before anything forks.
			if diags := ward.Check(op, t); len(diags) > 0 {
				return Descriptor{}, diags[0]
			}
			opMap[op] = t
		}
		if len(opMap) > 0 {
			m.Ops = opMap
		}
		if len(docOps) > 0 {
			slices.Sort(docOps)
			m.DocOps = docOps
		}
	}
	return m, nil
}

// decodeCommand reads a Command field map (bin/args/charms), validating each charm's
// RFC 6902 patch. It is shared by a command op and by each of a service op's
// run/ready/stop commands, so every command shape decodes identically.
func decodeCommand(spellName, opName string, o Obj) (types.Command, error) {
	c := types.Command{Args: o.Strs("args")}
	if bin, ok := o.Str("bin"); ok {
		c.Bin = bin
	}
	charms, ok := o.Obj("charms")
	if !ok {
		return c, nil
	}
	cm := map[string]types.Charm{}
	for _, cn := range charms.Keys() {
		ce, ok := charms.Obj(cn)
		if !ok {
			continue
		}
		// A charm value is { ops = [ {op, path, value?, from?}, ... ] }, an RFC 6902
		// patch over the base argv (built by the charm.* constructors at author time).
		var ch types.Charm
		for _, opObj := range ce.Objs("ops") {
			po := types.PatchOp{}
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
			// Qualify with spell/op only when named. The by-value entrypoint
			// (DecodeCommandValue) passes neither, so an empty `spell "" op ""` prefix
			// would read as a bug in the surfaced message; the engine path always names both.
			where := ""
			if spellName != "" || opName != "" {
				where = fmt.Sprintf("spell %q op %q ", spellName, opName)
			}
			return types.Command{}, fmt.Errorf("%scharm %q: %w", where, cn, err)
		}
		cm[cn] = ch
	}
	if len(cm) > 0 {
		c.Charms = cm
	}
	return c, nil
}
