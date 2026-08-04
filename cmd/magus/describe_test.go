package main

import (
	"context"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// TestDescribeWorkspacesOutput_MultiDeclared verifies that `describe workspaces`
// enumerates every declared daemon workspace, not just the active one.
func TestDescribeWorkspacesOutput_MultiDeclared(t *testing.T) {
	base := t.TempDir()
	mkWorkspace := func(name string) string {
		dir := filepath.Join(base, name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		// An empty magusfile.buzz marks the directory as a workspace root.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
		return dir
	}
	wsA, wsB := mkWorkspace("a"), mkWorkspace("b")

	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	globalCfg.Daemon.Workspaces = []string{wsA, wsB}

	out, err := describeWorkspacesOutput(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, out, 2)
	assert.NotEqual(t, out[1].Root, out[0].Root, "expected two distinct workspace roots")
}

// runDispatch drives startup+dispatchSub the same way runCLI does, so a test
// can invoke a real subcommand (including its flag parsing and --help) without
// going through os.Args or os.Exit. Not safe for parallel tests: it mutates
// the package's sync.Once-backed startup singletons via resetStartupSingletons.
//
// Isolates daemon-socket discovery from the host the same way
// TestStartupNoArgsReturnsExitZero does: clearing MAGUS_DAEMON_SOCKET alone is
// not enough, since startup still scans proc.SockDir() for the stable daemon
// socket, and a real `magus server start` daemon on the developer's machine
// would otherwise adopt this call and return ITS result instead.
func runDispatch(t *testing.T, args []string) error {
	t.Helper()
	t.Setenv("MAGUS_DAEMON_SOCKET", "")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	resetStartupSingletons()
	res, exitCode := startup(context.Background(), args)
	t.Cleanup(func() {
		if res.cleanup != nil {
			res.cleanup()
		}
	})
	if exitCode >= 0 {
		return nil
	}
	return dispatchSub(res.rootCtx, res.root, res.rc, res.sub, res.subArgs)
}

// TestDescribeTargetNoun_UnknownNameErrors pins the fix for a name no project
// declares: `magus describe target <name>` used to print a fully-fabricated
// dispatch plan (sources, depends_on, a spell entry per resolved spell) for
// every project, with no error at all. It must error instead, naming the
// target and listing what IS registered.
func TestDescribeTargetNoun_UnknownNameErrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
	t.Chdir(dir)

	err := runDispatch(t, []string{"describe", "target", "zzz-fake-target-zzz"})
	require.Error(t, err, "describe target zzz-fake-target-zzz: expected an error")
	assert.ErrorContains(t, err, `"zzz-fake-target-zzz"`)
	assert.ErrorContains(t, err, "registered:")
}

// TestDescribeTargetHelp_MatchesGrammar pins that `describe target --help`
// documents the ACTUAL positional grammar (a name, optionally suffixed with
// ":charm[,charm...]", then an optional trailing project) rather than the
// "path:target" pair the parser has never accepted.
func TestDescribeTargetHelp_MatchesGrammar(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
	t.Chdir(dir)

	previous := os.Stderr
	read, write, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = write
	t.Cleanup(func() { os.Stderr = previous })

	dispatchErr := runDispatch(t, []string{"describe", "target", "--help"})

	require.NoError(t, write.Close())
	content, readErr := io.ReadAll(read)
	require.NoError(t, readErr)

	assert.ErrorIs(t, dispatchErr, flag.ErrHelp, "describe target --help should report flag.ErrHelp")
	out := string(content)
	assert.Contains(t, out, "Usage: magus describe target[s] [<name>[:charm[,charm...]]] [<project>] [flags]")
	assert.NotContains(t, out, "path:target", "usage must not document the \"path:target\" form the parser never accepted")
}

// captureStdout redirects os.Stdout for the duration of fn and returns
// everything written to it. Mirrors the os.Stderr capture pattern
// TestDescribeTargetHelp_MatchesGrammar uses, for the text `describeTarget`
// itself writes to os.Stdout via fmt.Printf.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	previous := os.Stdout
	read, write, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = write
	fnErr := fn()
	require.NoError(t, write.Close())
	os.Stdout = previous
	content, readErr := io.ReadAll(read)
	require.NoError(t, readErr)
	return string(content), fnErr
}

// TestDescribeTargetCache_NoPriorEntry verifies `describe target --cache` on a
// target that has never run reports plainly that there is nothing to compare,
// rather than a stack of zero-value diff lines or a crash.
func TestDescribeTargetCache_NoPriorEntry(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}
	write("magusfile.buzz", "")
	write("svc/magusfile.buzz", `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {}
`)
	t.Chdir(root)

	out, err := captureStdout(t, func() error {
		return runDispatch(t, []string{"describe", "target", "build", "svc", "--cache"})
	})
	require.NoError(t, err)
	assert.Contains(t, out, "cache:")
	assert.Contains(t, out, "no prior entry for svc:build; nothing to compare")
}

// TestDescribeTargetCache_ReportsSourceChange verifies the end-to-end path: a
// real run through magus.Open/m.Run snapshots KeyInputs, editing the target's
// magusfile afterward (its only source, absent an explicit
// magus.project(sources=...)) without rebuilding leaves the cache holding the
// PRE-edit entry while the tree now holds different content - exactly the
// "would the next run miss, and why" question F17/F4 asked for - and
// `describe target --cache` names that file as the differing component.
func TestDescribeTargetCache_ReportsSourceChange(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}
	write("magusfile.buzz", "")
	svcMagusfile := filepath.Join(root, "svc", "magusfile.buzz")
	write("svc/magusfile.buzz", `import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {}
`)

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	require.NoError(t, m.Run(ctx, []types.Target{{Path: "svc", Name: "build"}}), "first run")

	// Edit the target's only source (no magus.project(sources=...) is declared,
	// so baseStep's magusfileGlobs is all there is) WITHOUT rebuilding: the
	// cache's stored entry still reflects the pre-edit content.
	require.NoError(t, os.WriteFile(svcMagusfile, []byte(`import "magus";
export fun build(ctx: magus\Context, args: [str]) > void {
    // v2
}
`), 0o644))

	t.Chdir(root)
	out, err := captureStdout(t, func() error {
		return runDispatch(t, []string{"describe", "target", "build", "svc", "--cache"})
	})
	require.NoError(t, err)
	assert.Contains(t, out, "src changed: svc/magusfile.buzz", "diff must name the edited source file")
}
