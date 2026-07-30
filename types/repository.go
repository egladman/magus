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

// Describer returns the structured inventory behind `magus describe`.
type Describer interface {
	DescribeSpells() SpellsOutput
	DescribeCharms(defaults []string) CharmsOutput
	DescribeTargets() TargetsOutput
	DescribeGraph(ctx context.Context) TargetGraphOutput
	DescribeProjects() ProjectsOutput
	DescribeWorkspaces(cfg WorkspaceConfig) WorkspacesOutput
	DescribeTarget(t Target) (EvaluatedTargetsOutput, error)
	DescribeEvaluatedProjects() EvaluatedProjectsOutput
	DescribeFiles(paths []string) FilesOutput
}

// WorkspaceRepository is the full domain interface for a discovered workspace.
// Prefer the narrowest embedded role a consumer actually uses.
type WorkspaceRepository interface {
	WorkspaceReader
	TargetExpander
	AffectedComputer
	Describer
}


// CacheRepository is implemented by a unit of work: a spell op and a target. Both
// answer the same question - what will actually run - so the cache asks it once through
// one method instead of guessing from the target's name. A name is only a label: two
// names over identical work can never share an entry, and one name over two bodies
// hashes as though they were the same thing.
//
// A Spell is deliberately NOT an implementer. A spell provides many ops, so it could
// only answer by being told which one, and that argument is what made the two sides
// asymmetric: an op and a target each already know what they are.
type CacheRepository interface {
	// Key returns the lines identifying this work, in a stable order (argument
	// order is meaning, so nothing sorts them). Nil adds nothing to the key, which is
	// not the same as adding an empty line.
	Key() []string
}
