package spells

import (
	"context"
	"maps"
	"sort"
)

// Driver is implemented by both spells (*Spell) and MCP tools.
// Metadata (markers, claims, sources) is not part of this interface.
type Driver interface {
	// Name returns the stable identifier for this spell or tool.
	Name() string
	// Invoke runs the spell or tool with the given request.
	// Implementations ignore fields they don't use.
	Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error)
}

// InvokeRequest is the unified invocation payload for Driver.
// Execution charms (including "rw") are carried on the context, not here.
type InvokeRequest struct {
	Target string         // build target or sub-action
	Dir    string         // project directory; empty for workspace-level MCP tools
	Params map[string]any // MCP tool parameters; ignored by *Spell
}

// InvokeResponse is the unified result payload for Driver.
type InvokeResponse struct {
	Text string // human-readable output
	Data any    // structured result for MCP tools; nil for *Spell
}

// Spell teaches magus how to build/test/lint/format projects of a given language.
// Spells are interned singletons registered at init() time; all fields are unexported.
type Spell struct {
	name                string
	sources             []string
	ignoreDirs          []string
	outputs             []string
	targets             []string
	language            string          // canonical source language the spell adapts; "" when it adapts none
	serviceTargets      map[string]bool // target names backed by a service op (long-running; uncacheable)
	opaque              bool
	internal            bool
	targetSources       map[string][]string
	targetCharms        map[string][]string // target name → charm names it declares (for discovery)
	targetDocs          map[string]string   // target name → handler doc comment (for describe/doctor)
	docRequiredTargets  []string            // function-handler targets doctor requires a doc comment on (local Buzz spells)
	declarationFiles    []string
	declarationDirGlobs []string
	manifests           []Manifest

	invoke      func(ctx context.Context, req InvokeRequest) (any, error)
	renderCmd   func(target string, charms []string) (cmd string, args []string, ok bool, err error)
	explainCmd  func(target string, charms []string) (steps []CharmTraceStep, ok bool, err error)
	conflictCmd func(target string, charms []string) (conflicts []CharmConflict, ok bool, err error)
	serviceView func(target string) (view *ServiceView, ok bool)
	dependsOn   func(dir string) []string
	// tools is every binary this spell drives, keyed by bin; see Descriptor.Tools.
	tools map[string]Tool
	// probe runs one tool's version argv in a project dir. Injected so the engine
	// owns process execution and this package stays free of it.
	probe func(ctx context.Context, cmd Command, dir string) (string, error)
}

// Name implements Driver.
func (s *Spell) Name() string { return s.name }

// Invoke implements Driver. A nil invoke func is a no-op. Fork-target
// spells ignore req.Params and return no Data; function-op spells (Buzz ops
// declared with "fn") receive req.Params and return their result as Data, the
// channel the remote cache provider and other Go callers read. Charms (including
// the built-in "rw") ride on ctx; a target that cares reads them via HasCharm.
func (s *Spell) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if s.invoke == nil {
		return InvokeResponse{}, nil
	}
	data, err := s.invoke(ctx, req)
	return InvokeResponse{Data: data}, err
}

var _ Driver = (*Spell)(nil)

func (s *Spell) Sources() []string { return s.sources }
func (s *Spell) Outputs() []string { return s.outputs }
func (s *Spell) Targets() []string { return s.targets }

// IgnoreDirs returns the non-source directory names this spell's ecosystem
// generates (vendor, node_modules, target, ...), declared by mgs_listIgnoreDirs.
// The input-hashing walk prunes them for a project this spell resolves, so the
// engine holds no language-specific directory names. Dot-directories are skipped
// structurally and never appear here.
func (s *Spell) IgnoreDirs() []string { return s.ignoreDirs }

// Language returns the canonical source language the spell adapts (e.g. "go",
// "typescript"), or "" when it adapts no single language. It tags the spell node so a
// `language:` query groups the adapter with that language's files and symbols.
func (s *Spell) Language() string { return s.language }

// IsServiceTarget reports whether target name is backed by a service op (a
// long-running process). The runner forces such targets uncacheable.
func (s *Spell) IsServiceTarget(name string) bool { return s.serviceTargets[name] }
func (s *Spell) Opaque() bool                     { return s.opaque }

// Internal reports whether this registration is dispatch plumbing rather than a
// spell a user binds. See [WithInternal].
func (s *Spell) Internal() bool                     { return s.internal }
func (s *Spell) TargetSources() map[string][]string { return s.targetSources }
func (s *Spell) Charms(target string) []string      { return s.targetCharms[target] }

// RenderCommand returns the command a fork target would run with the given
// charms applied — cmd plus the charm-patched argv — for static preview
// (`magus describe`). ok is false when the spell has no renderer, the target is
// a function-op (its argv is computed in-VM, not statically knowable), or it is a
// no-op marker. A non-nil err means an active charm's patch is valid in shape but
// does not apply to this target's argv (an out-of-range index, a failing `test`
// op): the charm is dead on this target and the caller surfaces it instead of
// dropping the command line silently. It executes nothing.
func (s *Spell) RenderCommand(target string, charms []string) (cmd string, args []string, ok bool, err error) {
	if s.renderCmd == nil {
		return "", nil, false, nil
	}
	return s.renderCmd(target, charms)
}

// ExplainCommand returns the charm-application trace for a static preview: step 0
// is the base command (no charms), and each later step is the command after one
// more active charm's patch, in the deterministic order magus applies them. ok is
// false on the same conditions as RenderCommand (no renderer, function-op, no-op
// marker). A non-nil err means an active charm's patch does not apply to this
// op's argv (see RenderCommand). It executes nothing.
func (s *Spell) ExplainCommand(target string, charms []string) (steps []CharmTraceStep, ok bool, err error) {
	if s.explainCmd == nil {
		return nil, false, nil
	}
	return s.explainCmd(target, charms)
}

// ConflictingCharms returns the active charms whose edit is overridden by another
// active charm on the target's command (both edit the same argument; the loser has
// no effect). ok is false on the same conditions as RenderCommand. It executes
// nothing; `magus describe target ...:a,b` surfaces the result before a run.
func (s *Spell) ConflictingCharms(target string, charms []string) (conflicts []CharmConflict, ok bool, err error) {
	if s.conflictCmd == nil {
		return nil, false, nil
	}
	return s.conflictCmd(target, charms)
}

// ServiceView returns the static, pre-run description of a service target (its
// readiness probe, stop command, idle override, distinct reason, and fingerprint).
// ok is false when the target is not a service or the spell carries no service data.
// It executes nothing.
func (s *Spell) ServiceView(target string) (view *ServiceView, ok bool) {
	if s.serviceView == nil {
		return nil, false
	}
	return s.serviceView(target)
}

func (s *Spell) DeclarationFiles() []string    { return s.declarationFiles }
func (s *Spell) DeclarationDirGlobs() []string { return s.declarationDirGlobs }

// Manifests returns the ordered candidate manifests this spell's ecosystem declares
// (go.mod, package.json, Cargo.toml, pyproject.toml), each with the lockfiles that
// ecosystem might resolve it into, as declared by mgs_listManifests. Ordered because
// a language can have genuine alternatives (Python's pyproject.toml / setup.py /
// setup.cfg): the first candidate present in a project directory is that project's
// manifest, not all of them at once.
//
// A manifest answers two questions that happen to share a file. It carries the
// project's own VERSION, which is what this was originally for, and it declares the
// project's DEPENDENCIES, which is what its lock candidates lead to. Go is the case
// that makes them look like one question - go.mod holds both - and npm is the case
// that separates them, since package.json pins neither its own resolved dependency
// versions nor, in a workspace, the lockfile's location.
//
// Do not confuse this with three adjacent but distinct facts: Sources
// (mgs_listRequiredGlobs) answers "what feeds my targets" and often already lists
// a manifest AND its lockfiles as cache/affected inputs (package.json and
// pnpm-lock.yaml among **/*.ts and friends) - that is a different question from
// "what declares this project". DeclarationFiles answers "a directory holding this
// file IS a project of mine" (discovery), used today only by the magusfile spell.
// VersionProbe is the TOOLCHAIN's version (`go version`), which feeds cache keys,
// not the project's own version.
func (s *Spell) Manifests() []Manifest { return s.manifests }

// TargetDoc returns the documentation comment of the named target's handler, or
// "" when undocumented or unknown.
func (s *Spell) TargetDoc(target string) string { return s.targetDocs[target] }

// DocRequiredTargets returns the function-handler targets `magus doctor` requires
// a doc comment on. Non-empty only for workspace-local Buzz spells (record-style
// {cmd,args} ops and Teal spells, whose comments aren't captured, are excluded).
func (s *Spell) DocRequiredTargets() []string { return s.docRequiredTargets }

// DependsOn returns in-workspace dependency paths for the project at dir.
func (s *Spell) DependsOn(dir string) []string {
	if s.dependsOn == nil {
		return nil
	}
	return s.dependsOn(dir)
}

// ToolNames returns the binaries this spell drives, sorted, so a caller iterates them
// deterministically and the cache key they produce is stable.
func (s *Spell) ToolNames() []string {
	if len(s.tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(s.tools))
	for name := range s.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Tool returns what this spell declares about one binary, and whether it declares it.
func (s *Spell) Tool(name string) (Tool, bool) {
	t, ok := s.tools[name]
	return t, ok
}

// HasVersionProbe reports whether ANY tool can report a version, so a caller can skip
// the probe pass entirely for a spell that declares none.
func (s *Spell) HasVersionProbe() bool {
	for _, t := range s.tools {
		if t.HasProbe() {
			return true
		}
	}
	return false
}

// ProbeVersion runs one tool's version argv in dir and returns its raw output. It
// returns "" for a tool that declares no argv - including one whose version is a
// constant, where the caller reads Tool.Key.Const instead of spawning anything.
func (s *Spell) ProbeVersion(ctx context.Context, tool, dir string) (string, error) {
	t, ok := s.tools[tool]
	if !ok || t.Probe.Bin == "" || s.probe == nil {
		return "", nil
	}
	return s.probe(ctx, t.Probe, dir)
}

// Option configures NewSpell.
type Option func(*Spell)

func WithSources(sources ...string) Option {
	return func(s *Spell) { s.sources = append(s.sources, sources...) }
}

func WithIgnoreDirs(dirs ...string) Option {
	return func(s *Spell) { s.ignoreDirs = append(s.ignoreDirs, dirs...) }
}

func WithOutputs(outputs ...string) Option {
	return func(s *Spell) { s.outputs = append(s.outputs, outputs...) }
}

func WithTargets(targets ...string) Option {
	return func(s *Spell) { s.targets = append(s.targets, targets...) }
}

// WithServiceTargets records which of the spell's targets are backed by a service
// op (long-running). The runner forces such targets uncacheable so a re-run
// restarts the process instead of replaying a completed-target result.
func WithServiceTargets(names ...string) Option {
	return func(s *Spell) {
		if len(names) == 0 {
			return
		}
		if s.serviceTargets == nil {
			s.serviceTargets = make(map[string]bool, len(names))
		}
		for _, n := range names {
			s.serviceTargets[n] = true
		}
	}
}

// WithLanguage sets the canonical source language the spell adapts, used to tag the
// spell node so a `language:` query reaches the adapter alongside that language's code.
func WithLanguage(language string) Option {
	return func(s *Spell) { s.language = language }
}

// WithOpaque marks the spell as opaque: it delegates to a foreign process that
// manages its own dependency graph, so magus treats the project as a black box
// rather than tracking per-file inputs. Informational only.
func WithOpaque() Option {
	return func(s *Spell) { s.opaque = true }
}

// WithInternal marks a registration as dispatch plumbing rather than a spell a
// user binds, keeping it out of every surface that enumerates spells.
//
// It exists for exactly one registration: `magusfile`. A spell is defined as a
// library of tool-native ops for ONE TOOLCHAIN (go-build, cargo-clippy, eslint) -
// see docs/concepts/spells.md, whose built-in table has never listed magusfile.
// The magusfile registration adapts no toolchain and contributes no ops; it reuses
// the driver interface so a magusfile's own targets dispatch through the same
// path. Registering it plainly made the code contradict the docs: `magus describe
// spells` listed a spell the reference says does not exist, and because every
// project is DISCOVERED by having a magusfile, `magus ls` stamped
// "spell: magusfile" on all of them - a field that told a reader nothing, since it
// was true by construction.
func WithInternal() Option {
	return func(s *Spell) { s.internal = true }
}

// WithInvoker sets the function that runs a target; a spell with none is a no-op.
// The invoker receives the full request (so function-ops can read Params) and
// returns structured Data (nil for fork targets), surfaced via InvokeResponse.
func WithInvoker(fn func(ctx context.Context, req InvokeRequest) (any, error)) Option {
	return func(s *Spell) { s.invoke = fn }
}

// WithCommandRenderer sets the fork-command renderer used by `magus describe` to
// preview the charm-applied argv without executing. See Spell.RenderCommand.
func WithCommandRenderer(fn func(target string, charms []string) (cmd string, args []string, ok bool, err error)) Option {
	return func(s *Spell) { s.renderCmd = fn }
}

// WithCommandExplainer sets the charm-trace renderer used by `magus describe
// target --explain`. See Spell.ExplainCommand.
func WithCommandExplainer(fn func(target string, charms []string) (steps []CharmTraceStep, ok bool, err error)) Option {
	return func(s *Spell) { s.explainCmd = fn }
}

// WithCommandConflicts sets the charm-conflict detector used by `magus describe` to
// report active charms whose edit another active charm overrides.
func WithCommandConflicts(fn func(target string, charms []string) (conflicts []CharmConflict, ok bool, err error)) Option {
	return func(s *Spell) { s.conflictCmd = fn }
}

// WithServiceView sets the static service-facts accessor used by `magus describe
// target` to describe a service op before it runs.
func WithServiceView(fn func(target string) (view *ServiceView, ok bool)) Option {
	return func(s *Spell) { s.serviceView = fn }
}

func WithDependsOn(fn func(dir string) []string) Option {
	return func(s *Spell) { s.dependsOn = fn }
}

func WithDeclarationFiles(files ...string) Option {
	return func(s *Spell) { s.declarationFiles = append(s.declarationFiles, files...) }
}

func WithDeclarationDirGlobs(globs ...string) Option {
	return func(s *Spell) { s.declarationDirGlobs = append(s.declarationDirGlobs, globs...) }
}

// WithManifests sets the ordered candidate manifests this spell's ecosystem
// declares. See Spell.Manifests for the ordering contract and how this differs from
// WithSources, WithDeclarationFiles, and WithVersionProbe.
func WithManifests(manifests ...Manifest) Option {
	return func(s *Spell) { s.manifests = append(s.manifests, manifests...) }
}

// WithTargetSources attaches workspace-root globs for the cache key per target.
// The map is cloned to prevent caller mutation.
func WithTargetSources(sources map[string][]string) Option {
	return func(s *Spell) { s.targetSources = maps.Clone(sources) }
}

// WithTargetCharms records the charm names each target declares, for discovery
// (e.g. `magus describe`). The map is cloned to prevent caller mutation.
func WithTargetCharms(charms map[string][]string) Option {
	return func(s *Spell) { s.targetCharms = maps.Clone(charms) }
}

// WithTargetDocs records each target handler's doc comment, surfaced by
// `magus describe`. The map is cloned to prevent caller mutation.
func WithTargetDocs(docs map[string]string) Option {
	return func(s *Spell) { s.targetDocs = maps.Clone(docs) }
}

// WithDocRequiredTargets records the function-handler targets `magus doctor`
// requires a doc comment on (workspace-local Buzz spells).
func WithDocRequiredTargets(targets ...string) Option {
	return func(s *Spell) { s.docRequiredTargets = append(s.docRequiredTargets, targets...) }
}

// NewSpell constructs a Spell with the given name and options.
func NewSpell(name string, opts ...Option) *Spell {
	s := &Spell{name: name}
	for _, o := range opts {
		o(s)
	}
	return s
}

// ModulePrefix is the import namespace every spell is reachable under.
const ModulePrefix = "magus/spell/"

// ModulePath is the literal a magusfile writes to bind this spell's handle:
// ModulePath("go") is "magus/spell/go", for `import "magus/spell/go"`.
//
// Reported as a STRING on the spell descriptor record rather than resolved to a handle.
// A handle can only come from a literal import, because internal/describe reads
// spell imports statically to build the target graph - so a dynamically resolved
// spell would drop the target-uses-spell edge and under-report the graph without
// failing. Handing back the path keeps discovery dynamic and the import static:
// you look the spell up, then write the import yourself.
func ModulePath(name string) string { return ModulePrefix + name }

// WithTools declares every binary this spell drives, keyed by bin name.
func WithTools(tools map[string]Tool) Option {
	return func(s *Spell) { s.tools = tools }
}

// WithVersionProber injects how a tool's version argv is run. The engine owns process
// execution, so this package never spawns anything itself.
func WithVersionProber(fn func(ctx context.Context, cmd Command, dir string) (string, error)) Option {
	return func(s *Spell) { s.probe = fn }
}
