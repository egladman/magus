package scipbuzz

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeFiles(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, nil, 0o644))
	}
}

func TestDiscoverSkipsNestedProjectsAndFixtures(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root,
		"magusfile.buzz",
		"a.buzz",
		"hack/b.buzz",
		"notes.txt",
		"nested/magusfile.buzz",
		"nested/c.buzz",
		"testdata/d.buzz",
		".git/e.buzz",
		"node_modules/f.buzz",
		"vendor/g.buzz",
		"hack/vendor/h.buzz",
	)
	files, err := discover(root)
	require.NoError(t, err)
	require.Equal(t, []string{"a.buzz", "hack/b.buzz", "magusfile.buzz"}, files)
}

func TestFindWorkspaceRoot(t *testing.T) {
	ws := t.TempDir()
	writeFiles(t, ws, "magus.yaml", "libs/app/app.buzz")
	require.Equal(t, ws, findWorkspaceRoot(filepath.Join(ws, "libs", "app")))
	require.Equal(t, ws, findWorkspaceRoot(ws))

	bare := t.TempDir()
	require.Equal(t, bare, findWorkspaceRoot(bare))
}
