package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus"
	sandboxapply "github.com/egladman/magus/internal/sandbox/apply"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBuzzCmd_UnusedImportWarnsOnStderrAndExitsClean drives the actual `magus buzz`
// code path (buzzCmd, the same function main.go dispatches to) end-to-end: a script
// with an unused import must print the BZZ3001 warning to stderr and still exit
// clean (nil error, which main.go turns into exit 0): the whole point of a warning
// is that it never fails the run.
func TestBuzzCmd_UnusedImportWarnsOnStderrAndExitsClean(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unused.buzz")
	require.NoError(t, os.WriteFile(path, []byte("import \"fs\";\nvar x = 1;\n"), 0o644))

	prevQuiet, prevSilent := global.quiet, global.silent
	global.quiet, global.silent = false, false
	t.Cleanup(func() { global.quiet, global.silent = prevQuiet, prevSilent })

	var runErr error
	stderr := captureStderr(t, func() {
		stdout := captureStdout(t, func() {
			runErr = buzzCmd(context.Background(), "", []string{path})
		})
		assert.NotContains(t, stdout, "BZZ3001", "stdout carries structured output only; a warning must never land there")
	})

	require.NoError(t, runErr, "a warning must never fail the run")
	assert.Contains(t, stderr, "BZZ3001")
	assert.Contains(t, stderr, "fs")
	assert.Contains(t, stderr, "warning:")
}

// TestBuzzCmd_SilentSuppressesUnusedImportWarning verifies -s/--silent, the flag
// this command's own progress/error output already respects, also gates the new
// warning line: a silent run should stay silent.
func TestBuzzCmd_SilentSuppressesUnusedImportWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unused.buzz")
	require.NoError(t, os.WriteFile(path, []byte("import \"fs\";\nvar x = 1;\n"), 0o644))

	prevSilent := global.silent
	t.Cleanup(func() { global.silent = prevSilent })

	var runErr error
	stderr := captureStderr(t, func() {
		runErr = buzzCmd(context.Background(), "", []string{"-s", path})
	})

	require.NoError(t, runErr)
	assert.NotContains(t, stderr, "BZZ3001", "-s/--silent must suppress the warning line")
}

// buzzSandboxTarget is what the fixture script removes. It has to sit outside $TMPDIR as
// well as outside the workspace, because the default policy grants read+write on both, so
// a t.TempDir() would be allowed and the test would pass with no policy attached at all.
// Nothing is created there: checkWrite runs before the removal, so the deny fires on a
// path that does not exist, and a permitted run removes nothing.
const buzzSandboxTarget = "/magus-buzz-sandbox-test/victim"

// buzzSandboxWorkspace builds a workspace whose magus.yaml sets the sandbox as asked,
// opens it, and returns it on the context loadMagus reads so buzzCmd never touches the
// process singleton.
func buzzSandboxWorkspace(t *testing.T, sandboxEnabled bool) (context.Context, *magus.Magus, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"magus\";\n\nmagus.project({})\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"),
		fmt.Appendf(nil, "sandbox:\n  enabled: %t\n", sandboxEnabled), 0o644))

	m, err := magus.Open(t.Context(), root)
	require.NoError(t, err, "fixture workspace must open")
	t.Cleanup(func() { _ = m.Close() })

	script := filepath.Join(root, "wipe.buzz")
	require.NoError(t, os.WriteFile(script,
		fmt.Appendf(nil, "import \"fs\";\n\nfun main(args: [str]) > void !> any {\n    fs\\removeAll(\"%s\");\n}\n", buzzSandboxTarget), 0o644))

	return withMagus(t.Context(), m), m, script
}

// TestBuzzCmd_ScriptRunsUnderTheWorkspaceSandbox pins the reason `magus buzz` is not a
// hole in the sandbox. The agent guard allows `magus buzz -` outright and cannot read a
// script body, so a script that magus never sandboxed was an unrestricted fs/proc/network
// surface in a workspace that had asked for one.
//
// MarkAppliedExternally puts the apply layer in its attach-only mode: landlock is
// permanent and process-wide, so applying it here would confine every later test in this
// binary. The policy still reaches ctx, which is what the binding checks read.
func TestBuzzCmd_ScriptRunsUnderTheWorkspaceSandbox(t *testing.T) {
	sandboxapply.MarkAppliedExternally("magus buzz sandbox test")
	ctx, m, script := buzzSandboxWorkspace(t, true)

	err := buzzCmd(ctx, "", []string{"-s", script})

	require.Error(t, err, "a write outside the workspace must be refused")
	assert.ErrorContains(t, err, string(types.PathWriteDenied))

	events, rerr := trail.ReadRecent(m.CacheDir(), 10)
	require.NoError(t, rerr)
	require.Len(t, events, 1, "the refusal must be readable on the trail, as a target's is")
	assert.Equal(t, trail.KindSandboxDenial, events[0].Kind)
}

// TestBuzzCmd_SandboxDisabledLeavesTheScriptUnrestricted holds the other half: the
// sandbox is off by default, and a script in a workspace that never asked for one keeps
// writing wherever it could before.
func TestBuzzCmd_SandboxDisabledLeavesTheScriptUnrestricted(t *testing.T) {
	ctx, m, script := buzzSandboxWorkspace(t, false)

	require.NoError(t, buzzCmd(ctx, "", []string{"-s", script}))

	events, rerr := trail.ReadRecent(m.CacheDir(), 10)
	require.NoError(t, rerr)
	assert.Empty(t, events, "nothing was refused, so nothing belongs on the trail")
}

const (
	buzzVanillaScript = "import \"std\";\n\nfun main(args: [str]) > void {\n    std\\print(\"hi\");\n}\n"
	buzzMemberScript  = "import \"std\";\nimport \"magus\";\n\nfun main(args: [str]) > void !> any {\n    final p = magus\\projects();\n    std\\print(\"{p.projects.len()}\");\n}\n"
)

// countWorkspaceOpens swaps buzzLoadWorkspace for open, counting its calls.
func countWorkspaceOpens(t *testing.T, open func(context.Context, string) (*magus.Magus, error)) *int {
	t.Helper()
	prev := buzzLoadWorkspace
	t.Cleanup(func() { buzzLoadWorkspace = prev })
	opens := 0
	buzzLoadWorkspace = func(ctx context.Context, root string, _ ...magus.Option) (*magus.Magus, error) {
		opens++
		return open(ctx, root)
	}
	return &opens
}

// buzzLazyWorkspace writes a one-project workspace holding script and returns its root
// and the script path.
func buzzLazyWorkspace(t *testing.T, script string) (string, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"magus\";\n\nmagus.project({})\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("sandbox:\n  enabled: false\n"), 0o644))
	path := filepath.Join(root, "script.buzz")
	require.NoError(t, os.WriteFile(path, []byte(script), 0o644))
	return root, path
}

func TestBuzzCmd_VanillaScriptDoesNotOpenTheWorkspace(t *testing.T) {
	root, script := buzzLazyWorkspace(t, buzzVanillaScript)
	opens := countWorkspaceOpens(t, func(ctx context.Context, _ string) (*magus.Magus, error) {
		return magus.Open(ctx, root)
	})

	var runErr error
	stdout := captureStdout(t, func() { runErr = buzzCmd(t.Context(), root, []string{"-s", script}) })

	require.NoError(t, runErr)
	assert.Equal(t, "hi\n", stdout)
	assert.Equal(t, 0, *opens, "a script that reads no workspace member must not pay for opening one")
}

func TestBuzzCmd_WorkspaceMemberOpensTheWorkspaceOnce(t *testing.T) {
	root, script := buzzLazyWorkspace(t, buzzMemberScript)
	var m *magus.Magus
	opens := countWorkspaceOpens(t, func(ctx context.Context, _ string) (*magus.Magus, error) {
		var err error
		m, err = magus.Open(ctx, root)
		return m, err
	})
	t.Cleanup(func() {
		if m != nil {
			_ = m.Close()
		}
	})

	var runErr error
	stdout := captureStdout(t, func() { runErr = buzzCmd(t.Context(), root, []string{"-s", script}) })

	require.NoError(t, runErr)
	assert.Equal(t, "1\n", stdout)
	assert.Equal(t, 1, *opens)
}

// guardGlueScripts are the hook scripts a host wires to `magus buzz`, the highest-rate
// callers `magus buzz` has: one runs on every shell command, every write and every file
// an agent opens.
//
// The last two run once per session rather than once per tool call, so the budget
// argument alone would excuse them. They are held to the same rule anyway: the
// exemption is what erodes, and a session-start hook that opens the workspace pays its
// 700ms at the one moment a person is watching the model come back.
var guardGlueScripts = []string{
	"magus-hook-command.buzz",
	"magus-hook-path.buzz",
	"magus-hook-observe.buzz",
	"magus-checkpoint.buzz",
	"magus-rehydrate.buzz",
}

// withStdin feeds text to the process's standard input for the duration of fn, which
// is what lets a hook script be driven the way its host drives it.
func withStdin(t *testing.T, text string, fn func()) {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	go func() {
		_, _ = w.WriteString(text)
		_ = w.Close()
	}()
	prev := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = prev })
	fn()
}

// TestBuzzCmd_GuardGlueNeverOpensTheWorkspace is the load-bearing half of moving the
// hook glue off POSIX sh.
//
// These scripts run on every tool call, so the only budget they have is the one a
// closed workspace buys: about 10ms here against roughly 700ms for an open. An
// `import "magus"` or a spell import added to any of them would be invisible in every
// other test, because the reply would stay byte-identical and only the clock would
// move. This counts the opens instead.
//
// __MAGUS_BIN names a path that does not exist, so the scripts take their
// magus-is-unavailable arm and judge nothing: what is under test is which modules the
// glue reaches for, not the verdict, which guard_templates.txtar executes for real.
//
// Read this with TestGuardGlueImportsNoWorkspaceReader, and delete neither as a
// duplicate of the other. The open is LAZY, so this one only moves once a call reaches
// through the import; that makes this the test that catches the incident and its
// sibling the test that catches the cause.
func TestBuzzCmd_GuardGlueNeverOpensTheWorkspace(t *testing.T) {
	const event = `{"session_id":"s1","tool_name":"Bash","tool_input":{"command":"git stash"}}`
	for _, name := range guardGlueScripts {
		t.Run(name, func(t *testing.T) {
			root, _ := buzzLazyWorkspace(t, buzzVanillaScript)
			opens := countWorkspaceOpens(t, func(ctx context.Context, _ string) (*magus.Magus, error) {
				return magus.Open(ctx, root)
			})
			script, err := filepath.Abs(filepath.Join("..", "..", "docs", "guides", "integrations", "agents", name))
			require.NoError(t, err)
			require.FileExists(t, script, "the shipped glue must be where the harness wires it")
			t.Setenv("__MAGUS_BIN", filepath.Join(t.TempDir(), "absent-magus"))
			t.Setenv("TMPDIR", t.TempDir())

			var runErr error
			withStdin(t, event, func() {
				captureStdout(t, func() { runErr = buzzCmd(t.Context(), root, []string{"-s", script}) })
			})

			require.NoError(t, runErr, "a hook script must never fail: its host reads a non-zero exit as an error notice, and exit 2 as a block")
			assert.Equal(t, 0, *opens,
				"%s opened the workspace. A hook runs on every tool call, so it may import no magus\n"+
					"module and no spell: either one pays the workspace open the lazy start exists to avoid.", name)
		})
	}
}

// TestGuardGlueImportsNoWorkspaceReader is the structural half of the gate above.
//
// The open is LAZY, so an unused `import "magus"` costs nothing and
// TestBuzzCmd_GuardGlueNeverOpensTheWorkspace stays green until someone calls through
// it. That makes the import the early warning and the call the incident: by the time
// the count moves, the line that pays for it is already written. Refusing the import
// refuses the call before it exists, which is why the two tests are a pair and neither
// is a duplicate of the other.
func TestGuardGlueImportsNoWorkspaceReader(t *testing.T) {
	for _, name := range guardGlueScripts {
		body, err := os.ReadFile(filepath.Join("..", "..", "docs", "guides", "integrations", "agents", name))
		require.NoError(t, err, "read %s", name)
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if !strings.HasPrefix(trimmed, "import ") {
				continue
			}
			assert.NotRegexp(t, `^import "(magus|spells/)`, trimmed,
				"%s:%d imports a workspace reader. A hook runs on every tool call and may not pay for\n"+
					"opening the workspace; reach the binary with proc\\exec instead, the way the glue\n"+
					"resolves its verdict today.", name, i+1)
		}
	}
}

// TestBuzzCmd_OutsideAWorkspace pins both halves outside any workspace: a script that
// never reads one is not warned about it, and a workspace member still raises MGS1022
// with the reason it could not attach.
func TestBuzzCmd_OutsideAWorkspace(t *testing.T) {
	// buzzCmd's flag parse rebuilds the default logger on whatever os.Stderr is then,
	// which is the only reason captureStderr sees the warning. The text handler writes
	// synchronously; the pretty one owns a terminal region.
	prevLog, prevFormat := slog.Default(), globalCfg.Log.Format
	prevQuiet, prevSilent := global.quiet, global.silent
	globalCfg.Log.Format = "text"
	global.quiet, global.silent = false, false
	t.Cleanup(func() {
		slog.SetDefault(prevLog)
		globalCfg.Log.Format = prevFormat
		global.quiet, global.silent = prevQuiet, prevSilent
	})
	noWorkspace := errors.New("no magus workspace found")
	opens := countWorkspaceOpens(t, func(context.Context, string) (*magus.Magus, error) { return nil, noWorkspace })
	dir := t.TempDir()
	vanilla := filepath.Join(dir, "vanilla.buzz")
	require.NoError(t, os.WriteFile(vanilla, []byte(buzzVanillaScript), 0o644))
	member := filepath.Join(dir, "member.buzz")
	require.NoError(t, os.WriteFile(member, []byte(buzzMemberScript), 0o644))

	var runErr error
	stderr := captureStderr(t, func() {
		captureStdout(t, func() { runErr = buzzCmd(t.Context(), dir, []string{vanilla}) })
	})
	require.NoError(t, runErr)
	assert.Equal(t, 0, *opens)
	assert.NotContains(t, stderr, "workspace not attached", "a script that never asked for a workspace is not warned about one")

	stderr = captureStderr(t, func() { runErr = buzzCmd(t.Context(), dir, []string{member}) })
	require.Error(t, runErr)
	assert.ErrorContains(t, runErr, string(types.MagusfileOnlyMember))
	assert.Contains(t, stderr, "workspace not attached")
	assert.Contains(t, stderr, noWorkspace.Error())
}

// TestBuzzCmd_ScriptArgvReachesMain pins the three shapes a caller has for handing a
// script its own argv, which is what lets one file serve several callers without an
// environment variable a shell has to set. The `--` form is the one that matters:
// cmdParse reorders flags ahead of positionals, so a bare `script.buzz --raw` is
// magus's flag to parse and fails, and the separator is what hands it to the script.
func TestBuzzCmd_ScriptArgvReachesMain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "argv.buzz")
	require.NoError(t, os.WriteFile(path, []byte(
		"import \"std\";\nexport fun main(args: [str]) > void { std\\print(\"argv={args}\"); }\n"), 0o644))

	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"after a separator":   {[]string{path, "--", "--raw", "-x"}, "argv=[--raw, -x]"},
		"bare words":          {[]string{path, "one", "two"}, "argv=[one, two]"},
		"none":                {[]string{path}, "argv=[]"},
		"separator with none": {[]string{path, "--"}, "argv=[]"},
	} {
		t.Run(name, func(t *testing.T) {
			var runErr error
			stdout := captureStdout(t, func() {
				runErr = buzzCmd(context.Background(), "", tc.args)
			})
			require.NoError(t, runErr)
			assert.Contains(t, stdout, tc.want)
		})
	}
}
