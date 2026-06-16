package types

import "context"

// This file collects the repository-style domain interfaces — the access surfaces
// consumers depend on, implemented by concrete types elsewhere (e.g. *Workspace,
// internal/depgraph). Interfaces that are ports/callbacks rather than repositories
// (SpellDriver, VCSDriver, MergeDriverInstaller, Observer, TargetNameNormalizer) stay
// beside their domain types: each is referenced by a types-level declaration, so
// hoisting it here would make types import its implementer and cycle.

// GraphRepository is the interface that internal/depgraph implements.
type GraphRepository interface {
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
	ExpandAffected(ctx context.Context, target, baseRef string) ([]Target, string, error)
}

// AffectedComputer computes the VCS-impacted project set.
type AffectedComputer interface {
	Affected(ctx context.Context, base string) (*AffectedResult, error)
	AffectedFromPaths(ctx context.Context, paths []string) (*AffectedResult, error)
}

// Describer returns the structured inventory behind `magus describe`.
type Describer interface {
	DescribeSpells() SpellsOutput
	DescribeTargets() TargetsOutput
	DescribeGraph() TargetGraphOutput
	DescribeProjects() ProjectsOutput
	DescribeWorkspaces(cfg WorkspaceConfig) WorkspacesOutput
	DescribeTarget(t Target) (EvaluatedTargetsOutput, error)
	DescribeEvaluatedProjects() EvaluatedProjectsOutput
}

// WorkspaceRepository is the full domain interface for a discovered workspace.
// Prefer the narrowest embedded role a consumer actually uses.
type WorkspaceRepository interface {
	WorkspaceReader
	TargetExpander
	AffectedComputer
	Describer
}
