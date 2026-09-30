package knowledge

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func guardFixture(t *testing.T) (root, cacheDir string, g *Graph) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for rel, body := range map[string]string{
		"magusfile.buzz": "export fun lint() > void {}\n",
		"pkg/a.go":       "package pkg\nfunc Judge() {}\n",
		"docs/x.md":      "# Title\n",
		"notes.txt":      "not indexed\n",
	} {
		p := filepath.Join(root, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	g = NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "symbol:go pkg/Judge().", Kind: types.KindSymbol, Label: "Judge", Source: "pkg/a.go:2"})
	g.AddNode(types.KnowledgeNode{ID: "symbol:go other/Judge().", Kind: types.KindSymbol, Label: "Judge", Source: "other/j.go:1"})
	g.AddNode(types.KnowledgeNode{ID: "file:pkg/a.go", Kind: types.KindFile})
	g.AddNode(types.KnowledgeNode{ID: "file:pkg/b_test.go", Kind: types.KindFile})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/a.go", Target: "symbol:go pkg/Judge().", Relation: types.RelationDefines, Provenance: "pkg/a.go"})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/b_test.go", Target: "symbol:go pkg/Judge().", Relation: types.RelationReferences, Provenance: "scip count=2 lines=4,9"})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/b_test.go", Target: "symbol:go other/Judge().", Relation: types.RelationReferences, Provenance: "scip count=1 lines=12"})
	g.AddEdge(types.KnowledgeEdge{Source: "file:pkg/a.go", Target: "symbol:go pkg/Judge().", Relation: types.RelationCalls, Provenance: "scip count=1"})
	g.AddNode(types.KnowledgeNode{ID: "docsection:docs/x.md#title", Kind: types.KindDocSection})
	g.AddNode(types.KnowledgeNode{ID: "target:.:lint", Kind: types.KindTarget})
	g.AddNode(types.KnowledgeNode{ID: "diagnostic:MGS1001", Kind: types.KindDiagnostic})
	g.AddNode(types.KnowledgeNode{ID: "spell:go", Kind: types.KindSpell})
	return root, t.TempDir(), g
}

// TestGuardIndexRoundTrip pins what the guard reads back: names for symbols, ids for the
// other kinds, nothing for a kind it never asks about, and every kind fresh right after
// the write.
func TestGuardIndexRoundTrip(t *testing.T) {
	root, cacheDir, g := guardFixture(t)
	require.NoError(t, WriteGuardIndex(cacheDir, root, g, true))

	x, err := ReadGuardIndex(cacheDir, root)
	require.NoError(t, err)
	assert.True(t, x.Has(GuardSymbol, "Judge"))
	assert.False(t, x.Has(GuardSymbol, "Jud"))
	assert.Equal(t, []string{"docsection:docs/x.md#title"}, x.IDs(types.KindDocSection))
	assert.Equal(t, []string{"target:.:lint"}, x.IDs(types.KindTarget))
	assert.Equal(t, []string{"diagnostic:MGS1001"}, x.IDs(types.KindDiagnostic))
	assert.Equal(t, []string{"file:pkg/a.go", "file:pkg/b_test.go"}, x.IDs(types.KindFile))
	assert.Empty(t, x.IDs(types.KindSpell))
	for _, kind := range []string{GuardSymbol, types.KindDocSection, types.KindTarget, types.KindDiagnostic, types.KindFile} {
		assert.True(t, x.Fresh(kind), kind)
	}

	// Sites are keyed by name and folded per file across every symbol of that name; a
	// definition contributes its declaring line, and a call edge contributes nothing.
	sites, err := x.RefSites("Judge")
	require.NoError(t, err)
	assert.Equal(t, []types.KnowledgeRefSite{
		{File: "pkg/a.go", Count: 1, Lines: []int{2}},
		{File: "pkg/b_test.go", Count: 3, Lines: []int{4, 9, 12}},
	}, sites)
	sites, err = x.RefSites("Jud")
	require.NoError(t, err)
	assert.Empty(t, sites)
}

// The index and its reference sites name the workspace's symbols and files, so both stay
// private to their owner.
func TestGuardIndexFilesAreOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX modes")
	}
	root, cacheDir, g := guardFixture(t)
	require.NoError(t, WriteGuardIndex(cacheDir, root, g, true))

	for _, p := range []string{GuardIndexPath(cacheDir), guardRefsPath(cacheDir)} {
		fi, err := os.Stat(p)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), fi.Mode().Perm(), p)
	}
}

// TestGuardIndexFreshness pins what makes a kind non-definitive: an edit to a file of its
// class, a file added beside one, and for symbols an index that was already stale when the
// graph was built. A file no kind reads changes nothing.
func TestGuardIndexFreshness(t *testing.T) {
	read := func(t *testing.T, cacheDir, root string) *GuardIndex {
		t.Helper()
		x, err := ReadGuardIndex(cacheDir, root)
		require.NoError(t, err)
		return x
	}
	future := time.Now().Add(time.Hour)

	t.Run("edited source", func(t *testing.T) {
		root, cacheDir, g := guardFixture(t)
		require.NoError(t, WriteGuardIndex(cacheDir, root, g, true))
		require.NoError(t, os.Chtimes(filepath.Join(root, "pkg/a.go"), future, future))
		x := read(t, cacheDir, root)
		assert.False(t, x.Fresh(GuardSymbol))
		assert.True(t, x.Fresh(types.KindDocSection), "a Go edit says nothing about docs")
		assert.True(t, x.Fresh(types.KindTarget))
		assert.False(t, x.Fresh(types.KindFile), "a file node may come from any indexed source")
	})
	t.Run("added file", func(t *testing.T) {
		root, cacheDir, g := guardFixture(t)
		require.NoError(t, WriteGuardIndex(cacheDir, root, g, true))
		require.NoError(t, os.WriteFile(filepath.Join(root, "pkg/b.go"), []byte("package pkg\n"), 0o644))
		assert.False(t, read(t, cacheDir, root).Fresh(GuardSymbol))
	})
	t.Run("unrelated file", func(t *testing.T) {
		root, cacheDir, g := guardFixture(t)
		require.NoError(t, WriteGuardIndex(cacheDir, root, g, true))
		require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte("binary"), 0o755))
		require.NoError(t, os.Chtimes(filepath.Join(root, "notes.txt"), future, future))
		assert.True(t, read(t, cacheDir, root).Fresh(GuardSymbol))
	})
	t.Run("stale symbol index", func(t *testing.T) {
		root, cacheDir, g := guardFixture(t)
		require.NoError(t, WriteGuardIndex(cacheDir, root, g, false))
		x := read(t, cacheDir, root)
		assert.False(t, x.Fresh(GuardSymbol))
		assert.True(t, x.Fresh(types.KindDocSection))
	})
}

func TestGuardIndexRefusesAnotherRoot(t *testing.T) {
	root, cacheDir, g := guardFixture(t)
	_, err := ReadGuardIndex(cacheDir, root)
	require.Error(t, err, "no index yet")
	require.NoError(t, WriteGuardIndex(cacheDir, root, g, true))
	_, err = ReadGuardIndex(cacheDir, t.TempDir())
	assert.Error(t, err)
}
