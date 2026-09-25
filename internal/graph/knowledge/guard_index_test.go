package knowledge

import (
	"os"
	"path/filepath"
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
	g.AddNode(types.KnowledgeNode{ID: "symbol:go pkg/Judge().", Kind: types.KindSymbol, Label: "Judge"})
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
	assert.Empty(t, x.IDs(types.KindSpell))
	for _, kind := range []string{GuardSymbol, types.KindDocSection, types.KindTarget, types.KindDiagnostic} {
		assert.True(t, x.Fresh(kind), kind)
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
