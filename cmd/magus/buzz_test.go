package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/guard"
	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/tier"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// TestBuzzCmd_SilentSuppressesUnusedImportWarning verifies -s/--silent, the flag
// this command's own progress/error output already respects, also gates the new
// warning line: a silent run should stay silent.
func TestBuzzCmd_SilentSuppressesUnusedImportWarning(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "unused.buzz")
	require.NoError(t, os.WriteFile(path, []byte("import \"fs\";\nvar x = 1;\n"), 0o644))

	useTextLogger(t)

	var runErr error
	stderr := captureStderr(t, func() {
		runErr = buzzCommand(context.Background(), "", []string{"-s", path})
	})

	require.NoError(t, runErr)
	assert.NotContains(t, stderr, "BZZ3001", "-s/--silent must suppress the warning line")
}

func TestBuzzCmd_ManagedHookRunsTheBinarysGlue(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(fmt.Sprintf("installed=%t", installed), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".magus", "harness", "hooks", "magus-reclaim-disk.buzz")
			if installed {
				require.NoError(t, os.MkdirAll(filepath.Join(filepath.Dir(path), "lib"), 0o755))
				require.NoError(t, os.WriteFile(path, []byte("import \"diskwatch\";\n"), 0o644))
				require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(path), "lib", "hook.buzz"),
					[]byte("import \"retired-hook-helper\";\n"), 0o644))
			}
			countWorkspaceOpens(t, func(context.Context, string) (*magus.Magus, error) { return nil, nil })
			var runErr error
			stderr := captureStderr(t, func() {
				runErr = buzzCommand(t.Context(), "", []string{"-s", path})
			})
			require.NoError(t, runErr)
			assert.Contains(t, stderr, "[MGS3024]", "the embedded main runs and reports the missing host argument")
			assert.NotContains(t, stderr, "BZZ2001")
			_, err := os.Stat(path)
			if installed {
				require.NoError(t, err)
				body, err := os.ReadFile(path)
				require.NoError(t, err)
				assert.Equal(t, "import \"diskwatch\";\n", string(body), "running a hook rewrites no installed files")
			} else {
				assert.ErrorIs(t, err, os.ErrNotExist)
			}
		})
	}
}

func TestBuzzCmd_AnOrdinaryHookFileStillRunsItsOwnSource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "magus-reclaim-disk.buzz")
	require.NoError(t, os.WriteFile(path, []byte("import \"retired-hook-module\";\n"), 0o644))
	countWorkspaceOpens(t, func(context.Context, string) (*magus.Magus, error) { return nil, nil })
	err := buzzCommand(t.Context(), "", []string{"-s", path})
	require.ErrorContains(t, err, "retired-hook-module")
}

// buzzSandboxTarget is what the fixture script removes. It sits outside the workspace
// and every other write grant, so a permitted run would reach it. Nothing is created
// there: checkWrite runs before the removal, so the deny fires on a path that does not
// exist, and a permitted run removes nothing.
const buzzSandboxTarget = "/magus-buzz-sandbox-test/victim"

// buzzSandboxWorkspace builds a workspace whose magus.yaml sets the sandbox as asked,
// opens it, and returns it on the context loadMagus reads so buzzCommand never touches the
// process singleton.
func buzzSandboxWorkspace(t *testing.T, mode types.SandboxMode) (context.Context, *magus.Magus, string) {
	t.Helper()
	w := testkit.NewWorkspace(t)
	root := w.Root()
	w.Magusfile("import \"magus\";\n\nmagus\\project({})\n")
	w.Write("magus.yaml", fmt.Sprintf("sandbox:\n  mode: %s\n", mode))

	m, err := magus.Open(t.Context(), root, magus.WithSpawnRule(tier.Ceiling))
	require.NoError(t, err, "fixture workspace must open")
	t.Cleanup(func() { _ = m.Close() })

	script := filepath.Join(root, "wipe.buzz")
	require.NoError(t, os.WriteFile(script,
		fmt.Appendf(nil, "import \"fs\";\n\nfun main(args: [str]) > void !> any {\n    fs\\removeAll(\"%s\");\n}\n", buzzSandboxTarget), 0o644))

	return withMagus(testkit.WithEnvResolver(t.Context()), m), m, script
}

// TestBuzzCmd_ScriptRunsUnderTheWorkspaceSandbox pins the reason `magus buzz` does not
// escape the sandbox. The agent guard allows `magus buzz -` outright and cannot read a
// script body, so a script that magus never sandboxed had unrestricted fs, proc and
// network access in a workspace that had asked for a sandbox. The script runs in this process, which
// the sandbox never confines, so the binding check is the whole defense here.
func TestBuzzCmd_ScriptRunsUnderTheWorkspaceSandbox(t *testing.T) {
	ctx, m, script := buzzSandboxWorkspace(t, types.SandboxModeBestEffort)

	err := buzzCommand(ctx, "", []string{"-s", script})

	require.Error(t, err, "a write outside the workspace must be refused")
	assert.ErrorContains(t, err, string(types.PathWriteDenied))

	events, rerr := loggedCalls(ctx, m.CacheDir(), 10)
	require.NoError(t, rerr)
	require.Len(t, events, 1, "the refusal must be readable on the trail, as a target's is")
	assert.Equal(t, journal.CallKindSandboxDenial, events[0].Kind)
}

// loggedCalls is the newest limit calls in the log under cacheDir.
func loggedCalls(ctx context.Context, cacheDir string, limit int) ([]journal.Call, error) {
	var out []journal.Call
	for c, err := range recorder.ReadCalls(ctx, cacheDir, limit) {
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// loggedPayload is the payload ref names in the log under cacheDir.
func loggedPayload(cacheDir, ref string) ([]byte, error) {
	return recorder.ReadCallPayload(cacheDir, journal.Payload{Ref: ref})
}

// TestBuzzCmd_SandboxDisabledLeavesTheScriptUnrestricted holds the other half: the
// sandbox is off by default, and a script in a workspace that never asked for one keeps
// writing wherever it could before.
func TestBuzzCmd_SandboxDisabledLeavesTheScriptUnrestricted(t *testing.T) {
	ctx, m, script := buzzSandboxWorkspace(t, types.SandboxModeOff)

	require.NoError(t, buzzCommand(ctx, "", []string{"-s", script}))

	events, rerr := loggedCalls(ctx, m.CacheDir(), 10)
	require.NoError(t, rerr)
	assert.Empty(t, events, "nothing was refused, so nothing belongs on the trail")
}

const (
	buzzVanillaScript  = "import \"std\";\n\nfun main(args: [str]) > void {\n    std\\print(\"hi\");\n}\n"
	buzzUnmergedScript = "import \"std\";\nimport \"magus\";\n\nfun main(args: [str]) > void !> any {\n    final r = magus\\workspace().unmerged();\n    std\\print(\"jobs={r.jobs.len()}\");\n}\n"
	buzzMemberScript   = "import \"std\";\nimport \"magus\";\n\nfun main(args: [str]) > void !> any {\n    final p = magus\\describe.project();\n    std\\print(\"{p.projects.len()}\");\n}\n"
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
	w := testkit.NewWorkspace(t)
	w.Magusfile("import \"magus\";\n\nmagus\\project({})\n")
	w.Write("magus.yaml", "sandbox:\n  mode: off\n")
	return w.Root(), w.Write("script.buzz", script)
}

func TestBuzzCmd_WorkspaceMemberOpensTheWorkspaceOnce(t *testing.T) {
	root, script := buzzLazyWorkspace(t, buzzMemberScript)
	var m *magus.Magus
	opens := countWorkspaceOpens(t, func(ctx context.Context, _ string) (*magus.Magus, error) {
		var err error
		m, err = magus.Open(ctx, root, magus.WithSpawnRule(tier.Ceiling))
		return m, err
	})
	t.Cleanup(func() {
		if m != nil {
			_ = m.Close()
		}
	})

	var runErr error
	stdout := captureStdout(t, func() { runErr = buzzCommand(testkit.WithEnvResolver(t.Context()), root, []string{"-s", script}) })

	require.NoError(t, runErr)
	assert.Equal(t, "1\n", stdout)
	assert.Equal(t, 1, *opens)
}

// The stop policy reads magus\workspace().unmerged() from a hook script.
func TestBuzzCmd_WorkspaceHandleReadsTheScriptsWorkspace(t *testing.T) {
	root, script := buzzLazyWorkspace(t, buzzUnmergedScript)
	var m *magus.Magus
	opens := countWorkspaceOpens(t, func(ctx context.Context, _ string) (*magus.Magus, error) {
		var err error
		m, err = magus.Open(ctx, root, magus.WithSpawnRule(tier.Ceiling))
		return m, err
	})
	t.Cleanup(func() {
		if m != nil {
			_ = m.Close()
		}
	})

	var runErr error
	stdout := captureStdout(t, func() { runErr = buzzCommand(testkit.WithEnvResolver(t.Context()), root, []string{"-s", script}) })

	require.NoError(t, runErr)
	assert.Equal(t, "jobs=0\n", stdout)
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
	"magus-session.buzz",
	"cursor-hook.buzz",
	"magus-reclaim-disk.buzz",
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
				return magus.Open(ctx, root, magus.WithSpawnRule(tier.Ceiling))
			})
			script, err := filepath.Abs(filepath.Join("..", "..", "docs", "guides", "integrations", "agents", name))
			require.NoError(t, err)
			require.FileExists(t, script, "the shipped glue must be where the harness wires it")
			t.Setenv("__MAGUS_BIN", filepath.Join(t.TempDir(), "absent-magus"))
			t.Setenv("TMPDIR", t.TempDir())

			var runErr error
			withStdin(t, event, func() {
				captureStdout(t, func() { runErr = buzzCommand(testkit.WithEnvResolver(t.Context()), root, []string{"-s", script}) })
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

// A hook runs in whatever directory the session is in, so its command names the root
// with -C and the script by a root-relative path.
func TestBuzzCmd_WorkDirResolvesRootRelativeScripts(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "hooks", "lib"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "sub", "deeper"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "hooks", "lib", "answer.buzz"), []byte(
		"export fun answer() > str { return \"ok\"; }\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "hooks", "main.buzz"), []byte(
		"import \"std\";\nimport \"lib/answer\";\nfun main() > void { std\\print(answer()); }\n"), 0o644))

	for _, args := range [][]string{
		{"-C", root, "-s", "hooks/main.buzz"},
		{"-C=" + root, "hooks/main.buzz"},
		{"-s", "-C", root, "hooks/main.buzz"},
	} {
		t.Run(strings.Join(args[:len(args)-1], " "), func(t *testing.T) {
			t.Chdir(filepath.Join(root, "sub", "deeper"))
			var runErr error
			stdout := captureStdout(t, func() { runErr = buzzCommand(testkit.WithEnvResolver(t.Context()), "", args) })
			require.NoError(t, runErr)
			assert.Equal(t, "ok\n", stdout)
		})
	}
}

func TestBuzzCmd_WorkDirMissingNamesTheDirectory(t *testing.T) {
	t.Chdir(t.TempDir())
	missing := filepath.Join(t.TempDir(), "gone")
	for _, args := range [][]string{
		{"-C", missing, "-e", "fun main() > void {}"},
		{"-e", "fun main() > void {}", "-C", missing},
		{"-C", missing, "lsp"},
	} {
		err := buzzCommand(testkit.WithEnvResolver(t.Context()), "", args)
		require.Error(t, err, "%v", args)
		assert.Contains(t, err.Error(), fmt.Sprintf("-C %s: no such file or directory", missing))
	}
}

func TestBuzzScriptStageSkipsALeadingWorkDir(t *testing.T) {
	assert.False(t, buzzScriptStage([]string{"-C", "/x", "lsp"}), "lsp is no pipe stage")
	assert.True(t, buzzScriptStage([]string{"-C", "/x", "hooks/main.buzz"}), "a script is")
}

// buzzTrace runs buzzCommand under an enabled startup trace wired the way main does it,
// with the trace's profile on ctx.
func buzzTrace(t *testing.T, args []string) (stdout, trace string) {
	t.Helper()
	tr := observability.NewStartupTracer(true)
	ctx := buzz.WithProfile(observability.WithTrace(context.Background(), tr), tr.Profile())
	var runErr error
	stdout = captureStdout(t, func() {
		runErr = buzzCommand(ctx, "", args)
	})
	require.NoError(t, runErr)
	var buf bytes.Buffer
	tr.SetWriter(&buf)
	tr.Done()
	return stdout, buf.String()
}

func TestBuzzCmd_TraceRecordsEachCompilePhaseOnce(t *testing.T) {
	_, trace := buzzTrace(t, []string{"-s", "-e", `import "std"; fun main() > void { std\print("ok"); }`})
	assert.Contains(t, trace, "buzz profile:")
	assert.Regexp(t, `(?m)^ +\S+ +parse$`, trace, "the profile carries the compile phases")
	assert.NotContains(t, trace, "buzz.parse", "a second copy on the startup trace doubles every phase")
}

func TestRecordBuzzRunKeepsTheOutputUnderItsRef(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, recordBuzzRun(testkit.WithEnvResolver(t.Context()), root, "probe.buzz", "code", []string{"--x"}, false, []byte("hello\n"), time.Now(), nil))

	dir, err := magus.ResolveCacheDir(root)
	require.NoError(t, err)
	store := cache.NewOutputStore(dir)
	lease, _, err := guard.CheckoutLease(t.Context(), root, jobPlace)
	require.NoError(t, err)
	ref := cache.PortableRef(interp.ScriptRunKey("probe.buzz", "code", lease, []string{"--x"}))
	out, d, err := store.ByRef(ref)
	require.NoError(t, err)
	assert.Equal(t, "hello\n", string(out))
	assert.Equal(t, "buzz probe.buzz", d.Target)
	assert.False(t, d.Failed)

	assert.NotEqual(t, interp.ScriptRunKey("probe.buzz", "code", lease, nil), interp.ScriptRunKey("probe.buzz", "code", lease, []string{"--x"}),
		"different arguments are a different probe")
	assert.NotEqual(t, interp.ScriptRunKey("probe.buzz", "code", "job-a", nil), interp.ScriptRunKey("probe.buzz", "code", "job-b", nil),
		"another job's run is another ref")
}

type BuzzRecordSuite struct {
	suite.Suite
	root string
}

func TestBuzzRecordSuite(t *testing.T) { suite.Run(t, new(BuzzRecordSuite)) }

func (s *BuzzRecordSuite) SetupTest() { s.root = s.T().TempDir() }

func (s *BuzzRecordSuite) TestRecordKeepsArgsAndLeaseInTheDescriptor() {
	args := []string{"a", "b"}
	s.Require().NoError(recordBuzzRun(testkit.WithEnvResolver(s.T().Context()), s.root, "p.buzz", "code", args, false, nil, time.Now(), nil))

	dir, err := magus.ResolveCacheDir(s.root)
	s.Require().NoError(err)
	lease, _, err := guard.CheckoutLease(s.T().Context(), s.root, jobPlace)
	s.Require().NoError(err)
	_, d, err := cache.NewOutputStore(dir).ByRef(cache.PortableRef(interp.ScriptRunKey("p.buzz", "code", lease, args)))
	s.Require().NoError(err)
	d.Ref, d.TimestampMs, d.DurationMs, d.PersistedNs, d.Attempt, d.Key, d.KeyVersion, d.Inv = "", 0, 0, 0, "", "", "", ""
	s.Equal(cache.OutputDescriptor{Project: ".", Target: "buzz p.buzz", ExtraArgs: args, Lease: lease}, d)
}

func (s *BuzzRecordSuite) scriptCheckRow(id, root, script string) job.Job {
	raw, err := types.EncodeLeaseCheck(types.LeaseCheck{Script: script})
	s.Require().NoError(err)
	return job.Job{ID: id, State: job.StateRunning, CheckoutRoot: root, Check: raw}
}

func (s *BuzzRecordSuite) holder(rows []job.Job, root, script string) string {
	holder, err := scriptCheckHolder(rows, root, script)
	s.Require().NoError(err)
	return holder
}

func (s *BuzzRecordSuite) TestHolderIsTheOneRowCheckingTheScriptHere() {
	rows := []job.Job{s.scriptCheckRow("a", s.root, "p.buzz"), s.scriptCheckRow("b", s.root, "other.buzz")}
	s.Equal("a", s.holder(rows, s.root, "./p.buzz"))
}

func (s *BuzzRecordSuite) TestTwoRowsCheckingTheScriptNameNoHolder() {
	rows := []job.Job{s.scriptCheckRow("a", s.root, "p.buzz"), s.scriptCheckRow("b", s.root, "p.buzz")}
	s.Empty(s.holder(rows, s.root, "p.buzz"))
}

func (s *BuzzRecordSuite) TestARowInAnotherCheckoutIsNoHolder() {
	s.Empty(s.holder([]job.Job{s.scriptCheckRow("a", s.T().TempDir(), "p.buzz")}, s.root, "p.buzz"))
}

func (s *BuzzRecordSuite) TestAnExitedRowIsNoHolder() {
	row := s.scriptCheckRow("a", s.root, "p.buzz")
	row.State = job.StateExited
	s.Empty(s.holder([]job.Job{row}, s.root, "p.buzz"))
}

func (s *BuzzRecordSuite) TestRecordPrintsItsRefOnStderrWhateverTheLogLevel() {
	s.T().Setenv("MAGUS_LOG_SILENT", "true")
	r, w, err := os.Pipe()
	s.Require().NoError(err)
	saved := os.Stderr
	os.Stderr = w
	recErr := recordBuzzRun(testkit.WithEnvResolver(s.T().Context()), s.root, "p.buzz", "code", nil, false, nil, time.Now(), nil)
	os.Stderr = saved
	s.Require().NoError(w.Close())
	printed, err := io.ReadAll(r)
	s.Require().NoError(err)
	s.Require().NoError(recErr)
	s.Regexp(`^ref  \S+\n$`, string(printed))
}

func (s *BuzzRecordSuite) TestAStaleJobStoreRecordsTheRunWithoutALease() {
	s.T().Setenv("BAGGAGE", "")
	// recordLease keys the store by the resolved root, and a temp dir is a symlink on macOS.
	root, err := filepath.EvalSymlinks(s.root)
	s.Require().NoError(err)
	store, err := openJobs(s.T().Context(), root, jobOpen{})
	s.Require().NoError(err)
	path, err := store.Path()
	s.Require().NoError(err)
	s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
	s.Require().NoError(os.WriteFile(path, []byte(`{"id":"a","schema_version":13,"state":"running","created":1,"updated":1}`+"\n"), 0o644))

	r, w, err := os.Pipe()
	s.Require().NoError(err)
	saved := os.Stderr
	os.Stderr = w
	recErr := recordBuzzRun(testkit.WithEnvResolver(s.T().Context()), root, "p.buzz", "code", nil, true, nil, time.Now(), nil)
	os.Stderr = saved
	s.Require().NoError(w.Close())
	printed, err := io.ReadAll(r)
	s.Require().NoError(err)

	s.Require().NoError(recErr)
	s.Contains(string(printed), "recorded without a lease")
	s.Contains(string(printed), path)
	s.Regexp(`ref  \S+\n$`, string(printed))
}

func (s *BuzzRecordSuite) TestRecordReturnsTheReasonWhenTheStoreCannotWrite() {
	dir, err := magus.ResolveCacheDir(s.root)
	s.Require().NoError(err)
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "outputs"), []byte("x"), 0o644))

	err = recordBuzzRun(testkit.WithEnvResolver(s.T().Context()), s.root, "p.buzz", "code", nil, false, nil, time.Now(), nil)
	s.Require().Error(err)
	s.Contains(err.Error(), "--record: store the run")
}

// A -t run is evidence a script check cites, so it is recorded like --record, under its
// own target, and the record holds every test line and the summary.
func TestBuzzTestRunIsRecordedWithItsTestLines(t *testing.T) {
	root := t.TempDir()
	dir, err := magus.ResolveCacheDir(root)
	require.NoError(t, err)
	store := cache.NewOutputStore(dir)
	for name, tc := range map[string]struct {
		body   string
		failed bool
		lines  []string
	}{
		"passing": {"test \"adds\" { std\\assert(1 + 1 == 2, message: \"sum\"); }\n", false, []string{`ok    test "adds"`, "1 passed, 0 failed, 0 skipped"}},
		"failing": {"test \"breaks\" { std\\assert(false, message: \"no\"); }\n", true, []string{`FAIL  test "breaks"`, "0 passed, 1 failed, 0 skipped"}},
	} {
		t.Run(name, func(t *testing.T) {
			script := filepath.Join(root, name+".buzz")
			code := "import \"std\";\n\n" + tc.body
			require.NoError(t, os.WriteFile(script, []byte(code), 0o644))

			var runErr error
			captureStdout(t, func() { runErr = buzzCommand(testkit.WithEnvResolver(t.Context()), root, []string{"-t", script}) })
			assert.Equal(t, tc.failed, runErr != nil, "%v", runErr)

			lease, _, err := guard.CheckoutLease(t.Context(), root, jobPlace)
			require.NoError(t, err)
			out, d, err := store.ByRef(cache.PortableRef(interp.ScriptRunKey(script, code, lease, []string{"-t"})))
			require.NoError(t, err)
			assert.Equal(t, cache.OutputDescriptor{Target: "buzz -t " + script, Failed: tc.failed}, cache.OutputDescriptor{Target: d.Target, Failed: d.Failed})
			for _, line := range tc.lines {
				assert.Contains(t, string(out), line)
			}
		})
	}
}

// BuzzTestManyScriptsSuite pins that every path after -t is a script to test,
// so a spell's test scope runs a file and its _test.buzz sibling.
type BuzzTestManyScriptsSuite struct {
	suite.Suite
	dir string
}

func TestBuzzTestManyScriptsSuite(t *testing.T) { suite.Run(t, new(BuzzTestManyScriptsSuite)) }

func (s *BuzzTestManyScriptsSuite) SetupTest() { s.dir = s.T().TempDir() }

func (s *BuzzTestManyScriptsSuite) script(name, assertion string) string {
	path := filepath.Join(s.dir, name)
	body := fmt.Sprintf(`import "std";

test %q {
    std\assert(%s, message: %q);
}
`, name, assertion, name)
	s.Require().NoError(os.WriteFile(path, []byte(body), 0o644))
	return path
}

func (s *BuzzTestManyScriptsSuite) run(args ...string) (string, error) {
	var err error
	out := captureStdout(s.T(), func() { err = buzzCommand(testkit.WithEnvResolver(s.T().Context()), s.dir, args) })
	return out, err
}

func (s *BuzzTestManyScriptsSuite) TestForwardedArgsNeedASingleScript() {
	_, err := s.run("-t", s.script("a.buzz", "true"), s.script("b.buzz", "true"), "--", "x")
	s.Require().Error(err)
	s.Equal(exit.Usage, exit.Code(err))
	s.Contains(err.Error(), "--")
}

func (s *BuzzTestManyScriptsSuite) TestASingleScriptStillTakesForwardedArgs() {
	_, err := s.run("-t", s.script("a.buzz", "true"), "--", "x")
	s.Require().NoError(err)
}

func (s *BuzzTestManyScriptsSuite) TestNoScriptAndNoPipedStdinIsAUsageError() {
	null, err := os.Open(os.DevNull)
	s.Require().NoError(err)
	defer null.Close()
	prev := os.Stdin
	os.Stdin = null
	defer func() { os.Stdin = prev }()
	_, err = s.run("-t")
	s.Require().Error(err)
	s.Equal(exit.Usage, exit.Code(err))
	s.Contains(err.Error(), "-t <file>")
}

func (s *BuzzTestManyScriptsSuite) TestPipedStdinStillRunsAsTheScript() {
	var out string
	var err error
	withStdin(s.T(), "import \"std\";\ntest \"piped\" { std\\assert(true, message: \"p\"); }\n", func() {
		out, err = s.run("-t")
	})
	s.Require().NoError(err)
	s.Contains(out, `ok    test "piped"`)
}

// scriptRunnerOver is the CLI's own runner, wired as buzzCommand wires it, over an opener
// that finds no workspace.
func scriptRunnerOver(o *scriptOpens) *interp.ScriptRunner {
	r := newBuzzRunner(context.Background())
	r.Open = o.open
	r.Adopted = func(context.Context) bool { return false }
	r.Sandbox, r.Quiet = false, false
	return r
}

// scriptOpens is an opener that finds no workspace and counts how often it was asked.
type scriptOpens struct {
	mu  sync.Mutex
	n   int
	err error
}

func (o *scriptOpens) open(context.Context, string) (*interp.ScriptWorkspace, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.n++
	return nil, o.err
}

func (o *scriptOpens) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.n
}

// scriptPhases records the startup phases a runner times.
type scriptPhases struct {
	mu   sync.Mutex
	seen []string
}

func (p *scriptPhases) time(name string) func() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen = append(p.seen, name)
	return func() {}
}

func (p *scriptPhases) names() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func writeBuzz(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func runBuzzFile(t *testing.T, r *interp.ScriptRunner, root string, o interp.ScriptOptions, rest ...string) (string, error) {
	t.Helper()
	var err error
	out := testkit.CaptureStdout(t, func() { err = r.Run(testkit.WithEnvResolver(t.Context()), root, o, "", rest, nil, false) })
	return out, err
}

func runBuzzInline(t *testing.T, code string) (string, error) {
	t.Helper()
	var err error
	out := testkit.CaptureStdout(t, func() {
		err = scriptRunnerOver(&scriptOpens{}).Run(testkit.WithEnvResolver(t.Context()), "", interp.ScriptOptions{Inline: true}, code, nil, nil, false)
	})
	return out, err
}

func TestBuzzRunner_UnusedImportWarnsOnStderrAndExitsClean(t *testing.T) {
	path := writeBuzz(t, t.TempDir(), "unused.buzz", "import \"fs\";\nvar x = 1;\n")

	var stdout string
	var runErr error
	stderr := captureStderr(t, func() {
		stdout, runErr = runBuzzFile(t, scriptRunnerOver(&scriptOpens{}), "", interp.ScriptOptions{}, path)
	})

	require.NoError(t, runErr, "a warning must never fail the run")
	assert.NotContains(t, stdout, "BZZ3001", "stdout carries structured output only; a warning must never land there")
	assert.Contains(t, stderr, "BZZ3001")
	assert.Contains(t, stderr, "warning:")

	quiet := scriptRunnerOver(&scriptOpens{})
	quiet.Quiet = true
	stderr = captureStderr(t, func() { _, runErr = runBuzzFile(t, quiet, "", interp.ScriptOptions{}, path) })
	require.NoError(t, runErr)
	assert.NotContains(t, stderr, "BZZ3001", "a quiet run stays quiet")
}

// A script's child sees the script's own process in MAGUS_INVOCATION_ANCESTORS, so a
// magus it spawns can tell its parent's claim from a stranger's instead of refusing
// MGS3009 as blind to its ancestry.
func TestBuzzRunner_ChildSeesTheScriptProcessAsAnAncestor(t *testing.T) {
	path := writeBuzz(t, t.TempDir(), "ancestry.buzz", `import "std";
import "proc";

fun main(args: [str]) > void !> any {
    final ancestors = proc\exec("sh", args: ["-c", "echo $MAGUS_INVOCATION_ANCESTORS"], opts: proc\ExecOptions{ quiet = true }).stdout.trim();
    final parent = proc\exec("sh", args: ["-c", "echo $PPID"], opts: proc\ExecOptions{ quiet = true }).stdout.trim();
    std\print("ancestors={ancestors} parent={parent}");
    std\assert(ancestors != "", message: "no MAGUS_INVOCATION_ANCESTORS in the child");
    std\assert(ancestors.indexOf("{parent}:") != null, message: "ancestry {ancestors} lacks the script process {parent}");
}
`)
	_, err := runBuzzFile(t, scriptRunnerOver(&scriptOpens{}), "", interp.ScriptOptions{}, path)
	require.NoError(t, err)
}

func TestBuzzRunner_VanillaScriptDoesNotOpenTheWorkspace(t *testing.T) {
	o := &scriptOpens{}
	path := writeBuzz(t, t.TempDir(), "vanilla.buzz", buzzVanillaScript)

	stdout, err := runBuzzFile(t, scriptRunnerOver(o), t.TempDir(), interp.ScriptOptions{}, path)

	require.NoError(t, err)
	assert.Equal(t, "hi\n", stdout)
	assert.Equal(t, 0, o.count(), "a script that reads no workspace member must not pay for opening one")
}

// Outside any workspace a script that never reads one is not warned about it, and a
// workspace member still raises MGS1022 with the reason it could not attach.
func TestBuzzRunner_OutsideAWorkspace(t *testing.T) {
	noWorkspace := errors.New("no magus workspace found")
	o := &scriptOpens{err: noWorkspace}
	dir := t.TempDir()
	vanilla := writeBuzz(t, dir, "vanilla.buzz", buzzVanillaScript)
	member := writeBuzz(t, dir, "member.buzz", buzzMemberScript)

	var runErr error
	stderr := captureStderr(t, func() { _, runErr = runBuzzFile(t, scriptRunnerOver(o), dir, interp.ScriptOptions{}, vanilla) })
	require.NoError(t, runErr)
	assert.Equal(t, 0, o.count())
	assert.NotContains(t, stderr, "workspace not attached", "a script that never asked for a workspace is not warned about one")

	stderr = captureStderr(t, func() { _, runErr = runBuzzFile(t, scriptRunnerOver(o), dir, interp.ScriptOptions{}, member) })
	require.Error(t, runErr)
	assert.ErrorContains(t, runErr, string(types.MagusfileOnlyMember))
	assert.Contains(t, stderr, "workspace not attached")
	assert.Contains(t, stderr, noWorkspace.Error())
	assert.Equal(t, 1, o.count())
}

func TestBuzzRunner_ImportsResolveBesideTheFile(t *testing.T) {
	dir := t.TempDir()
	writeBuzz(t, dir, "lib.buzz", "export fun answer() > str { return \"ok\"; }\n")
	script := writeBuzz(t, dir, "main.buzz", "import \"std\";\nimport \"lib\";\nfun main() > void { std\\print(answer()); }\n")

	stdout, err := runBuzzFile(t, scriptRunnerOver(&scriptOpens{}), "", interp.ScriptOptions{}, script)
	require.NoError(t, err)
	assert.Contains(t, stdout, "ok")
}

// --root names the checkout a script's vcs calls read. The process cwd here is the
// magus checkout this test runs in, a different repository on a different ref.
func TestBuzzRunner_RootSelectsTheVCS(t *testing.T) {
	r := testkit.NewRepository(t, nil)
	r.Git("switch", "-q", "-c", "split-target")
	script := writeBuzz(t, t.TempDir(), "ref.buzz",
		"import \"std\";\nimport \"vcs\";\n\nfun main(args: [str]) > void !> any {\n    std\\print(vcs\\ref());\n}\n")

	stdout, err := runBuzzFile(t, scriptRunnerOver(&scriptOpens{}), r.Root(), interp.ScriptOptions{}, script)

	require.NoError(t, err)
	assert.Equal(t, "split-target\n", stdout)
}

// Strict mode refuses a raising call at the top level, and the refusal names the form
// that runs one, which an -e snippet takes as well as a file does.
func TestBuzzRunner_TopLevelRaiseNamesTheMainForm(t *testing.T) {
	_, err := runBuzzInline(t, `import "vcs"; final r = vcs\root();`)
	require.ErrorContains(t, err, string(buzz.UnhandledRaise))
	assert.ErrorContains(t, err, "fun main(args: [str]) > void !> any")

	stdout, err := runBuzzInline(t,
		`import "std"; import "vcs"; fun main(args: [str]) > void !> any { std\print("root {vcs\root() != ""}"); }`)
	require.NoError(t, err)
	assert.Equal(t, "root true\n", stdout)
}

// -e is the one-off exception: statements and imports run with no `fun main`,
// while files and stdin stay strict.
func TestBuzzRunner_InlineTakesTheEmbeddedRulesAndFilesStayStrict(t *testing.T) {
	stdout, err := runBuzzInline(t, `import "std"; std\print("hi");`)
	require.NoError(t, err)
	assert.Equal(t, "hi\n", stdout)

	stdout, err = runBuzzInline(t, `import "std"; import "fs"; std\print(fs\basename("a/b.txt"));`)
	require.NoError(t, err)
	assert.Equal(t, "b.txt\n", stdout, "imports run beside the statements")

	stdout, err = runBuzzInline(t, `import "std"; fun main(args: [str]) > void { std\print("from main"); }`)
	require.NoError(t, err)
	assert.Equal(t, "from main\n", stdout, "a body that declares main still has it called")

	_, err = runBuzzInline(t, `import "std"; std\print(nope);`)
	require.Error(t, err)
	assert.ErrorContains(t, err, "line 1:25: undefined: nope", "an error position points at the user's own line")

	bare := writeBuzz(t, t.TempDir(), "bare.buzz", "import \"std\";\nif (true) { std\\print(\"bare\"); }\n")
	_, err = runBuzzFile(t, scriptRunnerOver(&scriptOpens{}), "", interp.ScriptOptions{}, bare)
	require.Error(t, err)
	assert.ErrorContains(t, err, "not allowed at the top level")
}

func scriptRunnerTraced(p *scriptPhases) *interp.ScriptRunner {
	r := scriptRunnerOver(&scriptOpens{})
	r.Phase = p.time
	return r
}

func scriptTracedRun(t *testing.T, r *interp.ScriptRunner, rest ...string) (stdout, profile string) {
	t.Helper()
	p := buzz.NewProfile()
	ctx := buzz.WithProfile(t.Context(), p)
	var err error
	stdout = testkit.CaptureStdout(t, func() { err = r.Run(ctx, "", interp.ScriptOptions{}, "", rest, nil, false) })
	require.NoError(t, err)
	return stdout, p.Report()
}

func TestBuzzRunner_TraceProfilesAClosedScriptWithoutTheNamespace(t *testing.T) {
	ph := &scriptPhases{}
	script := writeBuzz(t, t.TempDir(), "closed.buzz", "import \"std\"; fun main() > void { std\\print(\"ok\"); }\n")

	_, profile := scriptTracedRun(t, scriptRunnerTraced(ph), script)

	assert.Equal(t, []string{"buzz.register_modules", "buzz.register_decls", "buzz.exec", "buzz.main"}, ph.names())
	assert.Regexp(t, `(?m)^ +\S+ +parse$`, profile, "the profile carries the compile phases")
}

func TestBuzzRunner_TraceProfilesTheNamespaceWhenTheScriptImportsMagus(t *testing.T) {
	ph := &scriptPhases{}
	script := writeBuzz(t, t.TempDir(), "magus.buzz",
		"import \"std\"; import \"magus\"; fun main() > void { std\\print(magus\\canonicalName(\"HTTPServer\")); }\n")

	stdout, _ := scriptTracedRun(t, scriptRunnerTraced(ph), script)

	assert.Contains(t, ph.names(), "buzz.register_namespace")
	assert.Contains(t, stdout, "http-server")
}

func TestBuzzRunner_NestedMagusImportResolvesWithoutAnEntryImport(t *testing.T) {
	ph := &scriptPhases{}
	dir := t.TempDir()
	writeBuzz(t, dir, "lib.buzz", "import \"magus\";\nexport fun name() > str { return magus\\canonicalName(\"HTTPServer\"); }\n")
	script := writeBuzz(t, dir, "main.buzz", "import \"std\";\nimport \"lib\";\nfun main() > void { std\\print(name()); }\n")

	stdout, _ := scriptTracedRun(t, scriptRunnerTraced(ph), script)

	assert.Contains(t, stdout, "http-server")
	assert.Contains(t, ph.names(), "buzz.register_namespace")
}

// An aliased import checks against the types declared when it starts, so a file that
// reaches magus only through one needs the mirrors declared up front.
func TestBuzzRunner_AliasedImportReachingMagusSeesTheMirrors(t *testing.T) {
	ph := &scriptPhases{}
	dir := t.TempDir()
	writeBuzz(t, dir, "lib.buzz", `import "magus";
export fun language() > str {
    final opts: magus\DirsOptions = magus\DirsOptions{ language = "go" };
    return opts.language;
}
`)
	script := writeBuzz(t, dir, "main.buzz", "import \"std\";\nimport \"lib\" as lib;\nfun main() > void { std\\print(lib\\language()); }\n")

	stdout, _ := scriptTracedRun(t, scriptRunnerTraced(ph), script)

	assert.Contains(t, stdout, "go")
	assert.Contains(t, ph.names(), "buzz.register_namespace")
}

// The entry's `import "magus"` puts the magus types in the session, and a module it
// imports still names them only through an import of its own. A failure there is
// reported in the module's file.
func TestBuzzRunner_CheckFileMagusTypesNeedTheModulesOwnImport(t *testing.T) {
	for _, tc := range []struct {
		name, lib string
		wantErr   bool
	}{
		{name: "lib does not import magus", lib: "export fun f(o: magus\\DirsOptions?) > void {}\n", wantErr: true},
		{name: "lib imports magus", lib: "import \"magus\";\nexport fun f(o: magus\\DirsOptions?) > void {}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			lib := writeBuzz(t, dir, "lib.buzz", tc.lib)
			script := writeBuzz(t, dir, "main.buzz", "import \"magus\";\nimport \"lib\";\nfun g(o: magus\\DirsOptions?) > void { f(o); }\n")

			diags, err := scriptRunnerOver(&scriptOpens{}).CheckFile(t.Context(), script, false)
			require.NoError(t, err)
			if !tc.wantErr {
				assert.Empty(t, diags)
				return
			}
			require.Len(t, diags, 1)
			got := diags[0]
			assert.Contains(t, got.Msg, "no import binds magus")
			assert.Equal(t, buzz.Diagnostic{Line: 1, Col: got.Col, Code: got.Code, Msg: got.Msg, File: lib}, got)
		})
	}
}

// A magusfile's spell import resolves under --check, which reads the file by the
// loader's embedded rules without --embedded. A misspelled one still fails BZZ2001.
func TestBuzzRunner_CheckFileResolvesSpellImports(t *testing.T) {
	dir := t.TempDir()
	known := writeBuzz(t, dir, "known.buzz", "import \"magus\";\nimport \"magus/spell/go\";\nmagus\\project({\"spells\": [go]});\n")
	typo := writeBuzz(t, dir, "typo.buzz", "import \"magus/spell/gooo\";\n")
	r := scriptRunnerOver(&scriptOpens{})

	diags, err := r.CheckFile(t.Context(), known, false)
	require.NoError(t, err)
	assert.Empty(t, diags)

	diags, err = r.CheckFile(t.Context(), typo, false)
	require.NoError(t, err)
	require.Len(t, diags, 1)
	assert.Contains(t, diags[0].String(), "BZZ2001")
	assert.Contains(t, diags[0].String(), "magus/spell/gooo")
	require.Error(t, r.Check(t.Context(), []string{typo}, false))
}

func TestBuzzRunner_CheckFailsOnAWarning(t *testing.T) {
	dir := t.TempDir()
	clean := writeBuzz(t, dir, "clean.buzz", "fun main() > void {}\n")
	warned := writeBuzz(t, dir, "warned.buzz", "fun main() > void { var s = \"\"; foreach (p in [\"a\"]) { s = p + s; } }\n")
	r := scriptRunnerOver(&scriptOpens{})

	diags, err := r.CheckFile(t.Context(), warned, false)
	require.NoError(t, err)
	require.NotEmpty(t, diags)
	for _, d := range diags {
		require.Equal(t, buzz.SeverityWarning, d.Severity, d.String())
	}

	require.NoError(t, r.Check(t.Context(), []string{clean}, false))
	require.Error(t, r.Check(t.Context(), []string{warned}, false))
}

// The three shapes a caller has for handing a script its own argv, which is what lets
// one file serve several callers without an environment variable a shell has to set.
func TestBuzzRunner_ArgvReachesMain(t *testing.T) {
	path := writeBuzz(t, t.TempDir(), "argv.buzz",
		"import \"std\";\nexport fun main(args: [str]) > void { std\\print(\"argv={args}\"); }\n")

	for name, tc := range map[string]struct {
		rest, forwarded []string
		sep             bool
		want            string
	}{
		"after a separator":   {[]string{path}, []string{"--raw", "-x"}, true, "argv=[--raw, -x]"},
		"bare words":          {[]string{path, "one", "two"}, nil, false, "argv=[one, two]"},
		"none":                {[]string{path}, nil, false, "argv=[]"},
		"separator with none": {[]string{path}, nil, true, "argv=[]"},
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			stdout := testkit.CaptureStdout(t, func() {
				err = scriptRunnerOver(&scriptOpens{}).Run(t.Context(), "", interp.ScriptOptions{}, "", tc.rest, tc.forwarded, tc.sep)
			})
			require.NoError(t, err)
			assert.Contains(t, stdout, tc.want)
		})
	}
}

// BuzzRunnerTestSuite pins that every path after -t is a script to test, so a spell's test
// scope runs a file and its _test.buzz sibling, and that a test run is always recorded.
type BuzzRunnerTestSuite struct {
	suite.Suite
	dir     string
	records []interp.ScriptRecord
}

func TestBuzzRunnerTestSuite(t *testing.T) { suite.Run(t, new(BuzzRunnerTestSuite)) }

func (s *BuzzRunnerTestSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.records = nil
}

func (s *BuzzRunnerTestSuite) script(name, assertion string) string {
	return writeBuzz(s.T(), s.dir, name, fmt.Sprintf(`import "std";

test "%[1]s" {
    std\assert(%[2]s, message: "%[1]s");
}
`, name, assertion))
}

func (s *BuzzRunnerTestSuite) test(profile string, paths ...string) (string, error) {
	r := scriptRunnerOver(&scriptOpens{})
	r.Record = func(_ context.Context, _ string, rec interp.ScriptRecord) error {
		s.records = append(s.records, rec)
		return nil
	}
	var err error
	out := testkit.CaptureStdout(s.T(), func() { err = r.Test(s.T().Context(), s.dir, interp.ScriptOptions{}, profile, "", paths, nil) })
	return out, err
}

func (s *BuzzRunnerTestSuite) TestEveryScriptRunsItsTests() {
	out, err := s.test("", s.script("a.buzz", "true"), s.script("a_test.buzz", "true"))
	s.Require().NoError(err)
	s.Contains(out, `ok    test "a.buzz"`)
	s.Contains(out, `ok    test "a_test.buzz"`)
}

func (s *BuzzRunnerTestSuite) TestAFailingSecondScriptFailsTheRun() {
	out, err := s.test("", s.script("a.buzz", "true"), s.script("a_test.buzz", "false"))
	s.Require().Error(err)
	s.Contains(out, `FAIL  test "a_test.buzz"`)
}

func (s *BuzzRunnerTestSuite) TestAFailingFirstScriptStillRunsTheNext() {
	out, err := s.test("", s.script("a.buzz", "false"), s.script("a_test.buzz", "true"))
	s.Require().Error(err)
	s.Contains(out, `ok    test "a_test.buzz"`)
}

func (s *BuzzRunnerTestSuite) TestOneCoverprofileCoversEveryScript() {
	profile := filepath.Join(s.dir, "lcov.info")
	_, err := s.test(profile, s.script("a.buzz", "true"), s.script("b.buzz", "true"))
	s.Require().NoError(err)
	data, err := os.ReadFile(profile)
	s.Require().NoError(err)
	s.Contains(string(data), "a.buzz")
	s.Contains(string(data), "b.buzz")
}

func (s *BuzzRunnerTestSuite) TestForwardedArgsNeedASingleScript() {
	r := scriptRunnerOver(&scriptOpens{})
	err := r.Test(s.T().Context(), s.dir, interp.ScriptOptions{}, "", "", []string{s.script("a.buzz", "true"), s.script("b.buzz", "true")}, []string{"x"})
	s.Require().Error(err)
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *BuzzRunnerTestSuite) TestATestRunIsRecordedWithItsTestLines() {
	a := s.script("a.buzz", "true")
	b := s.script("b.buzz", "false")
	_, err := s.test("", a, b)
	s.Require().Error(err)

	s.Require().Len(s.records, 2)
	s.True(s.records[0].Test)
	s.Require().NoError(s.records[0].Err)
	s.Contains(string(s.records[0].Out), `ok    test "a.buzz"`)
	s.Contains(string(s.records[0].Out), "1 passed, 0 failed, 0 skipped")
	s.Require().Error(s.records[1].Err)
	s.Contains(string(s.records[1].Out), `FAIL  test "b.buzz"`)
	s.Contains(string(s.records[1].Out), "0 passed, 1 failed, 0 skipped")
}

func (s *BuzzRunnerTestSuite) TestARecordedRunWithNoRecorderFails() {
	path := writeBuzz(s.T(), s.dir, "p.buzz", buzzVanillaScript)
	r := scriptRunnerOver(&scriptOpens{})
	r.Record = nil
	var err error
	testkit.CaptureStdout(s.T(), func() {
		err = r.Run(s.T().Context(), s.dir, interp.ScriptOptions{Record: true}, "", []string{path}, nil, false)
	})
	s.Require().Error(err)
	s.Contains(err.Error(), "--record")
}
