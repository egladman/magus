package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// normalizeRoot lays down the one file every shape below names, so the existence-gated
// readings (a leading "/" that is not a real path, a backslash spelling) have something
// to land on.
func normalizeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "console"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "console", "magusfile.buzz"), []byte("x"), 0o644))
	return root
}

func TestNormalizeWorkspacePathShapes(t *testing.T) {
	root := normalizeRoot(t)
	const want = "console/magusfile.buzz"

	for _, tc := range []struct {
		name  string
		input string
	}{
		{"bare relative", "console/magusfile.buzz"},
		{"dot relative", "./console/magusfile.buzz"},
		{"root anchored", "/console/magusfile.buzz"},
		{"absolute under root", filepath.Join(root, "console", "magusfile.buzz")},
		{"backslash", `console\magusfile.buzz`},
		{"interior dotdot", "console/nested/../magusfile.buzz"},
		{"redundant slashes", "console//magusfile.buzz"},
		{"trailing dot segment", "./console/./magusfile.buzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NormalizeWorkspacePath(tc.input, root)
			assert.True(t, ok, "%q should normalize", tc.input)
			assert.Equal(t, want, got)
		})
	}
}

// Nothing here may come back rewritten. Some of it is not path-shaped at all; the rest
// is shaped like a path magus cannot place inside the workspace, and a rewrite would
// name a different file than the one the user meant.
func TestNormalizeWorkspacePathLeavesTheseAlone(t *testing.T) {
	root := normalizeRoot(t)

	for _, tc := range []struct {
		name  string
		input string
	}{
		{"bare word", "hint"},
		{"bare filename", "guard_shell.go"},
		{"empty", ""},
		{"url", "https://example.com/a/b"},
		{"regex fragment", `console/.*\.buzz$`},
		{"buzz namespace", `magus\project`},
		{"backslash beside a slash", `a/b\c`},
		{"escapes the workspace", "../sibling/file.go"},
		{"absolute outside the workspace", "/somewhere/else/console/magusfile.buzz"},
		{"root anchored but absent", "/console/nosuchfile.buzz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := NormalizeWorkspacePath(tc.input, root)
			assert.Equal(t, tc.input, got)
		})
	}
}

// The half of the table above where ok must also be false, because "leave it alone" is
// the DECISION and not just an incidentally unchanged clean: a caller that treats these
// as understood paths would report an out-of-workspace file as absent from this one.
func TestNormalizeWorkspacePathRefusesOutOfWorkspace(t *testing.T) {
	root := normalizeRoot(t)

	for _, in := range []string{
		"hint",
		"guard_shell.go",
		"",
		"https://example.com/a/b",
		`magus\project`,
		"../sibling/file.go",
		"/somewhere/else/console/magusfile.buzz",
		"/console/nosuchfile.buzz",
	} {
		_, ok := NormalizeWorkspacePath(in, root)
		assert.Falsef(t, ok, "%q must not be reported as a workspace path", in)
	}
}

// An absolute path under root resolves on the path alone. Requiring it to exist would
// make a query about a file the graph still remembers but the tree no longer has - a
// deleted file, a stale index - answer as if the path had never been understood.
func TestNormalizeWorkspacePathAbsoluteNeedsNoFile(t *testing.T) {
	root := normalizeRoot(t)
	got, ok := NormalizeWorkspacePath(filepath.Join(root, "gone", "deleted.go"), root)
	assert.True(t, ok)
	assert.Equal(t, "gone/deleted.go", got)
}

// Without a root only the shapes that need no workspace on disk resolve; the rest are
// left alone rather than guessed at.
func TestNormalizeWorkspacePathWithoutRoot(t *testing.T) {
	got, ok := NormalizeWorkspacePath("./console/magusfile.buzz", "")
	assert.True(t, ok)
	assert.Equal(t, "console/magusfile.buzz", got)

	for _, in := range []string{"/console/magusfile.buzz", `console\magusfile.buzz`} {
		got, ok := NormalizeWorkspacePath(in, "")
		assert.False(t, ok, "%q needs a root", in)
		assert.Equal(t, in, got)
	}
}

// A Windows drive letter is a path shape on every platform, so the verdict does not
// depend on GOOS - the same reasoning ResolveImport records for its backslashes.
func TestNormalizeWorkspacePathDriveLetter(t *testing.T) {
	root := normalizeRoot(t)
	got, ok := NormalizeWorkspacePath(`C:\console\magusfile.buzz`, root)
	assert.True(t, ok)
	assert.Equal(t, "console/magusfile.buzz", got)

	outside, ok := NormalizeWorkspacePath(`C:\Users\other\repo\main.go`, root)
	assert.False(t, ok)
	assert.Equal(t, `C:\Users\other\repo\main.go`, outside)
}
