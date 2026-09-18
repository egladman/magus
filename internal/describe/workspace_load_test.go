package describe

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceLoadFilesFindsEveryFileTheWorkspaceMustRead is the classification behind
// the shared-checkout refusal: these are the files whose half-saved state stops EVERY
// worker in the checkout, so they are named from the tree rather than from a list.
func TestWorkspaceLoadFilesFindsEveryFileTheWorkspaceMustRead(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	write("magus.yaml", "version: 1\n")
	write("magusfile.buzz", "import \"spells/house\" as house;\nimport \"magus/spell/go\" as go;\n")
	write("spells/house.buzz", "export fun brew() {}\n")
	write("spells/unimported.buzz", "export fun cold() {}\n")
	write("docs/magusfile.buzz", "import \"spells/render\" as render;\n")
	write("docs/spells/render/spell.buzz", "export fun page() {}\n")
	write("internal/job/store.go", "package job\n")
	write("node_modules/pkg/magusfile.buzz", "export fun no() {}\n")

	got := WorkspaceLoadFiles(root)

	assert.Equal(t, []string{
		"docs/magusfile.buzz",
		"docs/spells/render/spell.buzz",
		"magus.yaml",
		"magusfile.buzz",
		"spells/house.buzz",
	}, got)
}

// TestWorkspaceLoadFilesReadsTheMagusfilesDirectoryForm covers the second magusfile
// layout, which a project using it would otherwise have unprotected.
func TestWorkspaceLoadFilesReadsTheMagusfilesDirectoryForm(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "magusfiles"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfiles", "main.buzz"), []byte("import \"spells/a\" as a;\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "spells"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "spells", "a.buzz"), []byte("export fun a() {}\n"), 0o644))

	assert.Equal(t, []string{"magusfiles/main.buzz", "spells/a.buzz"}, WorkspaceLoadFiles(root))
}
