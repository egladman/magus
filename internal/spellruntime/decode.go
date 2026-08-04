package spellruntime

import (
	"fmt"
	semver "github.com/Masterminds/semver/v3"
	"maps"
	"slices"
	"strings"

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
	// StrMap returns the string-to-string map at key (a Command's `secrets`: env var
	// name -> provider reference); absent or empty yields (nil, nil). A present value
	// that is not a string is an error naming key, since a mistyped entry here would
	// otherwise resolve nothing at spawn with no signal until the run that needed it.
	StrMap(key string) (map[string]string, error)
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
		Name:       name,
		Claims:     src.Strs("claims"),
		IgnoreDirs: src.Strs("ignore_dirs"),
		Manifests:  src.Strs("manifests"),
		Tools:      decodeTools(src),
		Language:   language,
		Opaque:     src.Bool("opaque"),
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
	// Checked here rather than at probe time: an unusable component is a declaration
	// bug knowable without running anything, and discovering it from a cache that
	// silently never narrows is the failure this prevents.
	if err := validateTools(m); err != nil {
		return spells.Descriptor{}, err
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
	// Qualify with spell/op only when named. The by-value entrypoint
	// (DecodeCommandValue) passes neither, so an empty `spell "" op ""` prefix would
	// read as a bug in the surfaced message; the engine path always names both.
	where := ""
	if spellName != "" || opName != "" {
		where = fmt.Sprintf("spell %q op %q ", spellName, opName)
	}
	secrets, err := o.StrMap("secrets")
	if err != nil {
		return spells.Command{}, fmt.Errorf("%scommand secrets: %w", where, err)
	}
	c.Secrets = secrets
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
			// Str unwraps a PatchOpKind enum case to its backing string, so both the
			// enum spelling charm.buzz uses and a bare "add" from a hand-written record
			// decode the same way.
			opName, _ := opObj.Str("op")
			po.Op = spells.PatchOpKind(opName)
			po.Path, _ = opObj.Str("path")
			if v, ok := opObj.Str("value"); ok {
				po.Value = v
			}
			if f, ok := opObj.Str("fromPtr"); ok {
				po.From = f
			}
			ch.Ops = append(ch.Ops, po)
		}
		if err := spells.ValidatePatch(ch.Ops); err != nil {
			return spells.Command{}, fmt.Errorf("%scharm %q: %w", where, cn, err)
		}
		cm[cn] = ch
	}
	if len(cm) > 0 {
		c.Charms = cm
	}
	return c, nil
}

// validateTools reports the first unusable declaration in a decoded descriptor.
// Separate from decoding so the error can name the spell, and so a caller that only
// reads a descriptor (docs, graph extraction) is not forced to handle it.
func validateTools(m spells.Descriptor) error {
	for _, tool := range slices.Sorted(maps.Keys(m.Tools)) {
		// A malformed constraint is knowable without running anything, and a floor
		// nobody can parse protects nobody - the same reasoning magus.yaml's
		// required_version applies to its own floor.
		if f := m.Tools[tool].Floor; f != "" {
			if _, err := semver.NewConstraint(f); err != nil {
				return fmt.Errorf("spell %q: tools[%q].floor %q is not a valid semver constraint: %w",
					m.Name, tool, f, err)
			}
		}
		if c := m.Tools[tool].Key.UpTo; !c.Valid() {
			// The candidate list comes from the same registry that generates the enum,
			// so adding a component cannot leave this message stale.
			return fmt.Errorf("spell %q: tools[%q].key.upTo is %s; want one of %s",
				m.Name, tool, c, strings.Join(c.Values(), ", "))
		}
		if d := m.Tools[tool].Diagnostics; !d.Valid() {
			return fmt.Errorf("spell %q: tools[%q].diagnostics is %s; want one of %s",
				m.Name, tool, d, strings.Join(d.Values(), ", "))
		}
	}
	return nil
}

// decodeTools reads the per-binary declarations: what prints its version, what part of
// that keys the cache, and what proves it is usable. An entry with none of the three is
// dropped rather than kept as a tool magus knows nothing about.
func decodeTools(src Obj) map[string]spells.Tool {
	rec, ok := src.Obj("tools")
	if !ok {
		return nil
	}
	var out map[string]spells.Tool
	for _, name := range rec.Keys() {
		o, ok := rec.Obj(name)
		if !ok {
			continue
		}
		t := spells.Tool{}
		if pr, ok := o.Obj("probe"); ok {
			if cmd, err := decodeCommand("", "", pr); err == nil {
				t.Probe = cmd
			}
		}
		if k, ok := o.Obj("key"); ok {
			if c, ok := k.Str("const"); ok {
				t.Key.Const = c
			}
			if u, ok := k.Str("upTo"); ok {
				t.Key.UpTo = spells.VersionComponent(u)
			}
		}
		if f, ok := o.Str("floor"); ok {
			t.Floor = f
		}
		if r, ok := o.Obj("ready"); ok {
			cmd, err := decodeCommand("", "", r)
			if err == nil {
				t.Ready = cmd
			}
		}
		if d, ok := o.Str("diagnostics"); ok {
			t.Diagnostics = spells.DiagnosticFormat(d)
		}
		if t.Probe.Bin == "" && t.Key.IsZero() && t.Ready.Bin == "" && t.Floor == "" &&
			t.Diagnostics == spells.DiagnosticNone {
			continue
		}
		if out == nil {
			out = map[string]spells.Tool{}
		}
		out[name] = t
	}
	return out
}
