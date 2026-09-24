package magus

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// sourceCheckout is a checkout of module holding a binary named magus at its root.
func sourceCheckout(t *testing.T, module string) (root, exe string) {
	t.Helper()
	root = t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("module "+module+"\n\ngo 1.26\n"), 0o644))
	exe = filepath.Join(root, "magus")
	require.NoError(t, os.WriteFile(exe, []byte("binary"), 0o755))
	return root, exe
}

func builtFrom(module, pkg string) *debug.BuildInfo {
	return &debug.BuildInfo{Path: module + "/" + pkg, Main: debug.Module{Path: module}}
}

func TestOwnBuildEscapeNamesTheLinkForAnOwnSourceBuild(t *testing.T) {
	root, exe := sourceCheckout(t, "example.com/tool")
	assert.Equal(t, "This binary was built from this checkout's own sources and predates them, so it cannot "+
		"rebuild itself: every command it runs loads the same tree. Link a new one from source, one "+
		"command at a time: `mv magus magus.old`, then `go build -o magus ./cmd/tool`, then rebuild it the "+
		"way this workspace builds it. If that link fails with `undefined:` in generated code, the "+
		"checkout's committed generated files are behind its sources (a merge kept one side): restore "+
		"them from the revision that generated them, then link again.",
		ownBuildEscape(exe, builtFrom("example.com/tool", "cmd/tool"), root))
}

// A release on PATH, a release vendored at the root of a workspace of another module, and a
// binary outside the root all have somewhere else to come from, so the escape stays out.
func TestOwnBuildEscapeIsSilentForAnyOtherBinary(t *testing.T) {
	root, exe := sourceCheckout(t, "example.com/app")
	other, otherExe := sourceCheckout(t, "example.com/tool")
	info := builtFrom("example.com/tool", "cmd/tool")

	assert.Empty(t, ownBuildEscape(exe, info, root), "a module other than the workspace's")
	assert.Empty(t, ownBuildEscape(otherExe, info, root), "a binary outside the root")
	assert.Empty(t, ownBuildEscape(otherExe, &debug.BuildInfo{Path: "command-line-arguments"}, other), "no main module")
	assert.Empty(t, ownBuildEscape(otherExe, nil, other), "no build info")
}

func TestWithOwnBuildEscapeLeavesOtherErrorsAlone(t *testing.T) {
	plain := errors.New("magusfile: syntax error")
	assert.Equal(t, plain, withOwnBuildEscape(plain, t.TempDir()))

	other := types.DiagnosticErrorf(types.NoWorkspaceRoot, "no root")
	assert.Equal(t, error(other), withOwnBuildEscape(other, t.TempDir()))

	// The test binary does not sit at the root of a checkout of its module, so even an
	// MGS1021 passes through untouched.
	stale := types.DiagnosticErrorf(types.WorkspaceNeedsNewerMagus, "out of date")
	assert.Equal(t, error(stale), withOwnBuildEscape(stale, t.TempDir()))
}
