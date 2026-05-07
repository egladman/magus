package types

import (
	"context"
	"maps"
)

// SpellDriver is implemented by both spells (*Spell) and MCP tools.
// Metadata (markers, claims, sources) is not part of this interface.
type SpellDriver interface {
	// Name returns the stable identifier for this spell or tool.
	Name() string
	// Invoke runs the spell or tool with the given request.
	// Implementations ignore fields they don't use.
	Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error)
}

// InvokeRequest is the unified invocation payload for SpellDriver.
// Execution charms (including "rw") are carried on the context, not here.
type InvokeRequest struct {
	Target string         // build target or sub-action
	Dir    string         // project directory; empty for workspace-level MCP tools
	Params map[string]any // MCP tool parameters; ignored by *Spell
}

// InvokeResponse is the unified result payload for SpellDriver.
type InvokeResponse struct {
	Text string // human-readable output
	Data any    // structured result for MCP tools; nil for *Spell
}

// Spell teaches magus how to build/test/lint/format projects of a given language.
// Spells are interned singletons registered at init() time; all fields are unexported.
type Spell struct {
	name                string
	sources             []string
	claims              []string
	outputs             []string
	targets             []string
	foreignProcess      bool
	targetSources       map[string][]string
	targetCharms        map[string][]string // target name → charm names it declares (for discovery)
	targetDocs          map[string]string   // target name → handler doc comment (for describe/doctor)
	docRequiredTargets  []string            // function-handler targets doctor requires a doc comment on (local Buzz spells)
	declarationFiles    []string
	declarationDirGlobs []string

	invoke       func(ctx context.Context, req InvokeRequest) (any, error)
	renderCmd    func(target string, charms []string) (cmd string, args []string, ok bool)
	dependsOn    func(dir string) []string
	versionProbe func(ctx context.Context, dir string) (string, error)
}

// Name implements SpellDriver.
func (s *Spell) Name() string { return s.name }

// Invoke implements SpellDriver. A nil invoke func is a no-op. Fork-target
// spells ignore req.Params and return no Data; function-op spells (Buzz ops
// declared with "fn") receive req.Params and return their result as Data, the
// channel the remote cache backend and other Go callers read. Charms (including
// the built-in "rw") ride on ctx; a target that cares reads them via HasCharm.
func (s *Spell) Invoke(ctx context.Context, req InvokeRequest) (InvokeResponse, error) {
	if s.invoke == nil {
		return InvokeResponse{}, nil
	}
	data, err := s.invoke(ctx, req)
	return InvokeResponse{Data: data}, err
}

var _ SpellDriver = (*Spell)(nil)

func (s *Spell) Sources() []string                  { return s.sources }
func (s *Spell) Claims() []string                   { return s.claims }
func (s *Spell) Outputs() []string                  { return s.outputs }
func (s *Spell) Targets() []string                  { return s.targets }
func (s *Spell) ForeignProcess() bool               { return s.foreignProcess }
func (s *Spell) TargetSources() map[string][]string { return s.targetSources }
func (s *Spell) Charms(target string) []string      { return s.targetCharms[target] }

// RenderCommand returns the command a fork target would run with the given
// charms applied — cmd plus the charm-patched argv — for static preview
// (`magus describe`). ok is false when the spell has no renderer, the target is
// a function-op (its argv is computed in-VM, not statically knowable), or it is a
// no-op marker. It executes nothing.
func (s *Spell) RenderCommand(target string, charms []string) (cmd string, args []string, ok bool) {
	if s.renderCmd == nil {
		return "", nil, false
	}
	return s.renderCmd(target, charms)
}
func (s *Spell) DeclarationFiles() []string    { return s.declarationFiles }
func (s *Spell) DeclarationDirGlobs() []string { return s.declarationDirGlobs }

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

// HasVersionProbe reports whether a toolchain-version probe is set.
func (s *Spell) HasVersionProbe() bool { return s.versionProbe != nil }

// ProbeVersion returns the spell's toolchain version string for dir.
// Returns "" when no probe is set.
func (s *Spell) ProbeVersion(ctx context.Context, dir string) (string, error) {
	if s.versionProbe == nil {
		return "", nil
	}
	return s.versionProbe(ctx, dir)
}

// SpellOption configures NewSpell.
type SpellOption func(*Spell)

func WithSources(sources ...string) SpellOption {
	return func(s *Spell) { s.sources = append(s.sources, sources...) }
}

func WithClaims(claims ...string) SpellOption {
	return func(s *Spell) { s.claims = append(s.claims, claims...) }
}

func WithSpellOutputs(outputs ...string) SpellOption {
	return func(s *Spell) { s.outputs = append(s.outputs, outputs...) }
}

func WithTargets(targets ...string) SpellOption {
	return func(s *Spell) { s.targets = append(s.targets, targets...) }
}

// WithForeignProcess marks the spell as delegating to a foreign process. Informational only.
func WithForeignProcess() SpellOption {
	return func(s *Spell) { s.foreignProcess = true }
}

// WithInvoker sets the function that runs a target; a spell with none is a no-op.
// The invoker receives the full request (so function-ops can read Params) and
// returns structured Data (nil for fork targets), surfaced via InvokeResponse.
func WithInvoker(fn func(ctx context.Context, req InvokeRequest) (any, error)) SpellOption {
	return func(s *Spell) { s.invoke = fn }
}

// WithCommandRenderer sets the fork-command renderer used by `magus describe` to
// preview the charm-applied argv without executing. See Spell.RenderCommand.
func WithCommandRenderer(fn func(target string, charms []string) (cmd string, args []string, ok bool)) SpellOption {
	return func(s *Spell) { s.renderCmd = fn }
}

func WithSpellDependsOn(fn func(dir string) []string) SpellOption {
	return func(s *Spell) { s.dependsOn = fn }
}

// WithVersionProbe sets the toolchain version probe; the result mixes into the cache key.
func WithVersionProbe(fn func(ctx context.Context, dir string) (string, error)) SpellOption {
	return func(s *Spell) { s.versionProbe = fn }
}

func WithDeclarationFiles(files ...string) SpellOption {
	return func(s *Spell) { s.declarationFiles = append(s.declarationFiles, files...) }
}

func WithDeclarationDirGlobs(globs ...string) SpellOption {
	return func(s *Spell) { s.declarationDirGlobs = append(s.declarationDirGlobs, globs...) }
}

// WithTargetSources attaches workspace-root globs for the cache key per target.
// The map is cloned to prevent caller mutation.
func WithTargetSources(sources map[string][]string) SpellOption {
	return func(s *Spell) { s.targetSources = maps.Clone(sources) }
}

// WithTargetCharms records the charm names each target declares, for discovery
// (e.g. `magus describe`). The map is cloned to prevent caller mutation.
func WithTargetCharms(charms map[string][]string) SpellOption {
	return func(s *Spell) { s.targetCharms = maps.Clone(charms) }
}

// WithTargetDocs records each target handler's doc comment, surfaced by
// `magus describe`. The map is cloned to prevent caller mutation.
func WithTargetDocs(docs map[string]string) SpellOption {
	return func(s *Spell) { s.targetDocs = maps.Clone(docs) }
}

// WithDocRequiredTargets records the function-handler targets `magus doctor`
// requires a doc comment on (workspace-local Buzz spells).
func WithDocRequiredTargets(targets ...string) SpellOption {
	return func(s *Spell) { s.docRequiredTargets = append(s.docRequiredTargets, targets...) }
}

// NewSpell constructs a Spell with the given name and options.
func NewSpell(name string, opts ...SpellOption) *Spell {
	s := &Spell{name: name}
	for _, o := range opts {
		o(s)
	}
	return s
}
