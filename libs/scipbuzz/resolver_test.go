package scipbuzz

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestResolveOrder pins where an import is looked for: beside the importing file,
// then at the project root, then at the workspace root; anything else, and
// anything outside the workspace, is an external module.
func TestResolveOrder(t *testing.T) {
	ws := t.TempDir()
	writeFiles(t, ws,
		"magus.yaml",
		"shared.buzz",
		"hack/lint/support.buzz",
		"docs/lib/text.buzz",
		"docs/engine/page.buzz",
		"docs/engine/route.buzz",
		"docs/engine/shared.buzz",
	)
	project := filepath.Join(ws, "docs")
	ix := &indexer{project: project, workspace: ws, files: map[string]*file{}}
	from := ix.load(filepath.Join(project, "engine", "page.buzz"))
	require.NotNil(t, from)

	cases := map[string]string{
		"route":             "docs/engine/route.buzz",
		"./route":           "docs/engine/route.buzz",
		"shared":            "docs/engine/shared.buzz",
		"lib/text":          "docs/lib/text.buzz",
		"hack/lint/support": "hack/lint/support.buzz",
		"../../shared":      "shared.buzz",
		"buzz:route":        "docs/engine/route.buzz",
	}
	for importPath, want := range cases {
		m := ix.resolve(from, importPath)
		require.NotNil(t, m.file, importPath)
		require.Equal(t, want, m.file.rel, importPath)
	}

	for _, importPath := range []string{"fs", "magus/spell/go", "project/libs/coldread", "../../../outside", "buzz:io"} {
		m := ix.resolve(from, importPath)
		require.Nil(t, m.file, importPath)
	}
	require.Equal(t, "io", ix.resolve(from, "buzz:io").path)
}

func TestBindingName(t *testing.T) {
	require.Equal(t, "go", bindingName("magus/spell/go"))
	require.Equal(t, "helpers", bindingName("./hack/policy/helpers"))
	require.Equal(t, "io", bindingName("buzz:io"))
	require.Equal(t, "fs", bindingName("fs"))
}
