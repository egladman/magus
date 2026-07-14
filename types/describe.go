package types

// Describe-output types and the concept definitions printed by "magus describe".

// SpellDefinition is the human-readable description of a spell shown by "magus describe spells".
const SpellDefinition = "A spell is a language/runtime adapter that " +
	"teaches magus how to build, test, lint, and format projects of a given type. " +
	"Spells are registered at startup and bound to projects by importing the spell " +
	"and listing it in the spells of magus.project in the magusfile."

// SpellEntry is the structured view of a single spell.
type SpellEntry struct {
	Name    string   `json:"name"              yaml:"name"`
	Sources []string `json:"sources,omitempty" yaml:"sources,omitempty"`
	Outputs []string `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Claims  []string `json:"claims,omitempty"  yaml:"claims,omitempty"`
	Targets []string `json:"targets,omitempty" yaml:"targets,omitempty"`
	Opaque  bool     `json:"opaque,omitempty" yaml:"opaque,omitempty"`
	// Language is the canonical source language the spell adapts (e.g. "go",
	// "typescript"), empty for a spell tied to no single language. It tags the spell
	// node so `magus query language:go` reaches the adapter alongside that language's
	// files and symbols.
	Language string `json:"language,omitempty" yaml:"language,omitempty"`
	// TargetDocs maps a target name to its handler's doc comment, where one
	// exists. Populated only for workspace-local Buzz spells (built-in docs are
	// not serialized in bytecode).
	TargetDocs map[string]string `json:"target_docs,omitempty" yaml:"target_docs,omitempty"`
}

// SpellsOutput is the top-level result for "describe spells".
type SpellsOutput struct {
	Definition string       `json:"definition" yaml:"definition"`
	Count      int          `json:"count"      yaml:"count"`
	Spells     []SpellEntry `json:"spells"     yaml:"spells"`
}

// CharmDefinition is the human-readable description of a charm shown by "magus describe charms".
const CharmDefinition = "A charm is a named, shared execution modifier applied as an " +
	"RFC 6902 JSON Patch over a target's argv: it changes how a target runs (rw, gha), " +
	"never which target or project runs. See docs/charms.md."

// CharmsOutput is the payload of "magus describe charm[s]": the inverse charm
// index, every charm name known in the workspace paired with the declarations that
// give it meaning. It is the transpose of EvaluatedTargetsOutput's per-target charm
// list (one charm, every target that declares it).
type CharmsOutput struct {
	Definition string       `json:"definition" yaml:"definition"`
	Count      int          `json:"count"      yaml:"count"`
	Charms     []CharmEntry `json:"charms"     yaml:"charms"`
}

// CharmEntry is one charm in the inverse index: its name, whether it is a reserved
// built-in or a workspace default, its built-in doc (empty for a spell-defined
// charm), and every target that declares a patch for it.
type CharmEntry struct {
	Name         string             `json:"name"                   yaml:"name"`
	Builtin      bool               `json:"builtin,omitempty"      yaml:"builtin,omitempty"`
	Default      bool               `json:"default,omitempty"      yaml:"default,omitempty"`
	Doc          string             `json:"doc,omitempty"          yaml:"doc,omitempty"`
	Declarations []CharmDeclaration `json:"declarations,omitempty" yaml:"declarations,omitempty"`
}

// CharmDeclaration is one target's declaration of a charm: the spell that owns the
// command and the before/after argv the charm's patch produces for that target.
// Before == After marks a declaration whose patch changes nothing for this target.
type CharmDeclaration struct {
	Project string   `json:"project"          yaml:"project"`
	Target  string   `json:"target"           yaml:"target"`
	Spell   string   `json:"spell"            yaml:"spell"`
	Before  []string `json:"before,omitempty" yaml:"before,omitempty"`
	After   []string `json:"after,omitempty"  yaml:"after,omitempty"`
}

// TargetDefinition is the human-readable description of a target shown by "magus describe targets".
const TargetDefinition = "A target is a named operation (e.g. build, test, lint) declared as an " +
	"exported function in a project's magusfile, which may compose a spell's " +
	"tool-native operations. 'ci' is the conventional anchor that the affected set " +
	"keys off — magus runs it read-only but does not hardcode its steps; the magusfile " +
	"composes them with magus.needs."

// TargetEntry describes a single target available in the workspace.
type TargetEntry struct {
	Name     string   `json:"name"               yaml:"name"`
	Kind     string   `json:"kind"               yaml:"kind"`
	Spells   []string `json:"spells,omitempty"   yaml:"spells,omitempty"`
	Projects []string `json:"projects,omitempty" yaml:"projects,omitempty"`
}

// TargetsOutput is the top-level result for "describe targets".
type TargetsOutput struct {
	Definition string        `json:"definition" yaml:"definition"`
	Count      int           `json:"count"      yaml:"count"`
	Targets    []TargetEntry `json:"targets"    yaml:"targets"`
}

// TargetGraphDefinition describes "magus describe graph".
const TargetGraphDefinition = "The target dependency graph is the magus.needs " +
	"DAG of a project's magusfile: each node is a target (an exported function), each " +
	"edge a dependency it composes. It is extracted statically from the magusfile " +
	"source, so it shows every edge — including both arms of a runtime branch — and " +
	"flags any dependency cycle (which the run path rejects during dispatch)."

// TargetGraphNode is one target in the graph: its run name, doc comment, the
// targets it depends on, and the charm names its body branches on. The static
// extractor (internal/describe) populates it directly and `magus describe graph`
// serializes it. Wire keys are snake_case field names (dependencies, not the
// abbreviated deps), matching the project-level depends_on and the rest of this file.
type TargetGraphNode struct {
	Name         string   `json:"name"                   yaml:"name"`
	Doc          string   `json:"doc,omitempty"          yaml:"doc,omitempty"`
	Dependencies []string `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
	Charms       []string `json:"charms,omitempty"       yaml:"charms,omitempty"`
	// Spells are the spell ops the target's body invokes, captured statically from
	// the bracket (`go["go-test"]()`) and dotted (`md.markdownlint()`) call forms,
	// grouped by spell in first-appearance order. It shows which toolchain a
	// composite target drives - the part `deps` (sibling targets) omits.
	Spells []TargetSpellUse `json:"spells,omitempty" yaml:"spells,omitempty"`
	// CrossDependencies are dependencies on specific targets in *other* projects,
	// declared via a project import (<alias>.<target>). Unlike Dependencies (same-project
	// target names), each carries the other project's path, so the graph can draw a
	// target -> target edge across project boundaries instead of a coarse project -> project one.
	CrossDependencies []CrossTargetRef `json:"cross_dependencies,omitempty" yaml:"cross_dependencies,omitempty"`
}

// CrossTargetRef names one target in another project: a target-level cross-project
// dependency. Project is workspace-relative (resolved from the dot-/repo-relative
// path written in the magusfile); Target is the kebab-normalized target name.
type CrossTargetRef struct {
	Project string `json:"project" yaml:"project"`
	Target  string `json:"target"  yaml:"target"`
}

// TargetSpellUse is one spell a target invokes and the ops it calls on it.
type TargetSpellUse struct {
	Spell string   `json:"spell"         yaml:"spell"`
	Ops   []string `json:"ops,omitempty" yaml:"ops,omitempty"`
}

// TargetGraphProject is one project's target graph, plus a detected cycle (a path
// of node names that begins and ends at the same node) when the DAG is not acyclic.
type TargetGraphProject struct {
	Path   string            `json:"path"             yaml:"path"`
	Engine string            `json:"engine,omitempty" yaml:"engine,omitempty"`
	Nodes  []TargetGraphNode `json:"nodes,omitempty"  yaml:"nodes,omitempty"`
	Cycle  []string          `json:"cycle,omitempty"  yaml:"cycle,omitempty"`
	// DependsOn are the workspace-relative paths of the projects this project
	// depends on (its project-level deps, declared in magus.project).
	// They draw the project -> project arrows in the combined workspace graph;
	// intra-project target edges live on each node's Dependencies.
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	// RelPath is Path expressed relative to the VCS (repo) root, used only for an
	// unambiguous MAGUS.md heading when a project sits at the workspace root (Path
	// is "."). Display-only and repo-derived, so it is not serialized; the run path
	// still addresses the project by Path. Empty outside a repo.
	RelPath string `json:"-" yaml:"-"`
}

// Label is the human display name for this project, the single source every render site
// uses so none prints a bare ".": the pre-collapsed RelPath (which reads as the repo
// name for the workspace root), falling back to the shared never-'.' rule on Path.
func (p TargetGraphProject) Label() string {
	if p.RelPath != "" && p.RelPath != "." {
		return p.RelPath
	}
	return ProjectLabel(p.Path, "")
}

// TargetGraphOutput is the top-level result for "describe graph".
type TargetGraphOutput struct {
	Definition string               `json:"definition" yaml:"definition"`
	Projects   []TargetGraphProject `json:"projects"   yaml:"projects"`
}

// ProjectDefinition is the human-readable description of a project shown by "magus describe projects".
const ProjectDefinition = "A project is a directory the workspace recognized as a " +
	"unit of work, bound to one or more spells. Projects are " +
	"discovered by the presence of a magusfile (magusfile.buzz, or a magusfiles/ subdirectory) " +
	"and are the basic unit of caching, scheduling, and dependency tracking."

// ProjectEntry is the structured view of a single project.
type ProjectEntry struct {
	Path      string   `json:"path"                yaml:"path"`
	Dir       string   `json:"dir"                 yaml:"dir"`
	Spell     string   `json:"spell,omitempty"     yaml:"spell,omitempty"`
	Spells    []string `json:"spells,omitempty"    yaml:"spells,omitempty"`
	Sources   []string `json:"sources,omitempty"    yaml:"sources,omitempty"`
	Outputs   []string `json:"outputs,omitempty"    yaml:"outputs,omitempty"`
	DependsOn []string `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Exclusive bool     `json:"exclusive,omitempty"  yaml:"exclusive,omitempty"`
}

// ProjectsOutput is the top-level result for "describe projects".
type ProjectsOutput struct {
	Definition string         `json:"definition" yaml:"definition"`
	Workspace  string         `json:"workspace"  yaml:"workspace"`
	Count      int            `json:"count"      yaml:"count"`
	Projects   []ProjectEntry `json:"projects"   yaml:"projects"`
}

// ModuleDefinition is the human-readable description shown by "magus describe modules".
const ModuleDefinition = "A module is a magus standard-library namespace a magusfile imports for " +
	"host capabilities — filesystem, exec, vcs, crypto, http, and more. Import " +
	"each under its bare name (import \"fs\", then fs.glob(...)); magus layers these " +
	"methods onto Buzz's own stdlib. The magus forms are sandbox-aware; some methods " +
	"also exist in Buzz's own stdlib."

// ModuleMethodEntry is one method of a module, with its Buzz call form.
type ModuleMethodEntry struct {
	Name       string `json:"name"                  yaml:"name"`
	Doc        string `json:"doc,omitempty"         yaml:"doc,omitempty"`
	Buzz       string `json:"buzz"                  yaml:"buzz"`
	BuzzStdlib string `json:"buzz_stdlib,omitempty" yaml:"buzz_stdlib,omitempty"`
}

// ToMap is the Buzz boundary map for a method entry (magus.module's methods).
func (m ModuleMethodEntry) ToMap() map[string]any {
	return map[string]any{"name": m.Name, "doc": m.Doc, "buzz": m.Buzz, "buzzStdlib": m.BuzzStdlib}
}

// ModuleFieldEntry is one static, table-level value on a module (e.g. vcs.name).
type ModuleFieldEntry struct {
	Name string `json:"name"          yaml:"name"`
	Type string `json:"type"          yaml:"type"`
	Doc  string `json:"doc,omitempty" yaml:"doc,omitempty"`
}

// ToMap is the Buzz boundary map for a field entry (magus.module's fields).
func (f ModuleFieldEntry) ToMap() map[string]any {
	return map[string]any{"name": f.Name, "type": f.Type, "doc": f.Doc}
}

// ModuleEntry is a module's summary; Fields/Methods are populated only for the detail view.
type ModuleEntry struct {
	Name    string              `json:"name"              yaml:"name"`
	Doc     string              `json:"doc,omitempty"     yaml:"doc,omitempty"`
	Fields  []ModuleFieldEntry  `json:"fields,omitempty"  yaml:"fields,omitempty"`
	Methods []ModuleMethodEntry `json:"methods,omitempty" yaml:"methods,omitempty"`
}

// ToMap is the Buzz boundary map magus.modules / magus.module return:
// {name, doc, fields, methods}. fields/methods are always present (empty in the
// summary view). The generated/hand-written bindings marshal it via host.Mapper.
func (e ModuleEntry) ToMap() map[string]any {
	fields := make([]any, len(e.Fields))
	for i, f := range e.Fields {
		fields[i] = f.ToMap()
	}
	methods := make([]any, len(e.Methods))
	for i, m := range e.Methods {
		methods[i] = m.ToMap()
	}
	return map[string]any{"name": e.Name, "doc": e.Doc, "fields": fields, "methods": methods}
}

// ModulesOutput is the top-level result for "describe modules" / "describe module <name>".
type ModulesOutput struct {
	Definition string        `json:"definition" yaml:"definition"`
	Count      int           `json:"count"      yaml:"count"`
	Modules    []ModuleEntry `json:"modules"    yaml:"modules"`
}

// EvaluatedTargetDefinition is the human-readable description of an evaluated target shown by "magus describe".
const EvaluatedTargetDefinition = "An evaluated target shows the fully-resolved " +
	"dispatch plan for a specific path:target pair: the workspace-rooted source and " +
	"output globs that feed the cache key, the spells that will fire (with " +
	"target-specific sources and effective claims after weight/add/remove resolution), " +
	"and any behavioural policy (CheckClean, TrackVolatile, Exclusive)."

// EvaluatedSpellEntry is one spell's contribution to an evaluated target.
type EvaluatedSpellEntry struct {
	Name            string   `json:"name"                        yaml:"name"`
	TargetSources   []string `json:"target_sources,omitempty"    yaml:"target_sources,omitempty"`
	EffectiveClaims []string `json:"effective_claims,omitempty"  yaml:"effective_claims,omitempty"`
	ClaimWeight     int      `json:"claim_weight,omitempty"      yaml:"claim_weight,omitempty"`
	// Command is the fork command this spell's op would run for the target, with
	// the requested charms applied (cmd as element 0). Empty for function-op or
	// no-op targets, whose argv isn't statically knowable. Preview only: `magus
	// describe` renders it; nothing is executed.
	Command []string `json:"command,omitempty"           yaml:"command,omitempty"`
	// CharmTrace is the step-by-step application of the active charms over this
	// spell's base argv: element 0 is the base command (no charms), and each
	// subsequent step is the command after one more charm's patch, in the
	// deterministic sorted-name order magus applies them. Populated only when
	// charms are active and change the command; the RFC 6902 patch made legible by
	// `magus describe target ...:charm --explain`.
	CharmTrace []CharmTraceStep `json:"charm_trace,omitempty"       yaml:"charm_trace,omitempty"`
	// Conflicts lists the active charms whose edit is overridden by another active
	// charm on this command (both edit the same argument; the winner is decided by
	// sorted charm name, so the loser has no effect). Empty when the active charms
	// have disjoint edits. `magus describe target ...:a,b` surfaces it before a run.
	Conflicts []CharmConflict `json:"conflicts,omitempty"         yaml:"conflicts,omitempty"`
	// Service is set only when this spell's op is a service (a long-running process
	// magus supervises rather than runs to completion). It carries the static, pre-run
	// facts; Command above is the process itself. Nil for an ordinary command op.
	Service *ServiceView `json:"service,omitempty" yaml:"service,omitempty"`
}

// ServiceView is the static, pre-run description of a service op, shown by `magus
// describe target` when the target is a service. Every field is known without
// starting the service; live registry state (ref-count, probe status) needs the
// daemon and is not part of this static view.
type ServiceView struct {
	Readiness   []string `json:"readiness,omitempty"   yaml:"readiness,omitempty"`   // probe command polled until it exits 0, if any
	Stop        []string `json:"stop,omitempty"        yaml:"stop,omitempty"`        // graceful-shutdown command, if any
	Idle        string   `json:"idle,omitempty"        yaml:"idle,omitempty"`        // idle-timeout override (a duration), else the daemon default
	Distinct    string   `json:"distinct,omitempty"    yaml:"distinct,omitempty"`    // dedup opt-out reason; empty means the instance is shared
	Fingerprint string   `json:"fingerprint,omitempty" yaml:"fingerprint,omitempty"` // content hash that keys shared-instance dedup
}

// CharmConflict reports an active charm (Name) whose edit is overwritten by another
// active charm (OverriddenBy) on the same command, so Name has no effect there. The
// winner is decided by sorted charm name, not declared precedence.
type CharmConflict struct {
	Name         string `json:"name"                    yaml:"name"`
	OverriddenBy string `json:"overridden_by,omitempty" yaml:"overridden_by,omitempty"`
}

// CharmTraceStep is one line of a charm-application trace: the command (cmd as
// element 0) after the named charm's patch applies on top of the prior step. The
// base step (before any charm) has an empty Charm.
type CharmTraceStep struct {
	Charm   string   `json:"charm,omitempty"   yaml:"charm,omitempty"`
	Command []string `json:"command"           yaml:"command"`
}

// EvaluatedTargetEntry is the fully-resolved view of a single path:target pair.
type EvaluatedTargetEntry struct {
	Project   string                `json:"project"             yaml:"project"`
	Target    string                `json:"target"              yaml:"target"`
	Dir       string                `json:"dir"                 yaml:"dir"`
	Sources   []string              `json:"sources,omitempty"    yaml:"sources,omitempty"`
	Outputs   []string              `json:"outputs,omitempty"    yaml:"outputs,omitempty"`
	DependsOn []string              `json:"depends_on,omitempty" yaml:"depends_on,omitempty"`
	Charms    []string              `json:"charms,omitempty"     yaml:"charms,omitempty"`
	Spells    []EvaluatedSpellEntry `json:"spells,omitempty"     yaml:"spells,omitempty"`
	Policy    *Target               `json:"policy,omitempty"    yaml:"policy,omitempty"` // only the policy fields of Target are meaningful (SkipCache/Exclusive/FailOnDrift/RetryOnVolatile)
	Exclusive bool                  `json:"exclusive,omitempty" yaml:"exclusive,omitempty"`
}

// EvaluatedTargetsOutput is the top-level result for "describe target <ref>".
type EvaluatedTargetsOutput struct {
	Definition string                 `json:"definition" yaml:"definition"`
	Count      int                    `json:"count"      yaml:"count"`
	Targets    []EvaluatedTargetEntry `json:"targets"    yaml:"targets"`
}

// EvaluatedProjectEntry is the fully-resolved view of a project.
type EvaluatedProjectEntry struct {
	Path           string                `json:"path"                       yaml:"path"`
	Dir            string                `json:"dir"                        yaml:"dir"`
	Sources        []string              `json:"sources,omitempty"          yaml:"sources,omitempty"`
	Outputs        []string              `json:"outputs,omitempty"          yaml:"outputs,omitempty"`
	DependsOn      []string              `json:"depends_on,omitempty"       yaml:"depends_on,omitempty"`
	Spells         []EvaluatedSpellEntry `json:"spells,omitempty"           yaml:"spells,omitempty"`
	TargetPolicies map[string]Target     `json:"target_policies,omitempty"  yaml:"target_policies,omitempty"`
	Exclusive      bool                  `json:"exclusive,omitempty"        yaml:"exclusive,omitempty"`
}

// EvaluatedProjectsOutput is the top-level result for "describe projects --evaluated".
type EvaluatedProjectsOutput struct {
	Definition string                  `json:"definition" yaml:"definition"`
	Workspace  string                  `json:"workspace"  yaml:"workspace"`
	Count      int                     `json:"count"      yaml:"count"`
	Projects   []EvaluatedProjectEntry `json:"projects"   yaml:"projects"`
}

// WorkspaceDefinition is the human-readable description of a workspace shown by "magus describe workspaces".
const WorkspaceDefinition = "A workspace is a magus root directory that owns a set " +
	"of projects, a configuration file, a content-addressed cache, and VCS " +
	"integration. Every magus invocation operates within exactly one workspace, " +
	"identified by walking up from the current directory to the nearest go.mod."

// WorkspaceEntry holds details about the active workspace.
type WorkspaceEntry struct {
	Root         string `json:"root"                    yaml:"root"`
	VCSBaseRef   string `json:"vcs_base_ref,omitempty"  yaml:"vcs_base_ref,omitempty"`
	CacheDir     string `json:"cache_dir,omitempty"     yaml:"cache_dir,omitempty"`
	Concurrency  int    `json:"concurrency,omitempty"   yaml:"concurrency,omitempty"`
	ProjectCount int    `json:"project_count"           yaml:"project_count"`
}

// WorkspacesOutput is the top-level result for "describe workspaces".
type WorkspacesOutput struct {
	Definition string           `json:"definition"  yaml:"definition"`
	Count      int              `json:"count"       yaml:"count"`
	Workspaces []WorkspaceEntry `json:"workspaces"  yaml:"workspaces"`
}

// WorkspaceConfig carries infrastructure details for DescribeWorkspaces
// that are not part of the WorkspaceRepository interface (cache path,
// concurrency).
type WorkspaceConfig struct {
	CacheDir    string
	Concurrency int
}

// FileDefinition is the human-readable description printed by "magus describe file".
const FileDefinition = "Describe file classifies paths against the workspace's declared " +
	"globs: the project that owns each path, whether it is a declared output (generated: " +
	"regenerate it, never hand-edit) or a declared source (it feeds cache keys and the " +
	"affected set), and which projects claim it either way. It answers \"can I disregard " +
	"this changed file\" from the workspace's own declarations."

// FileEntry classifies one workspace-relative path.
type FileEntry struct {
	Path string `json:"path" yaml:"path"`
	// Project is the owning project by directory containment (longest project
	// path prefixing the file), empty when no project dir contains it.
	Project string `json:"project,omitempty" yaml:"project,omitempty"`
	// Role summarizes the strongest claim: "output" (a declared output glob
	// matches - the file is generated), "source" (a declared source glob
	// matches), or "unclaimed" (no project declares it; it invalidates no cache
	// key and affects no target).
	Role string `json:"role" yaml:"role"`
	// OutputOf and SourceOf list the projects whose declared output/source globs
	// match the path. A path can be both (a committed generated file is often a
	// source of downstream targets); Role reports output in that case because
	// the regeneration rule dominates how the file should be treated.
	OutputOf []string `json:"output_of,omitempty" yaml:"output_of,omitempty"`
	SourceOf []string `json:"source_of,omitempty" yaml:"source_of,omitempty"`
	// Hint is the one-line handling rule for the role, ready to surface to a
	// human or an agent.
	Hint string `json:"hint,omitempty" yaml:"hint,omitempty"`
}

// FilesOutput is the top-level result for "describe file <path>...".
type FilesOutput struct {
	Definition string      `json:"definition" yaml:"definition"`
	Count      int         `json:"count"      yaml:"count"`
	Files      []FileEntry `json:"files"      yaml:"files"`
}
