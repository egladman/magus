package magus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// TestGitRoot walks to the nearest ancestor holding a .git entry. The entry is a
// directory in a normal clone and a FILE in a worktree or submodule, so both shapes
// have to resolve.
func TestGitRoot(t *testing.T) {
	t.Run("a .git directory", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".git"), 0o755))
		deep := filepath.Join(root, "a", "b")
		require.NoError(t, os.MkdirAll(deep, 0o755))

		assert.Equal(t, root, gitRoot(deep))
		assert.Equal(t, root, gitRoot(root), "the walk is inclusive of dir itself")
	})

	t.Run("a .git file, as a worktree writes one", func(t *testing.T) {
		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: /elsewhere\n"), 0o644))
		deep := filepath.Join(root, "a")
		require.NoError(t, os.MkdirAll(deep, 0o755))

		assert.Equal(t, root, gitRoot(deep))
	})

	t.Run("no repository above the directory", func(t *testing.T) {
		// t.TempDir is under the system temp root, which carries no .git; the walk
		// terminates at the filesystem root rather than looping.
		assert.Empty(t, gitRoot(filepath.Join(string(filepath.Separator), "nonexistent-magus-cov-path")))
	})
}

// TestFindNearestLock: proximity outranks declared order. The candidate list is an
// alternation over package managers, not a preference between them, so a project
// holding its own yarn.lock beats a pnpm-lock.yaml hoisted to the workspace root.
func TestFindNearestLock(t *testing.T) {
	root := t.TempDir()
	member := filepath.Join(root, "packages", "app")
	require.NoError(t, os.MkdirAll(member, 0o755))

	candidates := []string{"pnpm-lock.yaml", "yarn.lock"}

	t.Run("no candidates", func(t *testing.T) {
		_, ok := findNearestLock(nil, member, root)
		assert.False(t, ok)
	})

	t.Run("nothing on the walk", func(t *testing.T) {
		_, ok := findNearestLock(candidates, member, root)
		assert.False(t, ok)
	})

	t.Run("hoisted to the workspace root", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), nil, 0o644))
		got, ok := findNearestLock(candidates, member, root)
		require.True(t, ok)
		assert.Equal(t, "pnpm-lock.yaml", got, "the result is root-relative and slash-separated")
	})

	t.Run("a nearer lock wins over the declared order", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(member, "yarn.lock"), nil, 0o644))
		got, ok := findNearestLock(candidates, member, root)
		require.True(t, ok)
		assert.Equal(t, "packages/app/yarn.lock", got)
	})

	t.Run("a directory outside root yields no hit", func(t *testing.T) {
		_, ok := findNearestLock(candidates, t.TempDir(), root)
		assert.False(t, ok, "the walk must stop at root rather than reaching the filesystem root")
	})
}

// TestProjectLockfiles deduplicates across manifests: a workspace whose root lock
// serves several manifests reports that lock once.
func TestProjectLockfiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "web")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "pnpm-lock.yaml"), nil, 0o644))

	manifests := []spells.Manifest{
		{Value: "package.json", LockCandidates: []string{"pnpm-lock.yaml", "package-lock.json"}},
		{Value: "tsconfig.json", LockCandidates: []string{"pnpm-lock.yaml"}},
		{Value: "Cargo.toml", LockCandidates: []string{"Cargo.lock"}},
	}

	assert.Equal(t, []string{"pnpm-lock.yaml"}, projectLockfiles(manifests, dir, root),
		"one hoisted lock serving two manifests is reported once, and an unresolved manifest contributes nothing")

	assert.Nil(t, projectLockfiles(manifests, "", root), "no project dir, nothing to walk from")
	assert.Nil(t, projectLockfiles(manifests, dir, ""), "no workspace root, nothing to stop the walk")
	assert.Nil(t, projectLockfiles(nil, dir, root))
}

func TestManifestNames(t *testing.T) {
	assert.Nil(t, manifestNames(nil))
	assert.Equal(t, []string{"package.json", "Cargo.toml"}, manifestNames([]spells.Manifest{
		{Value: "package.json"}, {Value: "Cargo.toml"},
	}), "the bare filenames, in declared order")
}

func TestAppendUniq(t *testing.T) {
	assert.Equal(t, []string{"a"}, appendUniq(nil, "a"))
	assert.Equal(t, []string{"a", "b"}, appendUniq([]string{"a"}, "b"))
	assert.Equal(t, []string{"a", "b"}, appendUniq([]string{"a", "b"}, "a"))
}

// TestResolveChain: a step's import path is dot-relative to the importing
// magusfile. An unresolvable one KEEPS its raw path rather than being dropped -
// the chain is the target's shape, and a silently shortened one misreports it.
func TestResolveChain(t *testing.T) {
	got := resolveChain([]types.ChainStep{
		{Target: "build"},
		{Project: "../lib", Target: "build"},
		{Project: "../../outside", Target: "x"},
	}, "web")

	assert.Equal(t, []types.ChainStep{
		{Target: "build"},
		{Project: "lib", Target: "build"},
		{Project: "../../outside", Target: "x"},
	}, got)

	assert.Empty(t, resolveChain(nil, "web"))
}

// TestResolveNodeRefs rewrites cross-project references into the workspace-relative
// form the graph keys projects by. Unlike a chain step, an unresolvable dependency
// or input is DROPPED: an edge to a project that does not resolve is not an edge.
func TestResolveNodeRefs(t *testing.T) {
	nodes := []types.TargetGraphNode{{
		Name: "build",
		CrossDependencies: []types.CrossTargetRef{
			{Project: "../lib", Target: "build"},
			{Project: "../../outside", Target: "x"},
		},
		Chain: []types.ChainStep{{Project: "../lib", Target: "build"}},
		ReadsFiles: []types.InputRef{
			{Glob: "src/**"},
			{Project: "../lib", Glob: "gen/**"},
			{Project: "../../outside", Glob: "x"},
		},
	}}

	resolveNodeRefs(nodes, "web")

	assert.Equal(t, []types.CrossTargetRef{{Project: "lib", Target: "build"}}, nodes[0].CrossDependencies)
	assert.Equal(t, []types.ChainStep{{Project: "lib", Target: "build"}}, nodes[0].Chain)
	assert.Equal(t, []types.InputRef{
		{Project: "web", Glob: "src/**"},
		{Project: "lib", Glob: "gen/**"},
	}, nodes[0].ReadsFiles,
		"a same-project input takes the declaring project's path so a consumer can join Project and Glob")
}

func TestResolveNodeRefsLeavesAnEmptyNodeAlone(t *testing.T) {
	nodes := []types.TargetGraphNode{{Name: "build"}}
	resolveNodeRefs(nodes, "web")
	assert.Equal(t, []types.TargetGraphNode{{Name: "build"}}, nodes)
}

// TestExistingWriter covers all three ways a cross-project output path can already
// be claimed. The writer re-declaring its OWN glob is idempotent and must not
// report a collision with itself.
func TestExistingWriter(t *testing.T) {
	co := crossOutput{owner: "site", writer: "p1", glob: "shared.txt"}

	t.Run("another writer holds the same inbound path", func(t *testing.T) {
		owner := &types.Project{Path: "site", InboundOutputs: map[string][]string{
			"p2": {"shared.txt"},
			"p3": {"other.txt"},
		}}
		assert.Equal(t, "p2", existingWriter(owner, co))
	})

	t.Run("the writer re-declaring its own glob is idempotent", func(t *testing.T) {
		owner := &types.Project{Path: "site", InboundOutputs: map[string][]string{"p1": {"shared.txt"}}}
		assert.Empty(t, existingWriter(owner, co))
	})

	t.Run("the owner declares it project-wide", func(t *testing.T) {
		owner := &types.Project{Path: "site", Outputs: []string{"shared.txt"}}
		assert.Equal(t, "site", existingWriter(owner, co))
	})

	t.Run("the owner declares it from one of its own targets", func(t *testing.T) {
		owner := &types.Project{Path: "site", TargetOutputs: map[string][]types.OutputRef{
			"build": {{Glob: "shared.txt"}},
		}}
		assert.Equal(t, "site", existingWriter(owner, co))

		owner.TargetOutputs["build"] = []types.OutputRef{{Project: "site", Glob: "shared.txt"}}
		assert.Equal(t, "site", existingWriter(owner, co),
			"an explicit self-reference is the same claim as a bare one")
	})

	t.Run("the owner writes the glob into a third project", func(t *testing.T) {
		owner := &types.Project{Path: "site", TargetOutputs: map[string][]types.OutputRef{
			"build": {{Project: "elsewhere", Glob: "shared.txt"}},
		}}
		assert.Empty(t, existingWriter(owner, co),
			"a glob relative to a third tree is not a claim on this one")
	})

	t.Run("nothing claims it", func(t *testing.T) {
		assert.Empty(t, existingWriter(&types.Project{Path: "site"}, co))
	})
}

// TestMatchedClaims reports each declaration that names a path individually, with
// the target that made it. It deliberately does NOT read InboundOutputs: the caller
// walks every project, so reading it here would report one declaration twice.
func TestMatchedClaims(t *testing.T) {
	p := &types.Project{
		Path:    "web",
		Outputs: []string{"dist/**"},
		TargetOutputs: map[string][]types.OutputRef{
			"generate": {{Glob: "gen/**"}, {Project: "site", Glob: "index.html"}},
		},
		TargetInputs: map[string][]types.InputRef{
			"build": {{Glob: "src/**"}},
		},
		TargetUpdates: map[string][]types.UpdateRef{
			"generate": {{Glob: "README.md"}},
		},
		InboundOutputs: map[string][]string{"p2": {"vendor/**"}},
	}

	assert.Equal(t, []types.FileClaim{{Project: "web", Target: "", Role: "output", Glob: "web/dist/**"}},
		matchedClaims(p, nil, "web/dist/app.js"))

	assert.Equal(t, []types.FileClaim{{Project: "web", Target: "generate", Role: "output", Glob: "web/gen/**"}},
		matchedClaims(p, nil, "web/gen/types.go"))

	assert.Equal(t, []types.FileClaim{{Project: "web", Target: "generate", Role: "output", Glob: "site/index.html"}},
		matchedClaims(p, nil, "site/index.html"),
		"a cross-project ref is rooted at the tree its glob is relative to")

	assert.Equal(t, []types.FileClaim{
		{Project: "web", Target: "", Role: "source", Glob: "web/src/**"},
		{Project: "web", Target: "build", Role: "source", Glob: "web/src/**"},
	}, matchedClaims(p, []string{"web/src/**"}, "web/src/main.go"),
		"the project-wide source and the per-target one are distinct declarations")

	assert.Equal(t, []types.FileClaim{{Project: "web", Target: "generate", Role: "update", Glob: "web/README.md"}},
		matchedClaims(p, nil, "web/README.md"))

	assert.Empty(t, matchedClaims(p, nil, "p2/vendor/lib.js"),
		"an inbound glob belongs to the writer's own walk, not the owner's")
	assert.Empty(t, matchedClaims(p, nil, "other/file.txt"))
}
