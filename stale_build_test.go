package magus

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// sourceCheckout is a checkout of module holding a binary named magus at its root.
func sourceCheckout(t *testing.T, module string) (root, exe string) {
	t.Helper()
	w := testkit.NewWorkspace(t)
	w.Write("go.mod", fmt.Sprintf(`module %s

go 1.26
`, module))
	return w.Root(), w.WriteExecutable("magus", "binary")
}

func moduleBuildInfo(module, pkg string) *debug.BuildInfo {
	return &debug.BuildInfo{Path: fmt.Sprintf("%s/%s", module, pkg), Main: debug.Module{Path: module}}
}

func TestOwnBuildEscapeNamesTheLinkForAnOwnSourceBuild(t *testing.T) {
	root, exe := sourceCheckout(t, "example.com/tool")
	want := fmt.Sprintf("This binary was built from this checkout's own sources and predates them, so it cannot "+
		"rebuild itself: every command it runs loads the same tree. Link a new one from source, one "+
		"command at a time: `go -C %s build -o magus ./cmd/tool`, then rebuild it the "+
		"way this workspace builds it. If that link fails with `undefined:` in generated code, the "+
		"checkout's committed generated files are behind its sources (a merge kept one side), and "+
		"restoring either side's copy brings back the mismatch: regenerate them with the commands its "+
		"generate targets run, then link again. No script in this workspace declares SERVES "+
		"\"code:MGS1021\", so magus names none of its own.", root)
	assert.Equal(t, want,
		ownBuildEscape(exe, moduleBuildInfo("example.com/tool", "cmd/tool"), root))
}

// magus's own checkout gets nothing a stranger's would not: its recovery is the script it
// declares for MGS1021, never a path compiled into the binary.
func TestOwnBuildEscapeOfMagusNamesNoScriptItDoesNotServe(t *testing.T) {
	root, exe := sourceCheckout(t, ownModule)
	escape := ownBuildEscape(exe, moduleBuildInfo(ownModule, "cmd/magus"), root)
	assert.NotContains(t, escape, "bootstrap-checkout.buzz")
	assert.NotContains(t, escape, "go generate ./cmd/magus-utils")
	assert.Contains(t, escape, "No script in this workspace declares SERVES")
}

func TestOwnBuildEscapeNamesTheScriptTheWorkspaceServes(t *testing.T) {
	root, exe := sourceCheckout(t, "example.com/tool")
	require.NoError(t, os.MkdirAll(filepath.Join(root, "hack"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "hack", "relink.buzz"), []byte("// relinks\n"), 0o644))
	require.NoError(t, hint.WriteScriptsIndex(config.CacheDir(root, config.Config{}), hint.ScriptsIndex{
		Root: root,
		Serves: map[string][]hint.ScriptServe{"code:MGS1021": {{
			Path: "hack/relink.buzz", Args: []string{"--checkout", "{checkout}"}, Effect: hint.ScriptEffectWrite, Summary: "relink",
		}}},
	}))
	escape := ownBuildEscape(exe, moduleBuildInfo("example.com/tool", "cmd/tool"), root)
	assert.Contains(t, escape, "This workspace's script for it: `")
	assert.Contains(t, escape, "hack/relink.buzz -- --checkout "+shellQuoted(root))
}

func TestOwnBuildEscapeNamesTheCheckoutHookAfterTheLink(t *testing.T) {
	root, exe := sourceCheckout(t, "example.com/tool")
	require.NoError(t, os.WriteFile(filepath.Join(root, config.Filename), []byte("checkout:\n  bootstrap: hack/boot.buzz\n"), 0o644))
	escape := ownBuildEscape(exe, moduleBuildInfo("example.com/tool", "cmd/tool"), root)
	assert.Contains(t, escape, "Then ready the checkout, alone on its line: `./magus buzz hack/boot.buzz -- --here --verify-ready`.")
}

// shellQuoted is path as one POSIX shell word, the way a remedy line names a checkout.
func shellQuoted(path string) string {
	q, _ := syntax.Quote(path, syntax.LangPOSIX) // only a NUL byte fails, and no temp dir holds one
	return q
}

// A checkout path the shell would split or expand is quoted once, so the line pasted builds
// that checkout.
func TestOwnBuildEscapeQuotesTheCheckout(t *testing.T) {
	root, exe := sourceCheckout(t, "example.com/tool")
	quoted := filepath.Join(t.TempDir(), "landing $cache 'quoted'")
	require.NoError(t, os.Rename(root, quoted))
	escape := ownBuildEscape(filepath.Join(quoted, filepath.Base(exe)), moduleBuildInfo("example.com/tool", "cmd/tool"), quoted)
	resolved, err := filepath.EvalSymlinks(quoted)
	require.NoError(t, err)
	words := shellQuoted(resolved)
	assert.Contains(t, words, `\$cache`, "the $ stays literal")
	assert.Contains(t, escape, fmt.Sprintf("go -C %s build -o magus ./cmd/tool", words))
}

// A release on PATH, a release vendored at the root of a workspace of another module, and a
// binary outside the root all have somewhere else to come from, so the escape stays out.
func TestOwnBuildEscapeIsSilentForAnyOtherBinary(t *testing.T) {
	root, exe := sourceCheckout(t, "example.com/app")
	other, otherExe := sourceCheckout(t, "example.com/tool")
	info := moduleBuildInfo("example.com/tool", "cmd/tool")

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
