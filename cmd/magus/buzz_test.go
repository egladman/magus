package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"text/template"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/interp/bindings"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/types"
	"github.com/google/jsonschema-go/jsonschema"
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

// buzzSandboxTarget is what the fixture script removes. It sits outside the workspace
// and every other write grant, so a permitted run would reach it. Nothing is created
// there: checkWrite runs before the removal, so the deny fires on a path that does not
// exist, and a permitted run removes nothing.
const buzzSandboxTarget = "/magus-buzz-sandbox-test/victim"

// buzzSandboxWorkspace builds a workspace whose magus.yaml sets the sandbox as asked,
// opens it, and returns it on the context loadMagus reads so buzzCmd never touches the
// process singleton.
func buzzSandboxWorkspace(t *testing.T, mode types.SandboxMode) (context.Context, *magus.Magus, string) {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"),
		[]byte("import \"magus\";\n\nmagus\\project({})\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"),
		fmt.Appendf(nil, "sandbox:\n  mode: %s\n", mode), 0o644))

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
// surface in a workspace that had asked for one. The script runs in this process, which
// the sandbox never confines, so the binding check is the whole defense here.
func TestBuzzCmd_ScriptRunsUnderTheWorkspaceSandbox(t *testing.T) {
	ctx, m, script := buzzSandboxWorkspace(t, types.SandboxModeBestEffort)

	err := buzzCmd(ctx, "", []string{"-s", script})

	require.Error(t, err, "a write outside the workspace must be refused")
	assert.ErrorContains(t, err, string(types.PathWriteDenied))

	events, rerr := trail.ReadRecent(m.CacheDir(), 10)
	require.NoError(t, rerr)
	require.Len(t, events, 1, "the refusal must be readable on the trail, as a target's is")
	assert.Equal(t, trail.KindSandboxDenial, events[0].Kind)
}

// TestBuzzScriptContextRefusesAFailedLoadUnderTheSandbox: a workspace that fails to load
// has no policy to apply, and the script used to run anyway, under none. Breaking the
// magusfile was a way out of the sandbox.
func TestBuzzScriptContextRefusesAFailedLoadUnderTheSandbox(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	opens := countWorkspaceOpens(t, func(context.Context, string) (*magus.Magus, error) {
		return nil, errors.New("magusfile.buzz:1: broken on purpose")
	})

	globalCfg.Sandbox.Mode = types.SandboxModeBestEffort
	_, err := buzzScriptContext(t.Context(), t.TempDir())
	require.ErrorIs(t, err, types.WorkspaceLoadFailed)
	assert.ErrorContains(t, err, "broken on purpose")
	assert.Equal(t, 1, *opens)

	// Adopted, so the open is attempted with the sandbox off too.
	globalCfg.Sandbox.Mode = types.SandboxModeOff
	_, err = buzzScriptContext(withMagus(t.Context(), nil), t.TempDir())
	require.NoError(t, err, "with the sandbox off a failed load still only warns")
	assert.Equal(t, 2, *opens)
}

// TestBuzzCmd_SandboxDisabledLeavesTheScriptUnrestricted holds the other half: the
// sandbox is off by default, and a script in a workspace that never asked for one keeps
// writing wherever it could before.
func TestBuzzCmd_SandboxDisabledLeavesTheScriptUnrestricted(t *testing.T) {
	ctx, m, script := buzzSandboxWorkspace(t, types.SandboxModeOff)

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
		[]byte("import \"magus\";\n\nmagus\\project({})\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("sandbox:\n  mode: off\n"), 0o644))
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
	"magus-command.buzz",
	"magus-path.buzz",
	"magus-observe.buzz",
	"magus-checkpoint.buzz",
	"magus-rehydrate.buzz",
	"cursor-hook.buzz",
}

// guardGlueModules are files the ports import. They are not hooks, so the
// open-count test does not run them, but they are held to the same import rule:
// a shared module that opened the workspace would put the cost back on every call.
var guardGlueModules = []string{
	"lib/hook.buzz",
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
	names := append(append([]string{}, guardGlueScripts...), guardGlueModules...)
	for _, name := range names {
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
	prevLog, prevFormat, prevLevel := slog.Default(), globalCfg.Log.Format, globalCfg.Log.Level
	prevQuiet, prevSilent := global.quiet, global.silent
	globalCfg.Log.Format, globalCfg.Log.Level = "text", "info"
	global.quiet, global.silent = false, false
	t.Cleanup(func() {
		slog.SetDefault(prevLog)
		globalCfg.Log.Format, globalCfg.Log.Level = prevFormat, prevLevel
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

func TestBuzzCmd_ScriptImportsResolveBesideTheFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.buzz"), []byte(
		"export fun answer() > str { return \"ok\"; }\n"), 0o644))
	script := filepath.Join(dir, "main.buzz")
	require.NoError(t, os.WriteFile(script, []byte(
		"import \"std\";\nimport \"lib\";\nfun main() > void { std\\print(answer()); }\n"), 0o644))

	var runErr error
	stdout := captureStdout(t, func() {
		runErr = buzzCmd(context.Background(), "", []string{script})
	})
	require.NoError(t, runErr)
	assert.Contains(t, stdout, "ok")
}

// --root names the checkout a script's vcs calls read. The process cwd here is the
// magus checkout this test runs in, a different repository on a different ref.
func TestBuzzCmd_RootSelectsTheVCS(t *testing.T) {
	repo := initGitRepo(t)
	runGit(t, repo, "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "init")
	runGit(t, repo, "switch", "-q", "-c", "split-target")
	script := filepath.Join(t.TempDir(), "ref.buzz")
	require.NoError(t, os.WriteFile(script, []byte(
		"import \"std\";\nimport \"vcs\";\n\nfun main(args: [str]) > void !> any {\n    std\\print(vcs\\ref());\n}\n"), 0o644))

	var runErr error
	stdout := captureStdout(t, func() { runErr = buzzCmd(t.Context(), repo, []string{"-s", script}) })

	require.NoError(t, runErr)
	assert.Equal(t, "split-target\n", stdout)
}

// Strict mode refuses a raising call at the top level, and the refusal names the form
// that runs one, which an -e snippet takes as well as a file does.
func TestBuzzCmd_TopLevelRaiseNamesTheMainForm(t *testing.T) {
	var runErr error
	captureStdout(t, func() {
		runErr = buzzCmd(t.Context(), "", []string{"-s", "-e", `import "vcs"; final r = vcs\root();`})
	})
	require.ErrorContains(t, runErr, string(buzz.UnhandledRaise))
	assert.ErrorContains(t, runErr, "fun main(args: [str]) > void !> any")

	stdout := captureStdout(t, func() {
		runErr = buzzCmd(t.Context(), "", []string{"-s", "-e",
			`import "std"; import "vcs"; fun main(args: [str]) > void !> any { std\print("root {vcs\root() != ""}"); }`})
	})
	require.NoError(t, runErr)
	assert.Equal(t, "root true\n", stdout)
}

// buzzTrace runs buzzCmd under an enabled startup trace wired the way main does it,
// with the trace's profile on ctx.
func buzzTrace(t *testing.T, args []string) (stdout, trace string) {
	t.Helper()
	tr := newStartupTracer(true)
	ctx := buzz.WithProfile(withTrace(context.Background(), tr), tr.profile)
	var runErr error
	stdout = captureStdout(t, func() {
		runErr = buzzCmd(ctx, "", args)
	})
	require.NoError(t, runErr)
	var buf bytes.Buffer
	tr.w = &buf
	tr.done()
	return stdout, buf.String()
}

func TestBuzzCmd_TraceRecordsEachCompilePhaseOnce(t *testing.T) {
	_, trace := buzzTrace(t, []string{"-s", "-e", `import "std"; fun main() > void { std\print("ok"); }`})
	assert.Contains(t, trace, "buzz profile:")
	assert.Regexp(t, `(?m)^ +\S+ +parse$`, trace, "the profile carries the compile phases")
	assert.NotContains(t, trace, "buzz.parse", "a second copy on the startup trace doubles every phase")
}

func TestBuzzCmd_TraceProfilesAClosedScriptWithoutTheNamespace(t *testing.T) {
	_, trace := buzzTrace(t, []string{"-s", "-e", `import "std"; fun main() > void { std\print("ok"); }`})
	assert.Contains(t, trace, "buzz.register_surface")
	assert.Contains(t, trace, "buzz.register_decls")
	assert.Contains(t, trace, "buzz.exec")
	assert.Contains(t, trace, "buzz.main")
	assert.NotContains(t, trace, "buzz.register_namespace")
}

func TestBuzzCmd_TraceProfilesTheNamespaceWhenTheScriptImportsMagus(t *testing.T) {
	stdout, trace := buzzTrace(t, []string{"-s", "-e",
		`import "std"; import "magus"; fun main() > void { std\print(magus\canonicalName("HTTPServer")); }`})
	assert.Contains(t, trace, "buzz.register_namespace")
	assert.Contains(t, stdout, "http-server")
}

func TestBuzzCmd_NestedMagusImportResolvesWithoutAnEntryImport(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.buzz"), []byte(
		"import \"magus\";\nexport fun name() > str { return magus\\canonicalName(\"HTTPServer\"); }\n"), 0o644))
	script := filepath.Join(dir, "main.buzz")
	require.NoError(t, os.WriteFile(script, []byte(
		"import \"std\";\nimport \"lib\";\nfun main() > void { std\\print(name()); }\n"), 0o644))

	stdout, trace := buzzTrace(t, []string{"-s", script})
	assert.Contains(t, stdout, "http-server")
	assert.Contains(t, trace, "buzz.register_namespace")
}

// An aliased import checks against the types declared when it starts, so a file that
// reaches magus only through one needs the mirrors declared up front.
func TestBuzzCmd_AliasedImportReachingMagusSeesTheMirrors(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "lib.buzz"), []byte(`import "magus";
export fun language() > str {
    final opts: magus\DirsOptions = magus\DirsOptions{ language = "go" };
    return opts.language;
}
`), 0o644))
	script := filepath.Join(dir, "main.buzz")
	require.NoError(t, os.WriteFile(script, []byte(
		"import \"std\";\nimport \"lib\" as lib;\nfun main() > void { std\\print(lib\\language()); }\n"), 0o644))

	stdout, trace := buzzTrace(t, []string{"-s", script})
	assert.Contains(t, stdout, "go")
	assert.Contains(t, trace, "buzz.register_namespace")
}

func TestBuzzReachesMagus(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) {
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644))
	}
	write("plain.buzz", `import "std";`)
	write("direct.buzz", `import "magus";`)
	write("lib/deep.buzz", `import "magus/spell";`)
	write("lib/mid.buzz", `import "deep" as deep;`)
	write("cycle.buzz", `import "cycle";`)

	sess := buzz.NewSession(t.Context())
	t.Cleanup(func() { _ = sess.Close() })
	sess.SetIncludeDirs([]string{dir})
	bindings.RegisterModuleSurface(t.Context(), sess)

	for code, want := range map[string]bool{
		`import "std"; import "fs";`:                  false,
		`import "plain" as p;`:                        false,
		`import "missing";`:                           false,
		`import "cycle";`:                             false,
		`import "magus" as m;`:                        true,
		`import print from "buzz:magus";`:             true,
		`import "std"; import "direct";`:              true,
		`import "lib/mid" as mid;`:                    true,
		`import "spells/hello";`:                      true,
		"import \"std\";\nimport \"magus/figure\";\n": true,
	} {
		assert.Equal(t, want, buzzReachesMagus(sess, code), code)
	}
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

// glueHostSchemaDir holds the host schemas the glue's replies are graded against;
// testdata/hosts/SOURCES.md says which a host published and which magus wrote.
// Absolute, and resolved when the package loads, because runGlue changes directory.
var glueHostSchemaDir = glueAbs("../../testdata/hosts")

// glueTemplateDir is the shipped glue, resolved the same way.
var glueTemplateDir = glueAbs("../../docs/guides/integrations/agents")

func glueAbs(rel string) string {
	abs, err := filepath.Abs(rel)
	if err != nil {
		panic(err)
	}
	return abs
}

// glueHostOutputSchema names the schema each host's hook stdout is graded against.
var glueHostOutputSchema = map[string]string{
	"claude-code": "claude-code/hook-output.schema.json",
	"codex":       "codex/pre-tool-use.command.output.schema.json",
}

// glueRawObject matches a JSON object a glue file holds as a raw string constant. Those
// with a template action or a placeholder the glue splices a value into are not replies
// yet; the rest are printed as they stand.
var glueRawObject = regexp.MustCompile("= `(\\{\"[^`]*)`;")

// The reply to Codex's approval request answers its own event, with its own schema.
const (
	gluePermissionRequestEvent  = `"hookEventName":"PermissionRequest"`
	gluePermissionRequestSchema = "codex/permission-request.command.output.schema.json"
)

// glueCoverageHost reads the host out of a glue file's `magus-guard-coverage:` marker, so
// a glue that learns a new host demands that host's schema on the next run.
var glueCoverageHost = regexp.MustCompile(`magus-guard-coverage:.*\bhost=(\S+)`)

// glueVerdicts are the decisions `magus shell` renders, plus one it never does. The values
// are fixed here rather than taken from a run so the rendered bytes belong to the test, and
// the reason carries the characters a naive template would break on. The unknown decision
// stands in for a contract that grew after a copy was installed: it must never render as
// an allow.
var glueVerdicts = []map[string]any{
	{"decision": "deny", "reason": `git stash is denied here: "quoted" & <angled>`},
	{"decision": "advise", "context": "magus workspace: `magus refs <sym>` for code"},
	{"decision": "pass"},
	{"decision": "ask", "reason": `pushing abc1234, which no passing gate covers: "quoted" & <angled>`},
	{"decision": "maybe", "reason": "a decision no template knows"},
	{"decision": "pass", "updated_command": "exec </dev/null; git push"},
	{"decision": "advise", "context": "magus runs your shell commands with stdin at end-of-file; pipe or redirect input explicitly.", "updated_command": "exec </dev/null; git push"},
}

// glueArrangement is one way a host reaches the shared glue: the host its wiring names,
// the environment it sets, the event it sends, and whether a Codex prompt rule sits in
// the project.
type glueArrangement struct {
	label string
	// host is the name the wiring passes as --agent-name, the only place the glue reads
	// one. Every arrangement names one: glue given none never assembles a reply for magus
	// to render, because magus refuses it (MGS3024), which the transport cases pin.
	host  string
	env   map[string]string
	event string
	rules bool
	// codex marks an arrangement whose PreToolUse reply must never carry permissionDecision
	// "ask": Codex parses it, reports the hook failed, and runs the call anyway.
	codex bool
	// ownResponse is a reader-written HOST_RESPONSE, which cannot claim --renders-ask.
	ownResponse bool
	// wantsAsk marks an arrangement whose ask must render as the host's own prompt.
	wantsAsk bool
}

var glueArrangements = []glueArrangement{
	{label: "claude-code", host: "claude-code", event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "claude-code without the advise arm", host: "claude-code", env: map[string]string{"__MAGUS_NO_ADVISE": "1"}, event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex with no prompt rule", host: "codex", codex: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex with its prompt rule", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex in a mode that never prompts", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"bypassPermissions","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "codex approval request for a push", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push origin HEAD"}}`},
	{label: "codex approval request for anything else", host: "codex", codex: true, rules: true, event: `{"hook_event_name":"PermissionRequest","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"rm -rf build"}}`},
	// The payload never overrides the name: an event carrying Codex's turn_id, under a
	// wiring that names claude-code, gets Claude Code's ask.
	{label: "claude-code named, Codex-shaped event", host: "claude-code", wantsAsk: true, event: `{"hook_event_name":"PreToolUse","session_id":"s","turn_id":"t","permission_mode":"default","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
	{label: "a reader's own HOST_RESPONSE", host: "claude-code", ownResponse: true, env: map[string]string{"HOST_RESPONSE": `{{if eq .decision "deny"}}{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":{{toJson .reason}}}}{{end}}`}, event: `{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":{"command":"git push","file_path":"README.md"}}`},
}

// glueTemplateEcho stands in for magus: it prints the template it was handed and nothing
// else, so what the glue ASSEMBLES for a host is read from the glue running. It records
// whether the call claimed --renders-ask in the file $RENDERS_ASK_RECORD names.
const glueTemplateEcho = "#!/bin/sh\nclaim=no\nfor a in \"$@\"; do [ \"$a\" = --renders-ask ] && claim=yes; done\nprintf '%s' \"$claim\" > \"$RENDERS_ASK_RECORD\"\n" +
	"while [ $# -gt 0 ]; do\n  if [ \"$1\" = -o ]; then printf '%s' \"${2#template=}\"; exit 0; fi\n  shift\ndone\nexit 1\n"

func glueLoadSchema(t *testing.T, rel string) *jsonschema.Resolved {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(glueHostSchemaDir, rel))
	require.NoError(t, err, "read the vendored schema %s", rel)
	var schema jsonschema.Schema
	require.NoError(t, json.Unmarshal(raw, &schema), "%s must parse as a JSON Schema", rel)
	// nil loader on purpose: a schema that grew a remote $ref would fail here rather
	// than turn every run of this test into an HTTP request.
	resolved, err := schema.Resolve(nil)
	require.NoError(t, err, "%s must resolve with no loader", rel)
	return resolved
}

func glueDecode(t *testing.T, label, body string) any {
	t.Helper()
	var doc any
	require.NoError(t, json.Unmarshal([]byte(body), &doc), "%s must be valid JSON: %s", label, body)
	return doc
}

// runGlue runs one shipped glue file under `magus buzz`, as a host config wires it,
// against event, with magus replaced by glueTemplateEcho. It returns what the glue
// printed and whether its call to magus claimed --renders-ask. Call it from a subtest:
// it sets the environment and working directory for the rest of the test it runs in.
func runGlue(t *testing.T, name string, a glueArrangement) (string, bool) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("the stand-in for magus is a POSIX sh script, and sh is not installed")
	}
	dir := t.TempDir()
	if a.rules {
		rules := filepath.Join(dir, ".codex", "rules", "magus.rules")
		require.NoError(t, os.MkdirAll(filepath.Dir(rules), 0o755))
		require.NoError(t, os.WriteFile(rules, []byte("prefix_rule(pattern = [\"git\", \"push\"], decision = \"prompt\")\n"), 0o644))
	}
	bin := filepath.Join(t.TempDir(), "magus")
	require.NoError(t, os.WriteFile(bin, []byte(glueTemplateEcho), 0o755))
	record := filepath.Join(dir, "renders-ask")
	t.Setenv("__MAGUS_BIN", bin)
	t.Setenv("RENDERS_ASK_RECORD", record)
	t.Setenv("TMPDIR", t.TempDir())
	for k, v := range a.env {
		t.Setenv(k, v)
	}
	t.Chdir(dir)

	script := filepath.Join(glueTemplateDir, name)
	var runErr error
	var reply string
	withStdin(t, a.event, func() {
		reply = captureStdout(t, func() {
			runErr = buzzCmd(t.Context(), dir, []string{"-s", script, "--", "--agent-name", a.host})
		})
	})
	require.NoError(t, runErr, "%s (%s) must never fail: its host reads a non-zero exit as an error", name, a.label)
	claim, err := os.ReadFile(record)
	require.NoError(t, err, "%s (%s) never called magus", name, a.label)
	return reply, string(claim) == "yes"
}

// TestGlueRepliesValidateAgainstTheirHostSchema grades the bytes a host actually receives
// from the shared glue: the template each glue file assembles for an arrangement, rendered
// on every verdict, validated against the schema the host's stdout answers to.
//
// Rendered from the glue running rather than from a copy pasted here, so an edit to a
// glue file is graded on the next run instead of drifting away from a fixture nobody
// updates.
func TestGlueRepliesValidateAgainstTheirHostSchema(t *testing.T) {
	funcs := templateFuncs()
	for _, name := range []string{"magus-command.buzz", "magus-path.buzz"} {
		raw, err := os.ReadFile(filepath.Join(glueTemplateDir, name))
		require.NoError(t, err)
		hosts := map[string]bool{}
		for _, match := range glueCoverageHost.FindAllStringSubmatch(string(raw), -1) {
			hosts[match[1]] = true
		}
		require.NotEmpty(t, hosts, "%s declares no magus-guard-coverage host, so nothing says whose schema grades it", name)

		// The replies the glue prints without asking magus at all: the unreadable-event
		// deny and the approval reply that leaves the prompt to the person.
		for _, match := range glueRawObject.FindAllStringSubmatch(string(raw), -1) {
			literal := match[1]
			if strings.Contains(literal, "{{") || strings.Contains(literal, "__MAGUS_") {
				continue
			}
			for host := range hosts {
				schemaFile := glueHostOutputSchema[host]
				if strings.Contains(literal, gluePermissionRequestEvent) {
					if host != "codex" {
						continue
					}
					schemaFile = gluePermissionRequestSchema
				}
				assert.NoError(t, glueLoadSchema(t, schemaFile).Validate(glueDecode(t, name, literal)),
					"%s prints %s, which %s would not accept, per %s", name, literal, host, schemaFile)
			}
		}

		for _, a := range glueArrangements {
			t.Run(name+"/"+a.label, func(t *testing.T) {
				body, rendersAsk := runGlue(t, name, a)
				require.NotEmpty(t, body, "%s (%s) handed magus no template", name, a.label)
				// The claim is what lets magus return an ask at all, so it must track exactly
				// the replies that render one: every reply the glue assembles, and none a
				// reader wrote.
				assert.Equal(t, !a.ownResponse, rendersAsk,
					"%s (%s): --renders-ask claimed=%v; the glue's own reply must claim it and a reader's must not", name, a.label, rendersAsk)
				tmpl, err := template.New(name).Funcs(funcs).Parse(body)
				require.NoError(t, err, "%s (%s) renders its verdict through this template, so it must parse", name, a.label)

				for _, verdict := range glueVerdicts {
					var rendered strings.Builder
					require.NoError(t, tmpl.Execute(&rendered, verdict), "%s (%s) on a %s", name, a.label, verdict["decision"])
					text := rendered.String()
					// A reader's own reply is theirs to get right; magus sends it no ask.
					decision := verdict["decision"]
					if a.ownResponse {
						decision = ""
					}
					switch decision {
					case "maybe":
						assert.Contains(t, text, "deny", "%s (%s) must refuse a decision it does not know, never allow it", name, a.label)
					case "ask":
						assert.NotEmpty(t, text, "%s (%s) renders an ask as nothing, which the host takes as allow", name, a.label)
						if a.codex {
							assert.NotContains(t, text, `"permissionDecision":"ask"`,
								"%s (%s): Codex parses a hook ask, marks the hook failed, and runs the call", name, a.label)
						}
						if a.wantsAsk {
							assert.Contains(t, text, `"permissionDecision":"ask"`,
								"%s (%s): the wiring's host decides the arm, never the event's shape", name, a.label)
						}
					}
					if !strings.HasPrefix(text, "{") {
						continue
					}
					if strings.Contains(text, gluePermissionRequestEvent) {
						require.True(t, hosts["codex"], "%s answers a PermissionRequest but declares no codex coverage", name)
						assert.NoError(t, glueLoadSchema(t, gluePermissionRequestSchema).Validate(glueDecode(t, name, text)),
							"%s prints %s, which codex would not accept, per %s", name, text, gluePermissionRequestSchema)
						continue
					}
					schemaFile, ok := glueHostOutputSchema[a.host]
					require.True(t, ok, "%s answers %s and no schema grades that host's stdout", name, a.host)
					require.True(t, hosts[a.host], "%s is wired for %s but declares no coverage for it", name, a.host)
					assert.NoError(t, glueLoadSchema(t, schemaFile).Validate(glueDecode(t, name, text)),
						"%s prints %s, which %s would not accept, per %s", name, text, a.host, schemaFile)
				}
			})
		}
	}
}

// TestGlueHandsBackTheRewrittenCommand pins the stdin rewrite per host. Claude Code's
// updatedInput replaces the whole tool input, so every other field comes back with only
// the command replaced; Codex applies updatedInput only beside an explicit allow, and a
// restated Read is not the command the host runs, so neither is handed one.
func TestGlueHandsBackTheRewrittenCommand(t *testing.T) {
	funcs := templateFuncs()
	const input = `{"command":"git push","description":"push it","timeout":60000,"run_in_background":true}`
	const rewritten = `{"command":"exec </dev/null; git push","description":"push it","timeout":60000,"run_in_background":true}`
	for _, tc := range []struct {
		label, host, event string
		want               map[string]string // decision -> reply; "" renders nothing
	}{
		{"claude-code", "claude-code",
			`{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Bash","tool_input":` + input + `}`,
			map[string]string{
				"pass":   `{"hookSpecificOutput":{"hookEventName":"PreToolUse","updatedInput":` + rewritten + `}}`,
				"advise": `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"said once","updatedInput":` + rewritten + `}}`,
			}},
		{"codex", "codex",
			`{"hook_event_name":"PreToolUse","session_id":"s","permission_mode":"default","tool_name":"Bash","tool_input":` + input + `}`,
			map[string]string{
				"pass":   "",
				"advise": `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"said once"}}`,
			}},
		{"a restated read", "claude-code",
			`{"hook_event_name":"PreToolUse","session_id":"s","tool_name":"Read","tool_input":{"file_path":"README.md"}}`,
			map[string]string{
				"pass":   "",
				"advise": `{"hookSpecificOutput":{"hookEventName":"PreToolUse","additionalContext":"said once"}}`,
			}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			body, _ := runGlue(t, "magus-command.buzz", glueArrangement{label: tc.label, host: tc.host, event: tc.event, codex: tc.host == "codex"})
			// magus renders with missingkey=error, and a verdict with no rewrite has no key.
			tmpl, err := template.New(tc.label).Funcs(funcs).Option("missingkey=error").Parse(body)
			require.NoError(t, err)
			for decision, want := range tc.want {
				verdict := map[string]any{"decision": decision, "updated_command": "exec </dev/null; git push"}
				if decision == "advise" {
					verdict["context"] = "said once"
				}
				var out strings.Builder
				require.NoError(t, tmpl.Execute(&out, verdict), "%s on a %s", tc.label, decision)
				if want == "" {
					assert.Empty(t, out.String(), "%s on a %s", tc.label, decision)
					continue
				}
				assert.JSONEq(t, want, out.String(), "%s on a %s", tc.label, decision)
				assert.NoError(t, glueLoadSchema(t, glueHostOutputSchema[tc.host]).Validate(glueDecode(t, tc.label, out.String())))
			}
			var plain strings.Builder
			require.NoError(t, tmpl.Execute(&plain, map[string]any{"decision": "pass"}), "a pass with no rewrite has no updated_command key")
			assert.Empty(t, plain.String())
		})
	}
}

// glueCursorEvents are the two Cursor events that read a reply the guard renders, and
// the schema each one's stdout answers to. Cursor validates each event's stdout with a
// different function, and its gating events carry no advisory channel, so the guard
// answers two events with two shapes.
var glueCursorEvents = map[string]struct {
	event, schema string
}{
	"gate":   {`{"hook_event_name":"beforeShellExecution","conversation_id":"c","command":"git push","cwd":"."}`, "cursor/hook-output.schema.json"},
	"advise": {`{"hook_event_name":"postToolUse","conversation_id":"c","tool_name":"Shell","tool_input":{"command":"git push"}}`, "cursor/post-tool-use.output.schema.json"},
}

// glueSchemaPropertyNames reads the field names a vendored schema declares, out of the
// raw JSON: the question is what the host NAMES, and jsonschema-go validates rather than
// introspects.
func glueSchemaPropertyNames(t *testing.T, rel string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(glueHostSchemaDir, rel))
	require.NoError(t, err, "read the vendored schema %s", rel)
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	require.NotEmpty(t, schema.Properties, "%s must name the fields it grades", rel)
	return schema.Properties
}

// TestCursorGlueRepliesValidateAgainstTheEventThatReadsThem grades what cursor-hook.buzz
// answers, per event.
//
// Both halves of the check matter and only one of them is a schema. Validating catches a
// field whose TYPE moved. The subset assertion catches a field Cursor RENAMED, which
// validating cannot: Cursor's stdout validators read the fields they know and ignore the
// rest, so a reply naming additional_context_v2 is accepted, ignored, and carries no
// advisory at all.
func TestCursorGlueRepliesValidateAgainstTheEventThatReadsThem(t *testing.T) {
	funcs := templateFuncs()
	for name, ev := range glueCursorEvents {
		t.Run(name, func(t *testing.T) {
			body, _ := runGlue(t, "cursor-hook.buzz", glueArrangement{label: name, host: "cursor", event: ev.event})
			require.NotEmpty(t, body, "cursor-hook.buzz handed magus no template on its %s event", name)
			schema := glueLoadSchema(t, ev.schema)
			named := glueSchemaPropertyNames(t, ev.schema)

			tmpl, err := template.New(name).Funcs(funcs).Parse(body)
			require.NoError(t, err, "the %s template carries the reply, so it must parse", name)
			for _, verdict := range glueVerdicts {
				var out strings.Builder
				require.NoError(t, tmpl.Execute(&out, verdict), "the %s template on a %s", name, verdict["decision"])
				reply, isObject := glueDecode(t, name, out.String()).(map[string]any)
				require.True(t, isObject, "the %s template must render a JSON object", name)
				if name == "gate" {
					switch verdict["decision"] {
					case "ask":
						assert.Equal(t, "ask", reply["permission"], "an ask is Cursor's own approval prompt")
					case "pass", "advise":
						assert.Equal(t, "allow", reply["permission"])
					default:
						assert.Equal(t, "deny", reply["permission"], "only pass and advise may allow; %s must not", verdict["decision"])
					}
				}
				assert.NoError(t, schema.Validate(reply),
					"the %s template renders %s, which Cursor would not accept, per %s", name, out.String(), ev.schema)
				for field := range reply {
					assert.Contains(t, named, field,
						"the %s template names %q, which %s does not. Cursor ignores a field it has never\n"+
							"heard of rather than rejecting it, so a rename costs the verdict in silence.",
						name, field, ev.schema)
				}
			}
		})
	}
}

// The guard hook's `magus buzz` forks `magus shell`, which inherits MAGUS_PPROF. Each
// process profiles to its own file, so neither overwrites the profile the other is
// still writing.
func TestBuzzHookProfilesEachMagusApart(t *testing.T) {
	for spec, want := range map[string]string{
		"cpu:/tmp/c":               "cpu:/tmp/c.%p",
		"cpu:/tmp/c,mem:/tmp/m":    "cpu:/tmp/c.%p,mem:/tmp/m.%p",
		"cpu: /tmp/c ":             "cpu:/tmp/c.%p",
		"trace:/tmp/t.%p":          "trace:/tmp/t.%p",
		"cpu:/tmp/c.%p,mem:/tmp/m": "cpu:/tmp/c.%p,mem:/tmp/m.%p",
		"cpu":                      "cpu",
		"cpu:":                     "cpu:",
	} {
		assert.Equal(t, want, pprofDescendantSpec(spec), spec)
	}

	dir := t.TempDir()
	t.Setenv(pprofEnv, "cpu:"+filepath.Join(dir, "top.pprof")+",mem:"+filepath.Join(dir, "own.%p"))
	stop := startProfiling()
	inherited := os.Getenv(pprofEnv)
	stop()
	assert.Equal(t, "cpu:"+filepath.Join(dir, "top.pprof.%p")+",mem:"+filepath.Join(dir, "own.%p"), inherited)
	for _, name := range []string{"top.pprof", fmt.Sprintf("own.%d", os.Getpid())} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.True(t, bytes.HasPrefix(data, []byte{0x1f, 0x8b}), "%s is a gzipped profile", name)
	}
}
