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

// TestResolveFollowsMagusSearchPaths pins the templates magus gives a magusfile's
// session (internal/interp magusSearchPaths), tried at the project root and then
// at the workspace root, after the importing file's own directory. Like
// gopherbuzz, `.buzz` is always appended, so `x.buzz` names x.buzz.buzz.
func TestResolveFollowsMagusSearchPaths(t *testing.T) {
	ws := t.TempDir()
	writeFiles(t, ws,
		"magus.yaml",
		"docs/magusfile.buzz",
		"docs/widgets/main.buzz",
		"docs/kit/src/main.buzz",
		"docs/tool/src/tool.buzz",
		"docs/magusfiles/helpers.buzz",
		"magusfiles/rootonly.buzz",
		"docs/both.buzz",
		"docs/both/main.buzz",
		"docs/magusfiles/order.buzz",
		"order.buzz",
		"docs/named.buzz.buzz",
		"docs/plain.buzz",
	)
	project := filepath.Join(ws, "docs")
	ix := &indexer{project: project, workspace: ws, files: map[string]*file{}}
	from := ix.load(filepath.Join(project, "magusfile.buzz"))
	require.NotNil(t, from)

	cases := map[string]string{
		"widgets":    "docs/widgets/main.buzz",
		"kit":        "docs/kit/src/main.buzz",
		"tool":       "docs/tool/src/tool.buzz",
		"helpers":    "docs/magusfiles/helpers.buzz",
		"rootonly":   "magusfiles/rootonly.buzz",
		"both":       "docs/both.buzz",
		"order":      "docs/magusfiles/order.buzz",
		"named.buzz": "docs/named.buzz.buzz",
	}
	for importPath, want := range cases {
		m := ix.resolve(from, importPath)
		require.NotNil(t, m.file, importPath)
		require.Equal(t, want, m.file.rel, importPath)
	}
	require.Nil(t, ix.resolve(from, "plain.buzz").file, "plain.buzz.buzz does not exist")
}

func TestBindingName(t *testing.T) {
	require.Equal(t, "go", bindingName("magus/spell/go"))
	require.Equal(t, "helpers", bindingName("./hack/policy/helpers"))
	require.Equal(t, "io", bindingName("buzz:io"))
	require.Equal(t, "fs", bindingName("fs"))
}
