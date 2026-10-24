package spell

import (
	"cmp"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/symbols"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/scip-code/scip/bindings/go/scip"
)

// Descriptor checks what Resolve decoded and assembles the spells.Descriptor. The
// decoders have already refused a wrong kind, an unknown member and a bad enum case, so
// what is left is meaning: a valid op name, a charm patch that applies, a lockfile that
// is one of the manifest's candidates. It registers nothing.
func (r Resolved) Descriptor() (spells.Descriptor, error) {
	name := r.Contract.Name
	if name == "" {
		return spells.Descriptor{}, fmt.Errorf("spell: name is required")
	}
	m, err := r.declared()
	if err != nil {
		return spells.Descriptor{}, fmt.Errorf("spell %q: %w", name, err)
	}
	if m.Sandbox != nil {
		if err := validateSandbox(*m.Sandbox); err != nil {
			return spells.Descriptor{}, types.DiagnosticErrorf(types.AllowlistUnresolved, "spell %q: mgs_getSandbox: %v", name, err)
		}
	}
	if err := r.assembleOps(&m); err != nil {
		return spells.Descriptor{}, err
	}
	if err := applyModeArgs(&m, r.Contract.ModeArgs); err != nil {
		return spells.Descriptor{}, err
	}
	if err := addSymbolIndexOp(&m); err != nil {
		return spells.Descriptor{}, err
	}
	if err := synthesizeInstall(&m); err != nil {
		return spells.Descriptor{}, err
	}
	if err := validateTools(m); err != nil {
		return spells.Descriptor{}, err
	}
	return m, nil
}

// declared validates the contract's load-time answers other than the ops and the sandbox,
// and returns the descriptor they make. Its errors do not name the spell.
func (r Resolved) declared() (spells.Descriptor, error) {
	c := r.Contract
	lang, err := validateLanguage(c.Language)
	if err != nil {
		return spells.Descriptor{}, err
	}
	tools, err := validateToolDecls(c.Tools)
	if err != nil {
		return spells.Descriptor{}, err
	}
	ignoreDirs, err := ignoreDirValues(c.IgnoreDirs)
	if err != nil {
		return spells.Descriptor{}, err
	}
	manifests, err := validateManifests(c.Manifests)
	if err != nil {
		return spells.Descriptor{}, err
	}
	for i, runner := range c.ScriptRunners {
		if err := validateCommand("", "", runner); err != nil {
			return spells.Descriptor{}, fmt.Errorf("mgs_listScriptRunners[%d]: %w", i, err)
		}
	}
	if err := spells.ValidateScriptRunners(c.ScriptRunners); err != nil {
		return spells.Descriptor{}, err
	}
	var indexer *spells.SymbolIndexer
	if c.SymbolIndexer != nil {
		si := *c.SymbolIndexer
		if err := validateSymbolIndexer(c.Name, &si); err != nil {
			return spells.Descriptor{}, err
		}
		indexer = &si
	}
	return spells.Descriptor{
		Name:               c.Name,
		Needs:              globValues(c.RequiredGlobs),
		Provides:           globValues(c.ProvidedGlobs),
		IgnoreDirs:         ignoreDirs,
		Manifests:          manifests,
		ScriptRunners:      c.ScriptRunners,
		Tools:              tools,
		Language:           lang.Name,
		LanguageExtensions: lang.Extensions,
		Syntax:             lang.Syntax,
		SymbolIndexer:      indexer,
		Sandbox:            c.Sandbox,
		Opaque:             c.Opaque,
	}, nil
}

// addSymbolIndexOp registers m's declared indexer as an op under its reserved name.
func addSymbolIndexOp(m *spells.Descriptor) error {
	indexer := m.SymbolIndexer
	// An authored op under the reserved name collides. Before mgs_getSymbolIndexer,
	// declaring `scip` WAS the capability, and accepting it would run the indexer with an
	// unresolved $MAGUS_SYMBOL_INDEX.
	for _, reserved := range []string{spells.DefaultSymbolIndexOp, indexer.OpName()} {
		if _, authored := m.Ops[reserved]; authored {
			return fmt.Errorf("spell %q declares an op named %q, which is magus's own name for a declared symbol indexer, move the command to `export fun mgs_getSymbolIndexer() > SymbolIndexer` and drop the op", m.Name, reserved)
		}
	}
	if indexer == nil {
		return nil
	}
	if m.Ops == nil {
		m.Ops = map[string]spells.Op{}
	}
	// Synthesized, so the run, cache and freshness paths reach the indexer as an
	// ordinary command op; the kind is what the runner matches on.
	// Quiet: scip-go repaints a progress line with carriage returns, which a
	// non-terminal log keeps as one long line. A failure still prints stderr.
	command := indexer.Command
	command.Capture, command.Quiet = true, true
	m.Ops[indexer.OpName()] = spells.Op{Kind: spells.OpKindSymbolIndex, Command: command}
	return nil
}

// assembleOps turns the spell's target and service handlers into m.Ops and m.DocOps.
func (r Resolved) assembleOps(m *spells.Descriptor) error {
	c, name := r.Contract, r.Contract.Name
	ops := map[string]spells.Op{}
	names := opNames{spell: name, claimed: map[string]string{}}
	for _, op := range slices.Sorted(maps.Keys(c.Targets)) {
		canonical, err := names.claim(op)
		if err != nil {
			return err
		}
		t, err := targetOp(name, canonical, c.Targets[op], r.TargetDocs[op])
		if err != nil {
			return err
		}
		ops[canonical] = t
	}
	for _, op := range slices.Sorted(maps.Keys(c.Services)) {
		canonical, err := names.claim(op)
		if err != nil {
			return err
		}
		t, err := serviceOp(name, canonical, c.Services[op], r.TargetDocs[op])
		if err != nil {
			return err
		}
		ops[canonical] = t
	}
	if len(ops) > 0 {
		m.Ops = ops
	}
	if len(names.docOps) > 0 {
		slices.Sort(names.docOps)
		m.DocOps = names.docOps
	}
	return nil
}

// opNames claims a spell's op names in their canonical spelling.
type opNames struct {
	spell   string
	claimed map[string]string
	docOps  []string
}

func (n *opNames) claim(op string) (string, error) {
	if err := types.ValidateTargetName(op); err != nil {
		return "", fmt.Errorf("spell %q op %q: %w", n.spell, op, err)
	}
	// Every request reaching dispatch is already kebab-normalized, so an op stored
	// as go_build would be unreachable. Two spellings can now land on one key, which
	// is an error naming both rather than a silent overwrite.
	canonical := types.Normalize(op)
	if prior, dup := n.claimed[canonical]; dup {
		return "", fmt.Errorf("spell %q declares ops %q and %q, which are the same op %q, rename one", n.spell, prior, op, canonical)
	}
	n.claimed[canonical] = op
	n.docOps = append(n.docOps, canonical)
	return canonical, nil
}

func targetOp(spellName, canonical string, cmd spells.Command, doc string) (spells.Op, error) {
	if err := validateCommand(spellName, canonical, cmd); err != nil {
		return spells.Op{}, err
	}
	t := spells.Op{Command: cmd, Capture: cmd.Capture, Doc: doc}
	if diags := ward.Check(canonical, t); len(diags) > 0 {
		return spells.Op{}, diags[0]
	}
	return t, nil
}

func serviceOp(spellName, canonical string, svc spells.Service, doc string) (spells.Op, error) {
	if err := validateService(spellName, canonical, svc); err != nil {
		return spells.Op{}, err
	}
	// The op's embedded Command mirrors Command, or Start when set, so the fork,
	// render and cache paths read every op uniformly.
	t := spells.Op{Kind: spells.OpKindService, Service: &svc, Command: svc.Command, Doc: doc}
	if svc.Start.Bin != "" {
		t.Command = svc.Start
	}
	if diags := ward.Check(canonical, t); len(diags) > 0 {
		return spells.Op{}, diags[0]
	}
	return t, nil
}

// globValues reduces declared Paths to their lexical values: glob matching does not
// resolve filesystem paths, so a Path's base has nothing to add.
func globValues(paths []types.Path) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = p.Value
	}
	return out
}

func ignoreDirValues(dirs []types.Path) ([]string, error) {
	for i, d := range dirs {
		if !d.IsDir {
			return nil, fmt.Errorf("mgs_listIgnoreDirs[%d] must set isDir = true", i)
		}
	}
	return globValues(dirs), nil
}

func validateManifests(ms []spells.Manifest) ([]spells.Manifest, error) {
	if len(ms) == 0 {
		return nil, nil
	}
	out := make([]spells.Manifest, len(ms))
	for i, m := range ms {
		if m.Value == "" {
			return nil, fmt.Errorf("mgs_listManifests[%d].value is required", i)
		}
		installs, err := validateInstalls(m)
		if err != nil {
			return nil, err
		}
		m.Installs = installs
		out[i] = m
	}
	return out, nil
}

// validateInstalls checks one manifest's installs and returns them with each name
// normalized. Every rule is a declaration bug knowable at load, so each is an error
// rather than an install that misbehaves in one project later.
func validateInstalls(m spells.Manifest) (map[string]spells.Install, error) {
	if len(m.Installs) == 0 {
		return nil, nil //nolint:nilnil // a manifest that declares no installs is not an error; nil is that answer
	}
	out := make(map[string]spells.Install, len(m.Installs))
	for _, lock := range slices.Sorted(maps.Keys(m.Installs)) {
		in := m.Installs[lock]
		where := fmt.Sprintf("manifests[%q].installs[%q]", m.Value, lock)
		if !slices.Contains(m.LockCandidates, lock) {
			return nil, fmt.Errorf("%s: %q is not one of this manifest's lockCandidates %v, so no project could ever select it", where, lock, m.LockCandidates)
		}
		if in.Name == "" {
			return nil, fmt.Errorf("%s: name is required, the op magus registers this install under", where)
		}
		if err := types.ValidateTargetName(in.Name); err != nil {
			return nil, fmt.Errorf("%s: name %q: %w", where, in.Name, err)
		}
		in.Name = types.Normalize(in.Name)
		if err := validateCommand("", in.Name, in.Command); err != nil {
			return nil, fmt.Errorf("%s: %w", where, err)
		}
		if in.Command.Bin == "" {
			return nil, fmt.Errorf("%s: command has no bin", where)
		}
		if in.Relocatable && in.Dir == "" {
			return nil, fmt.Errorf("%s: relocatable names no dir to seed", where)
		}
		for _, p := range slices.Concat([]string{in.Dir}, in.Stamps, in.Inputs) {
			if p != "" && !filepath.IsLocal(p) {
				return nil, fmt.Errorf("%s: %q must be a path inside the project", where, p)
			}
		}
		out[lock] = in
	}
	return out, nil
}

// synthesizeInstall registers one install op per Name a spell's manifests declare,
// after checking each install's tools against the ones the spell declares. Entries
// sharing a Name (typescript's package-lock.json and npm-shrinkwrap.json both run
// npm-ci) register as ONE op, scoped to just their own manifest and lock candidates,
// so resolving it requires that op's own lockfile rather than any of the spell's.
func synthesizeInstall(m *spells.Descriptor) error {
	var order []string
	byName := map[string]*spells.Manifest{}
	for _, man := range m.Manifests {
		for _, lock := range man.LockCandidates {
			in, ok := man.Installs[lock]
			if !ok {
				continue
			}
			for _, tool := range in.Tools {
				if _, ok := m.Tools[tool]; !ok {
					return fmt.Errorf("spell %q manifests[%q].installs[%q]: tool %q is not declared in mgs_getTools", m.Name, man.Value, lock, tool)
				}
			}
			fm, ok := byName[in.Name]
			if !ok {
				fm = &spells.Manifest{Value: man.Value, Installs: map[string]spells.Install{}}
				byName[in.Name] = fm
				order = append(order, in.Name)
			}
			fm.LockCandidates = append(fm.LockCandidates, lock)
			fm.Installs[lock] = in
		}
	}
	if len(order) == 0 {
		return nil
	}
	for _, name := range order {
		if _, authored := m.Ops[name]; authored {
			return fmt.Errorf("spell %q declares an op named %q and manifest installs, magus registers the installs under that name, so drop the op", m.Name, name)
		}
	}
	if m.Ops == nil {
		m.Ops = map[string]spells.Op{}
	}
	for _, name := range order {
		fm := byName[name]
		first := fm.Installs[fm.LockCandidates[0]]
		m.Ops[name] = spells.Op{
			Kind:    spells.OpKindInstall,
			Command: first.Command,
			Install: &spells.InstallSpec{Spell: m.Name, Manifests: []spells.Manifest{*fm}},
		}
	}
	return nil
}

// validEnvName reports whether s is usable as an environment variable name:
// letters, digits and underscores, not starting with a digit. The POSIX portable
// set: anything looser differs by platform, and a secret landing under a
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

// validateTools reports the first unusable declaration in a decoded descriptor.
// Separate from decoding so the error can name the spell, and so a caller that only
// reads a descriptor (docs, graph extraction) is not forced to handle it.
func validateTools(m spells.Descriptor) error {
	// A used tool with no version probe would key the index on nothing, so an upgrade
	// that changes what the indexer reads could never stale it.
	if m.SymbolIndexer != nil {
		for _, tool := range m.SymbolIndexer.Uses {
			if m.Tools[tool].Probe.Bin == "" {
				return fmt.Errorf("spell %q: symbol indexer uses %q, which mgs_getTools does not declare with a version probe",
					m.Name, tool)
			}
		}
	}
	for _, tool := range slices.Sorted(maps.Keys(m.Tools)) {
		// A malformed bound is knowable without running anything, and a window nobody
		// can parse protects nobody, the same reasoning magus.yaml's required_version
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
			// spells.ValidBound, not a second parser: a bound only this side accepts
			// loads fine and then fails every comparison as VerdictUnknown, which is
			// the silent no-op the check exists to prevent.
			if !spells.ValidBound(bound) {
				return fmt.Errorf("spell %q: tools[%q].supported.%s %q is not a valid version, want a plain version like \"1.21\" or \"2.0.0\"",
					m.Name, tool, field, bound)
			}
		}
		if c := m.Tools[tool].Key.UpTo; !c.Valid() {
			// The candidate list comes from the same registry that generates the enum,
			// so adding a component cannot leave this message stale.
			return fmt.Errorf("spell %q: tools[%q].key.upTo is %s, want one of %s",
				m.Name, tool, c, strings.Join(c.Values(), ", "))
		}
		if d := m.Tools[tool].Diagnostics; !d.Valid() {
			return fmt.Errorf("spell %q: tools[%q].diagnostics is %s, want one of %s",
				m.Name, tool, d, strings.Join(d.Values(), ", "))
		}
	}
	return nil
}

// applyModeArgs puts mgs_getModeArgs onto the ops it names. Each arg must be in the op's
// own argv: the raw-tool guard denies only invocations carrying all of them, and one magus
// never renders would leave the op's own spelling allowed.
func applyModeArgs(m *spells.Descriptor, modes map[string][]string) error {
	for _, key := range slices.Sorted(maps.Keys(modes)) {
		args := modes[key]
		name := types.Normalize(key)
		op, ok := m.Ops[name]
		if !ok {
			return fmt.Errorf("spell %q mgs_getModeArgs names op %q, which the spell does not declare", m.Name, key)
		}
		if len(args) == 0 {
			return fmt.Errorf("spell %q mgs_getModeArgs[%q] names no args, drop the entry", m.Name, key)
		}
		for _, a := range args {
			if !slices.Contains(op.Args, a) {
				return fmt.Errorf("spell %q mgs_getModeArgs[%q]: %q is not in the op's args %q", m.Name, key, a, op.Args)
			}
		}
		op.ModeArgs = args
		m.Ops[name] = op
	}
	return nil
}

// validateCommand checks a decoded Command: its charm patches apply, its environment
// names are usable and its hints are whole. It names the spell and op only when given
// them, since the playground's dry run holds a bare Command.
func validateCommand(spellName, opName string, c spells.Command) error {
	where := ""
	if spellName != "" || opName != "" {
		where = fmt.Sprintf("spell %q op %q ", spellName, opName)
	}
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%scommand: %w", where, err)
	}
	if !c.External.Valid() {
		return fmt.Errorf("%scommand: external is %s, want one of %s", where, c.External, strings.Join(c.External.Values(), ", "))
	}
	for _, envName := range slices.Sorted(maps.Keys(c.Secrets)) {
		// The key becomes `name=value` in the child environment, so "=" in it would
		// retarget another variable and an empty key would emit "=value".
		if !validEnvName(envName) {
			return fmt.Errorf("%scommand secrets: %q is not an environment variable name (want letters, digits and underscores, not starting with a digit)", where, envName)
		}
	}
	for _, envName := range c.EnvKeys {
		if !validEnvName(envName) {
			return fmt.Errorf("%scommand envKeys: %q is not an environment variable name (want letters, digits and underscores, not starting with a digit)", where, envName)
		}
	}
	// A rule with no `contains` matches every failure, and one with no `advise` consumes
	// the match and prints nothing, which reads as "magus had no idea".
	for i, h := range c.Hints {
		if h.Contains == "" || h.Advise == "" {
			return fmt.Errorf("%scommand hints[%d]: both contains and advise are required (contains %q, advise %q)", where, i, h.Contains, h.Advise)
		}
	}
	for _, cn := range slices.Sorted(maps.Keys(c.Charms)) {
		if err := spells.ValidatePatch(c.Charms[cn].Ops); err != nil {
			return fmt.Errorf("%scharm %q: %w", where, cn, err)
		}
	}
	return nil
}

// validateService checks a service op's four commands and refuses what the supervisor
// cannot honor: it runs them directly, so secrets never reach a supervised service and
// hints never reach the classifier.
func validateService(spellName, opName string, s spells.Service) error {
	where := fmt.Sprintf("spell %q op %q", spellName, opName)
	for _, part := range []struct {
		cmd           spells.Command
		what, hintsBy string
	}{
		{s.Command, "a service op yet", "a service op (a supervised service never reaches the classifier, and a foregrounded one ends by being stopped)"},
		{s.Start, "a service start command", "a service start command (the supervisor runs it directly, so they could never fire)"},
		{s.Readiness, "a service readiness command", "a service readiness command (the supervisor polls it directly, so they could never fire)"},
		{s.Stop, "a service stop command", "a service stop command (the supervisor runs it directly, so they could never fire)"},
	} {
		if err := validateCommand(spellName, opName, part.cmd); err != nil {
			return err
		}
		if len(part.cmd.Secrets) > 0 {
			return fmt.Errorf("%s: secrets are not supported on %s", where, part.what)
		}
		if len(part.cmd.Hints) > 0 {
			return fmt.Errorf("%s: hints are not supported on %s", where, part.hintsBy)
		}
	}
	if err := s.Validate(); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	return nil
}

// validateLanguage checks mgs_getLanguage's answer. Absent means the spell adapts no
// single source language.
func validateLanguage(l *spells.Language) (spells.Language, error) {
	if l == nil {
		return spells.Language{}, nil
	}
	if l.Name == "" {
		return spells.Language{}, fmt.Errorf("language: name is required")
	}
	if syn := l.Syntax; syn != nil {
		if syn.Comments != nil && len(l.Extensions) == 0 {
			return spells.Language{}, fmt.Errorf("language: syntax.comments without extensions covers no files, declare the extensions that are this language")
		}
		if syn.Stubs != nil {
			if err := validateStubs(*syn.Stubs); err != nil {
				return spells.Language{}, fmt.Errorf("language.syntax: %w", err)
			}
		}
	}
	return *l, nil
}

// validateStubs requires every field of a stub syntax: a record with no kinds stubs
// nothing, one with no body has nothing to splice, and an unknown kind or body style
// makes the splitter skip or misplace every stub with no error anywhere.
func validateStubs(st spells.StubSyntax) error {
	if len(st.Kinds) == 0 {
		return fmt.Errorf(`stubs.kinds is empty, name the SCIP symbol kinds a stub may replace, like "Function" and "Method"`)
	}
	for _, k := range st.Kinds {
		if v, known := scip.SymbolInformation_Kind_value[k]; !known || v == int32(scip.SymbolInformation_UnspecifiedKind) {
			return fmt.Errorf(`stubs.kinds: %q is not a SCIP symbol kind, want a SymbolInformation.Kind name like "Function" or "Method"`, k)
		}
	}
	if st.BodyStyle != spells.StubBodyBrace && st.BodyStyle != spells.StubBodyIndent {
		return fmt.Errorf("stubs.bodyStyle is %q, want %q or %q", st.BodyStyle, spells.StubBodyBrace, spells.StubBodyIndent)
	}
	if st.Body == "" {
		return fmt.Errorf("stubs.body is required")
	}
	return nil
}

// validateToolDecls checks each tool's commands and drops an entry that declares
// nothing rather than keep a tool magus knows nothing about.
func validateToolDecls(tools map[string]spells.Tool) (map[string]spells.Tool, error) {
	var out map[string]spells.Tool
	for _, name := range slices.Sorted(maps.Keys(tools)) {
		t := tools[name]
		for _, part := range []struct {
			field string
			cmd   spells.Command
		}{{"probe", t.Probe}, {"ready", t.Ready}, {"observe", t.Observe}} {
			if err := validateCommand("", "", part.cmd); err != nil {
				return nil, fmt.Errorf("tools[%q].%s: %w", name, part.field, err)
			}
		}
		if t.Probe.Bin == "" && t.Key.IsZero() && t.Ready.Bin == "" && t.Supported.IsZero() &&
			t.Observe.Bin == "" && t.Diagnostics == spells.DiagnosticNone && t.Lifecycle == "" {
			continue
		}
		if out == nil {
			out = map[string]spells.Tool{}
		}
		out[name] = t
	}
	return out, nil
}

// validateSymbolIndexer checks mgs_getSymbolIndexer's answer and normalizes its op in
// place. The format is required: a declaration naming none would leave ingestion guessing
// which reader to use.
func validateSymbolIndexer(spellName string, si *spells.SymbolIndexer) error {
	if !si.Format.Valid() || si.Format == spells.SymbolFormatNone {
		return fmt.Errorf("symbol indexer: format is %s, want one of %s", si.Format, strings.Join(si.Format.Values(), ", "))
	}
	if si.Op != "" {
		if err := types.ValidateTargetName(si.Op); err != nil {
			return fmt.Errorf("symbol indexer: op %q: %w", si.Op, err)
		}
		si.Op = types.Normalize(si.Op)
	}
	if err := validateCommand(spellName, cmp.Or(si.Op, spells.DefaultSymbolIndexOp), si.Command); err != nil {
		return err
	}
	if si.Command.Bin == "" {
		return fmt.Errorf("symbol indexer: command.bin is required")
	}
	// An empty overlay would index the host twice, and one naming a variable magus sets
	// for the run would aim the indexer somewhere the merge never reads.
	for i, env := range si.Envs {
		if len(env) == 0 {
			return fmt.Errorf("symbol indexer: envs[%d] is empty", i)
		}
		for _, k := range slices.Sorted(maps.Keys(env)) {
			if k == symbols.IndexEnvVar || k == symbols.WorkspaceRootEnvVar {
				return fmt.Errorf("symbol indexer: envs[%d] sets %s, which magus sets for each run", i, k)
			}
		}
	}
	return nil
}

// validateSandbox refuses what sandbox.CheckDeclaration refuses: a grant that cannot be
// honored would quietly grant nothing, and the tool would fail with a permission error
// naming a path, not the spell.
func validateSandbox(sb spells.Sandbox) error {
	return sandbox.CheckDeclaration(sb)
}
