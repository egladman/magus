package types

import "context"

// This file collects the repository-style domain interfaces — the access surfaces
// consumers depend on, implemented by concrete types elsewhere (e.g. *Workspace,
// internal/graph/dependency). Interfaces that are ports/callbacks rather than repositories
// (SpellDriver, VCSDriver, MergeDriverInstaller, Observer, TargetNameNormalizer) stay
// beside their domain types: each is referenced by a types-level declaration, so
// hoisting it here would make types import its implementer and cycle.

// DepGraphRepository is the interface that internal/graph/dependency implements.
type DepGraphRepository interface {
	TopoSort() []string
	ReverseClosure(seeds []string) []string
	NearCycles(ctx context.Context, maxDepth int) []NearCycle
	BlastRadius() map[string]int
	NCCD() float64
	PathsFromSeeds(seeds []string, target string) []AffectedPath
	Successors(path string) []string
	Predecessors(path string) []string
	Nodes() []string
}

// WorkspaceReader is the read-only in-memory view of a discovered workspace.
type WorkspaceReader interface {
	Root() string
	All() []*Project
	Get(path string) *Project
	// Graph returns the PROJECT dependency graph (project -> project, from
	// depends_on). See Inspector.TargetGraph for the target-level graph.
	Graph() (*Graph, error)
	VCSOptions() VCSOptions
	Where(dir string) (*Project, bool)
}

// TargetExpander resolves a Target into concrete per-project targets.
type TargetExpander interface {
	ExpandPath(t Target) ([]Target, error)
	// ExpandCwd resolves t against the project containing the current working
	// directory. found reports whether cwd is inside any project; when false,
	// targets is empty and the caller typically falls back to ExpandPath or
	// reports "not inside a project". found is a deliberate signal distinct from
	// len(targets) — callers (e.g. magus tail) key their error message on it.
	ExpandCwd(t Target) (targets []Target, found bool, err error)
	ExpandAffected(ctx context.Context, target, baseRef string) (targets []Target, source string, fellBack bool, err error)
}

// AffectedComputer computes the VCS-impacted project set.
type AffectedComputer interface {
	Affected(ctx context.Context, base string) (*AffectedResult, error)
	AffectedFromPaths(ctx context.Context, paths []string) (*AffectedResult, error)
}

// Inspector reads the structured facts of a workspace without evaluating anything:
// what projects, targets, charms, and files exist, and how a specific target
// resolves. Organized on one axis - List* enumerates a declaration (cheap), Evaluate*
// resolves one (it costs), Classify* and TargetGraph say what they do.
type Inspector interface {
	// ListCharms builds the inverse charm index: every charm name a target in the
	// workspace declares, plus the reserved built-ins and the workspace's own
	// default_charms set (read from the receiver's config), and for each the
	// project/target/spell declarations that give it a patch.
	ListCharms(ctx context.Context) ([]Charm, error)
	ListTargets(ctx context.Context) ([]TargetEntry, error)
	ListProjects(ctx context.Context) (ProjectsOutput, error)
	// EvaluateProjects returns the fully-resolved project inventory: every
	// ListProjects fact plus each project's resolved spells and target policies.
	EvaluateProjects(ctx context.Context) (EvaluatedProjectsOutput, error)
	// EvaluateTarget returns the fully-evaluated dispatch plan for t.
	EvaluateTarget(ctx context.Context, t Target) ([]EvaluatedTarget, error)
	ClassifyFiles(ctx context.Context, paths []string) ([]FileEntry, error)
	// TargetGraph returns the ctx.needs dependency DAG of each project's targets,
	// read statically from the magusfile source. A different graph, at a different
	// granularity, from WorkspaceReader.Graph: that one is the PROJECT dependency
	// graph (project -> project, from depends_on); this one is the TARGET graph
	// within and across projects (target -> target, from ctx.needs).
	TargetGraph(ctx context.Context) (TargetGraphOutput, error)
	// Workspace returns the single-entry view of this workspace: a *Magus is
	// always exactly one workspace. The CLI's `describe workspaces` merges these
	// across the daemon's declared roots when daemon.workspaces is set.
	Workspace(ctx context.Context, cfg WorkspaceConfig) (WorkspaceEntry, error)
}

// WorkspaceRepository is the full domain interface for a discovered workspace.
// Prefer the narrowest embedded role a consumer actually uses.
type WorkspaceRepository interface {
	WorkspaceReader
	TargetExpander
	AffectedComputer
	Inspector
}
