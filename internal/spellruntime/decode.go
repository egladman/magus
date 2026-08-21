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
// adapter). Decoupling Decode from the concrete value type keeps the marshaling in
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
	// bound handle, where define/load marshaled the result back as data so
	// magus.project can decode the spell by value at bind time). Absent yields
	// (nil, nil). Calling a function is the one genuinely engine-specific act:
	// Buzz calls through its session.
	CallStrs(key string, args ...string) ([]string, error)
}

// decodeManifests reads the manifests field, which is a list of records rather than
// the list of strings every other path-bearing field decodes to (see
// ContractEntry.Shape). Each record is a Manifest - the file dependencies are declared
// in, plus the lockfiles its ecosystem might resolve them into.
//
// A record carrying only .value decodes as a manifest with no lock candidates, which
// is what makes a spell written against the older [Path] contract keep loading. That
// is the entire compat surface: Path and Manifest agree on .value, and the extra Path
// fields (base, isDir) are meaningless for a manifest, so ignoring them loses nothing.
func decodeManifests(src Obj) []spells.Manifest {
	objs := src.Objs("manifests")
	if len(objs) == 0 {
		return nil
	}
	out := make([]spells.Manifest, 0, len(objs))
	for _, o := range objs {
		value, ok := o.Str("value")
		if !ok || value == "" {
			continue
		}
		out = append(out, spells.Manifest{Value: value, LockCandidates: o.Strs("lockCandidates")})
	}
	return out
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
	tools, err := decodeTools(src)
	if err != nil {
		return spells.Descriptor{}, fmt.Errorf("spell %q: %w", name, err)
	}
	m := spells.Descriptor{
		Name:       name,
		IgnoreDirs: src.Strs("ignore_dirs"),
		Manifests:  decodeManifests(src),
		Tools:      tools,
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
				// Secrets on a service op are rejected here, not silently dropped
				// later: the supervised path rebuilds the command without its env,
				// so a declared secret would reach a foregrounded service and
				// vanish from a supervised one - the same op behaving differently
				// by how it was reached. Until the supervisor threads env through,
				// refusing at load is the only honest answer.
				if len(cmd.Secrets) > 0 {
					return spells.Descriptor{}, fmt.Errorf("spell %q op %q: secrets are not supported on a service op yet", name, op)
				}
				// Hints are refused on a service op for the same reason and with the
				// same shape of answer. Reached as a dependency the op is handed to
				// the supervisor and never runs through runCommand, so its advice
				// could not fire; run directly it foregrounds and would fire - on
				// every Ctrl-C shutdown, since that is how a foregrounded service
				// ends. Advice that depends on how the op was reached, and that
				// mostly fires on a normal exit, is worse than none.
				if len(cmd.Hints) > 0 {
					return spells.Descriptor{}, fmt.Errorf("spell %q op %q: hints are not supported on a service op (a supervised service never reaches the classifier, and a foregrounded one ends by being stopped)", name, op)
				}
				svc := &spells.Service{Command: cmd}
				if readinessObj, ok := spec.Obj("readiness"); ok {
					readiness, err := decodeCommand(name, op, readinessObj)
					if err != nil {
						return spells.Descriptor{}, err
					}
					if len(readiness.Secrets) > 0 {
						return spells.Descriptor{}, fmt.Errorf("spell %q op %q: secrets are not supported on a service readiness command", name, op)
					}
					// A readiness probe is polled by the supervisor, not run through
					// runCommand, so advice declared here can never fire. Refuse it
					// rather than accept a declaration that does nothing.
					if len(readiness.Hints) > 0 {
						return spells.Descriptor{}, fmt.Errorf("spell %q op %q: hints are not supported on a service readiness command (the supervisor polls it directly, so they could never fire)", name, op)
					}
					svc.Readiness = readiness
				}
				if stopObj, ok := spec.Obj("stop"); ok {
					stop, err := decodeCommand(name, op, stopObj)
					if err != nil {
						return spells.Descriptor{}, err
					}
					if len(stop.Secrets) > 0 {
						return spells.Descriptor{}, fmt.Errorf("spell %q op %q: secrets are not supported on a service stop command", name, op)
					}
					// Same as readiness: the supervisor runs stop, so advice here is inert.
					if len(stop.Hints) > 0 {
						return spells.Descriptor{}, fmt.Errorf("spell %q op %q: hints are not supported on a service stop command (the supervisor runs it directly, so they could never fire)", name, op)
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

// validEnvName reports whether s is usable as an environment variable name:
// letters, digits and underscores, not starting with a digit. The POSIX portable
// set - anything looser differs by platform, and a secret landing under a
// misparsed name fails somewhere far from the declaration.
func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// decodeCommand reads a Command field map (bin/args/charms), validating each charm's
// RFC 6902 patch. It is shared by a command op and by each of a service op's
// run/ready/stop commands, so every command shape decodes identically.
func decodeCommand(spellName, opName string, o Obj) (spells.Command, error) {
	c := spells.Command{
		Args:        o.Strs("args"),
		Capture:     o.Bool("capture"),
		Sources:     o.Strs("sources"),
		SourcesEach: o.Bool("sourcesEach"),
	}
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
	for envName := range secrets {
		// The key becomes `name=value` in the child environment, so a key carrying
		// "=" would silently retarget a different variable and an empty key would
		// emit "=value"; neither is a spelling of anything an author meant.
		if !validEnvName(envName) {
			return spells.Command{}, fmt.Errorf("%scommand secrets: %q is not an environment variable name (want letters, digits and underscores, not starting with a digit)", where, envName)
		}
	}
	if len(secrets) == 0 {
		// Normalized HERE, once, so "declared nothing" and "declared an empty map"
		// are one value with one cache spelling, and no Obj implementation has to
		// remember the rule.
		secrets = nil
	}
	c.Secrets = secrets
	// Failure advice, in declaration order - that order IS the precedence, so it must
	// survive decode unsorted. A half-written rule is rejected rather than dropped: a
	// rule with no `contains` matches every string and would advise on every failure of
	// this command, and one with no `advise` would consume the match and print nothing,
	// which reads as "magus had no idea" when in fact it matched and had nothing to say.
	for i, ho := range o.Objs("hints") {
		var h spells.Hint
		h.Contains, _ = ho.Str("contains")
		h.Advise, _ = ho.Str("advise")
		if h.Contains == "" || h.Advise == "" {
			return spells.Command{}, fmt.Errorf("%scommand hints[%d]: both contains and advise are required (contains %q, advise %q)", where, i, h.Contains, h.Advise)
		}
		c.Hints = append(c.Hints, h)
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
		// A malformed bound is knowable without running anything, and a window nobody
		// can parse protects nobody - the same reasoning magus.yaml's required_version
		// applies to its own floor. Rejecting here is what keeps Check's VerdictUnknown
		// a backstop for authored input rather than its normal path.
		// A slice, not a map: two bad bounds on one tool must always name the same one
		// first, or the load error moves between runs.
		for _, b := range []struct{ field, bound string }{
			{"min", m.Tools[tool].Supported.Min},
			{"below", m.Tools[tool].Supported.Below},
		} {
			field, bound := b.field, b.bound
			if bound == "" {
				continue
			}
			if _, err := semver.NewVersion(bound); err != nil {
				return fmt.Errorf("spell %q: tools[%q].supported.%s %q is not a valid version: %w",
					m.Name, tool, field, bound, err)
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
func decodeTools(src Obj) (map[string]spells.Tool, error) {
	rec, ok := src.Obj("tools")
	if !ok {
		//nolint:nilnil // declaring no tools is not an error, and a nil map is the correct empty value here: every caller ranges it or takes its len, both of which read a nil map fine.
		return nil, nil
	}
	var out map[string]spells.Tool
	for _, name := range rec.Keys() {
		o, ok := rec.Obj(name)
		if !ok {
			continue
		}
		t := spells.Tool{}
		if pr, ok := o.Obj("probe"); ok {
			cmd, err := decodeCommand("", "", pr)
			if err != nil {
				return nil, fmt.Errorf("tools[%q].probe: %w", name, err)
			}
			t.Probe = cmd
		}
		if k, ok := o.Obj("key"); ok {
			if c, ok := k.Str("const"); ok {
				t.Key.Const = c
			}
			if u, ok := k.Str("upTo"); ok {
				t.Key.UpTo = spells.VersionComponent(u)
			}
		}
		if s, ok := o.Obj("supported"); ok {
			if m, ok := s.Str("min"); ok {
				t.Supported.Min = m
			}
			if b, ok := s.Str("below"); ok {
				t.Supported.Below = b
			}
		}
		if r, ok := o.Obj("ready"); ok {
			cmd, err := decodeCommand("", "", r)
			if err != nil {
				return nil, fmt.Errorf("tools[%q].ready: %w", name, err)
			}
			t.Ready = cmd
		}
		if d, ok := o.Str("diagnostics"); ok {
			t.Diagnostics = spells.DiagnosticFormat(d)
		}
		if t.Probe.Bin == "" && t.Key.IsZero() && t.Ready.Bin == "" && t.Supported.IsZero() &&
			t.Diagnostics == spells.DiagnosticNone {
			continue
		}
		if out == nil {
			out = map[string]spells.Tool{}
		}
		out[name] = t
	}
	return out, nil
}
