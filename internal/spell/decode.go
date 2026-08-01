package spell

import (
	"fmt"
	"slices"

	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/spells"
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

// Decode marshals a spell definition record into the canonical spells.Descriptor,
// resolving needs()/provides() and validating op names and charm strategies. It
// is the single reader the Buzz engine routes through, so a spell's shape is
// known in exactly one place. Decode is pure: it neither registers the spell nor
// touches any global state.
func Decode(src Obj) (spells.Descriptor, error) {
	name, _ := src.Str("name")
	if name == "" {
		return spells.Descriptor{}, fmt.Errorf("spell: name is required")
	}
	language, _ := src.Str("language")
	m := spells.Descriptor{
		Name:        name,
		Claims:      src.Strs("claims"),
		IgnoreDirs:  src.Strs("ignore_dirs"),
		Manifests:   src.Strs("manifests"),
		VersionCmd:  src.Strs("version_cmd"),
		VersionCmds: decodeVersionCmds(src),
		Language:    language,
		Opaque:      src.Bool("opaque"),
	}

	needs, err := src.CallStrs("needs")
	if err != nil {
		return spells.Descriptor{}, fmt.Errorf("spell %q: needs(): %w", name, err)
	}
	m.Needs = needs

	provides, err := src.CallStrs("provides")
	if err != nil {
		return spells.Descriptor{}, fmt.Errorf("spell %q: provides(): %w", name, err)
	}
	m.Provides = provides

	if ops, ok := src.Obj("ops"); ok {
		opMap := map[string]spells.Op{}
		// canonical op name -> the spelling that claimed it, for the collision check below.
		authored := map[string]string{}
		var docOps []string
		for _, op := range ops.Keys() {
			spec, ok := ops.Obj(op)
			if !ok {
				continue
			}
			if err := types.ValidateTargetName(op); err != nil {
				return spells.Descriptor{}, fmt.Errorf("spell %q op %q: %w", name, op, err)
			}
			// Canonicalize the key. The charset above admits '_' and uppercase, but
			// every request reaching dispatchOp has already been kebab-normalized by
			// ParseTarget, and dispatch is a plain map hit - so an op authored as
			// go_build was stored under go_build, looked up as go-build, missed, and
			// swallowed as a fan-out skip at debug level. Declared and unreachable,
			// with no error anywhere. Normalizing on the way in is the other half of
			// the rule ParseTarget already applies on the way out.
			//
			// Collapsing keys means two spellings can now land on one, so the
			// collision is checked rather than resolved: `go-build` and `goBuild` in
			// one spell used to be two ops (one of them unreachable) and would
			// otherwise become a last-write-wins overwrite - the same silent loss this
			// normalization exists to end, just moved to the other side. Which one
			// survives depends on map iteration, so it is a load error naming both.
			canonical := types.Normalize(op)
			if prior, dup := authored[canonical]; dup {
				return spells.Descriptor{}, fmt.Errorf(
					"spell %q declares ops %q and %q, which are the same op %q; rename one",
					name, prior, op, canonical)
			}
			authored[canonical] = op
			op = canonical
			t := spells.Op{Capture: spec.Bool("capture")}
			if doc, ok := spec.Str("doc"); ok {
				t.Doc = doc
			}
			// A function-authored op (vs a plain {cmd,args} record op) is a candidate
			// for the doctor doc-comment check; see spells.Descriptor.DocOps.
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
					return spells.Descriptor{}, err
				}
				svc := &spells.Service{Command: cmd}
				if readinessObj, ok := spec.Obj("readiness"); ok {
					readiness, err := decodeCommand(name, op, readinessObj)
					if err != nil {
						return spells.Descriptor{}, err
					}
					svc.Readiness = readiness
				}
				if stopObj, ok := spec.Obj("stop"); ok {
					stop, err := decodeCommand(name, op, stopObj)
					if err != nil {
						return spells.Descriptor{}, err
					}
					svc.Stop = stop
				}
				if distinct, ok := spec.Str("distinct"); ok {
					svc.Distinct = distinct
				}
				if idle, ok := spec.Str("idle"); ok {
					svc.Idle = idle
				}
				t.Kind = spells.OpKindService
				t.Service = svc
				t.Command = cmd
			} else {
				cmd, err := decodeCommand(name, op, spec)
				if err != nil {
					return spells.Descriptor{}, err
				}
				t.Command = cmd
				t.Capture = cmd.Capture
			}
			// Kind-coherence wards: reject an op whose argv contradicts its kind
			// (a detached service, a never-exiting command) at resolution time,
			// before anything forks.
			if diags := ward.Check(op, t); len(diags) > 0 {
				return spells.Descriptor{}, diags[0]
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
func decodeCommand(spellName, opName string, o Obj) (spells.Command, error) {
	c := spells.Command{Args: o.Strs("args"), Capture: o.Bool("capture")}
	if bin, ok := o.Str("bin"); ok {
		c.Bin = bin
	}
	charms, ok := o.Obj("charms")
	if !ok {
		return c, nil
	}
	cm := map[string]spells.Charm{}
	for _, cn := range charms.Keys() {
		ce, ok := charms.Obj(cn)
		if !ok {
			continue
		}
		// A charm value is { ops = [ {op, path, value?, from?}, ... ] }, an RFC 6902
		// patch over the base argv (built by the charm.* constructors at author time).
		var ch spells.Charm
		for _, opObj := range ce.Objs("ops") {
			po := spells.PatchOp{}
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
		if err := spells.ValidatePatch(ch.Ops); err != nil {
			// Qualify with spell/op only when named. The by-value entrypoint
			// (DecodeCommandValue) passes neither, so an empty `spell "" op ""` prefix
			// would read as a bug in the surfaced message; the engine path always names both.
			where := ""
			if spellName != "" || opName != "" {
				where = fmt.Sprintf("spell %q op %q ", spellName, opName)
			}
			return spells.Command{}, fmt.Errorf("%scharm %q: %w", where, cn, err)
		}
		cm[cn] = ch
	}
	if len(cm) > 0 {
		c.Charms = cm
	}
	return c, nil
}

// decodeVersionCmds reads the named additional version probes, tool name to argv.
// It uses only Obj/Keys/Strs, so a second authoring backend implementing Obj gets
// this field for free rather than needing a new method on the interface.
//
// An entry with an empty argv is dropped rather than kept as a probe that could
// never run: a spell naming a tool it cannot version is a declaration bug, and
// carrying it would put a permanent "UNPROBED" into every cache key for that
// project - noise that never resolves.
func decodeVersionCmds(src Obj) map[string][]string {
	rec, ok := src.Obj("version_cmds")
	if !ok {
		return nil
	}
	var out map[string][]string
	for _, tool := range rec.Keys() {
		argv := rec.Strs(tool)
		if len(argv) == 0 {
			continue
		}
		if out == nil {
			out = map[string][]string{}
		}
		out[tool] = argv
	}
	return out
}
