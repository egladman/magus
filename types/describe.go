package types

// Describe-output types and the concept definitions printed by "magus describe".

// ── Spells ────────────────────────────────────────────────────────────────────

// SpellDefinition is the human-readable description of a spell shown by "magus describe spells".
const SpellDefinition = "A spell is a language/runtime adapter that " +
	"teaches magus how to build, test, lint, and format projects of a given type. " +
	"Spells are registered at startup and bound to projects via explicit registration " +
	"in the magusfile (magus.spell.register or magus.spell.load)."

// SpellEntry is the structured view of a single spell.
type SpellEntry struct {
	Name           string   `json:"name"              yaml:"name"`
	Sources        []string `json:"sources,omitempty" yaml:"sources,omitempty"`
	Outputs        []string `json:"outputs,omitempty" yaml:"outputs,omitempty"`
	Claims         []string `json:"claims,omitempty"  yaml:"claims,omitempty"`
	Targets        []string `json:"targets,omitempty" yaml:"targets,omitempty"`
	ForeignProcess bool     `json:"foreign_process,omitempty" yaml:"foreign_process,omitempty"`
	// TargetDocs maps a target name to its handler's doc comment, for the targets
	// that have one. Populated for Buzz spells (built-in docs are not serialized in
	// bytecode, so only workspace-local Buzz spells carry them here).
	TargetDocs map[string]string `json:"target_docs,omitempty" yaml:"target_docs,omitempty"`
}

// SpellsOutput is the top-level result for "describe spells".
type SpellsOutput struct {
	Definition string       `json:"definition" yaml:"definition"`
	Count      int          `json:"count"      yaml:"count"`
	Spells     []SpellEntry `json:"spells"     yaml:"spells"`
}

// ── Targets ───────────────────────────────────────────────────────────────────

// TargetDefinition is the human-readable description of a target shown by "magus describe targets".
const TargetDefinition = "A target is a named operation (e.g. build, test, lint) declared as an " +
	"exported function in a project's magusfile, which may compose a spell's " +
	"tool-native operations. 'ci' is the conventional anchor that the affected set " +
	"keys off — magus runs it read-only but does not hardcode its steps; the magusfile " +
	"composes them with magus.depends_on."

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
const TargetGraphDefinition = "The target dependency graph is the magus.depends_on " +
	"DAG of a project's magusfile: each node is a target (an exported function), each " +
	"edge a dependency it composes. It is extracted statically from the magusfile " +
	"source, so it shows every edge — including both arms of a runtime branch — and " +
	"flags any dependency cycle (which the run path rejects during dispatch)."

// TargetGraphNode is one target in the graph: its run name, its doc comment, the
// names of the targets it depends on, and the charm names its body branches on.
type TargetGraphNode struct {
	Name   string   `json:"name"             yaml:"name"`
	Doc    string   `json:"doc,omitempty"    yaml:"doc,omitempty"`
	Deps   []string `json:"deps,omitempty"   yaml:"deps,omitempty"`
	Charms []string `json:"charms,omitempty" yaml:"charms,omitempty"`
}

// TargetGraphProject is one project's target graph, plus a detected cycle (a path
// of node names that begins and ends at the same node) when the DAG is not acyclic.
type TargetGraphProject struct {
	Path   string            `json:"path"             yaml:"path"`
	Engine string            `json:"engine,omitempty" yaml:"engine,omitempty"`
	Nodes  []TargetGraphNode `json:"nodes,omitempty"  yaml:"nodes,omitempty"`
	Cycle  []string          `json:"cycle,omitempty"  yaml:"cycle,omitempty"`
	// RelPath is Path expressed relative to the VCS (repo) root, used only for an
	// unambiguous MAGUS.md heading when a project sits at the workspace root (Path
	// is "."). Display-only and repo-derived, so it is not serialized; the run path
	// still addresses the project by Path. Empty outside a repo.
	RelPath string `json:"-" yaml:"-"`
}

// TargetGraphOutput is the top-level result for "describe graph".
type TargetGraphOutput struct {
	Definition string               `json:"definition" yaml:"definition"`
	Projects   []TargetGraphProject `json:"projects"   yaml:"projects"`
}

// ── Projects ──────────────────────────────────────────────────────────────────

// ProjectDefinition is the human-readable description of a project shown by "magus describe projects".
const ProjectDefinition = "A project is a directory the workspace recognized as a " +
	"unit of work, bound to one or more spells. Projects are " +
	"discovered by the presence of a magusfile (magusfile.tl or magusfile.bzz, or a magusfiles/ subdirectory) " +
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

// ── Modules ───────────────────────────────────────────────────────────────────

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
	NativeBuzz string `json:"native_buzz,omitempty" yaml:"native_buzz,omitempty"`
}

// ModuleFieldEntry is one static, table-level value on a module (e.g. vcs.name).
type ModuleFieldEntry struct {
	Name string `json:"name"          yaml:"name"`
	Type string `json:"type"          yaml:"type"`
	Doc  string `json:"doc,omitempty" yaml:"doc,omitempty"`
}

// ModuleEntry is a module's summary; Fields/Methods are populated only for the detail view.
type ModuleEntry struct {
	Name    string              `json:"name"              yaml:"name"`
	Doc     string              `json:"doc,omitempty"     yaml:"doc,omitempty"`
	Fields  []ModuleFieldEntry  `json:"fields,omitempty"  yaml:"fields,omitempty"`
	Methods []ModuleMethodEntry `json:"methods,omitempty" yaml:"methods,omitempty"`
}

// ModulesOutput is the top-level result for "describe modules" / "describe module <name>".
type ModulesOutput struct {
	Definition string        `json:"definition" yaml:"definition"`
	Count      int           `json:"count"      yaml:"count"`
	Modules    []ModuleEntry `json:"modules"    yaml:"modules"`
}

// ── Evaluated targets ─────────────────────────────────────────────────────────

// EvaluatedTargetDefinition is the human-readable description of an evaluated target shown by "magus describe".
const EvaluatedTargetDefinition = "An evaluated target shows the fully-resolved " +
	"dispatch plan for a specific path:target pair: the workspace-rooted source and " +
	"output globs that feed the cache key, the spells that will fire (with " +
	"target-specific sources and effective claims after weight/add/remove resolution), " +
	"and any behavioural policy (CheckClean, TrackFlake, Isolated)."

// EvaluatedSpellEntry is one spell's contribution to an evaluated target.
type EvaluatedSpellEntry struct {
	Name            string   `json:"name"                        yaml:"name"`
	TargetSources   []string `json:"target_sources,omitempty"    yaml:"target_sources,omitempty"`
	EffectiveClaims []string `json:"effective_claims,omitempty"  yaml:"effective_claims,omitempty"`
	ClaimWeight     int      `json:"claim_weight,omitempty"      yaml:"claim_weight,omitempty"`
	// Command is the fork command this spell's op would run for the target, with
	// the requested charms applied (cmd as element 0). Empty for function-op or
	// no-op targets, whose argv isn't statically knowable. Preview only — `magus
	// describe` renders it; nothing is executed.
	Command []string `json:"command,omitempty"           yaml:"command,omitempty"`
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
	Policy    *TargetPolicy         `json:"policy,omitempty"    yaml:"policy,omitempty"`
	Exclusive bool                  `json:"exclusive,omitempty" yaml:"exclusive,omitempty"`
}

// EvaluatedTargetsOutput is the top-level result for "describe target <ref>".
type EvaluatedTargetsOutput struct {
	Definition string                 `json:"definition" yaml:"definition"`
	Count      int                    `json:"count"      yaml:"count"`
	Targets    []EvaluatedTargetEntry `json:"targets"    yaml:"targets"`
}

// ── Evaluated projects ────────────────────────────────────────────────────────

// EvaluatedProjectEntry is the fully-resolved view of a project.
type EvaluatedProjectEntry struct {
	Path           string                  `json:"path"                       yaml:"path"`
	Dir            string                  `json:"dir"                        yaml:"dir"`
	Sources        []string                `json:"sources,omitempty"          yaml:"sources,omitempty"`
	Outputs        []string                `json:"outputs,omitempty"          yaml:"outputs,omitempty"`
	DependsOn      []string                `json:"depends_on,omitempty"       yaml:"depends_on,omitempty"`
	Spells         []EvaluatedSpellEntry   `json:"spells,omitempty"           yaml:"spells,omitempty"`
	TargetPolicies map[string]TargetPolicy `json:"target_policies,omitempty"  yaml:"target_policies,omitempty"`
	Exclusive      bool                    `json:"exclusive,omitempty"        yaml:"exclusive,omitempty"`
}

// EvaluatedProjectsOutput is the top-level result for "describe projects --evaluated".
type EvaluatedProjectsOutput struct {
	Definition string                  `json:"definition" yaml:"definition"`
	Workspace  string                  `json:"workspace"  yaml:"workspace"`
	Count      int                     `json:"count"      yaml:"count"`
	Projects   []EvaluatedProjectEntry `json:"projects"   yaml:"projects"`
}

// ── Workspaces ────────────────────────────────────────────────────────────────

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
