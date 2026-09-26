package std

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/proc/run"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/sandbox/filesystem"
	buzzstd "github.com/egladman/magus/libs/gopherbuzz/std"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOsExecTeesToOutputWriters verifies proc.exec sends output through the run's
// output writers (reusing the CLI's live+cached-log sink) while still capturing
// it into the returned object.
func TestOsExecTeesToOutputWriters(t *testing.T) {
	var tee bytes.Buffer
	ctx := run.WithOutputWriters(context.Background(), &tee, &tee)
	res, err := OsExec(ctx, "printf", []string{"hello"}, "", nil)
	require.NoError(t, err)
	got := res.Stdout
	assert.Equal(t, "hello", got)
	assert.Contains(t, tee.String(), "hello", "output was not teed to the run writer")
}

// TestOsExecStdin verifies opts.stdin is fed to the process: the plumbing under
// pipe-style chaining (a prior call's stdout becomes the next call's stdin).
func TestOsExecStdin(t *testing.T) {
	res, err := OsExec(context.Background(), "cat", nil, "", map[string]any{"stdin": "piped-input"})
	require.NoError(t, err)
	got := res.Stdout
	assert.Equal(t, "piped-input", got, "stdin not delivered")
}

func TestLooksLikeShellCommand(t *testing.T) {
	shellish := []string{
		"ls | grep foo",  // pipe
		"a && b",         // logical
		"echo hello",     // space (command line, not a program)
		"cat < in > out", // redirection
		"echo $HOME",     // variable expansion
		"rm *.tmp",       // glob (with space)
		"cd",             // shell builtin, no metachars
		"export",         // shell builtin
		"foo; bar",       // sequence
	}
	for _, c := range shellish {
		assert.True(t, looksLikeShellCommand(c), "looksLikeShellCommand(%q) should be true", c)
	}

	plain := []string{"go", "gofmt", "docker", "golangci-lint", "/usr/bin/env", "my-tool"}
	for _, c := range plain {
		assert.False(t, looksLikeShellCommand(c), "looksLikeShellCommand(%q) should be false", c)
	}
}

func TestShellExe(t *testing.T) {
	// An override is used verbatim as the shell; the flag is /c only for cmd.
	assertShell := func(override, wantFlag string) {
		shell, flag := shellExe(override)
		assert.Equal(t, override, shell, "shellExe(%q) shell", override)
		assert.Equal(t, wantFlag, flag, "shellExe(%q) flag", override)
	}
	assertShell("bash", "-c")
	assertShell("/bin/bash", "-c")
	assertShell("sh", "-c")
	assertShell("zsh", "-c")
	assertShell("dash", "-c")
	assertShell("cmd", "/c")
	assertShell("cmd.exe", "/c")
	assertShell("CMD.EXE", "/c")

	// No override: platform default (never $SHELL), with a matching flag.
	shell, flag := shellExe("")
	assert.NotEmpty(t, shell, `shellExe("") shell should be non-empty default`)
	assert.NotEmpty(t, flag, `shellExe("") flag should be non-empty default`)
}

// TestOsExecResolvesCwd is the load-bearing test for the unified exec
// primitives: the working directory comes from the context (WithCwd) when no
// explicit dir is passed, and an explicit dir nests relative to that context
// cwd. It runs `pwd` (whose output is the resolved working directory) and
// checks where the subprocess actually ran.
func TestOsExecResolvesCwd(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "nested")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	// Resolve symlinks so the comparison holds on platforms (e.g. macOS) where
	// TempDir lives under a symlinked /var -> /private/var.
	baseReal, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	subReal := filepath.Join(baseReal, "nested")

	runPwd := func(ctxCwd, dirArg, wantDir string) {
		ctx := WithCwd(context.Background(), ctxCwd)
		res, err := OsExec(ctx, "pwd", nil, dirArg, nil)
		require.NoError(t, err)
		require.Equal(t, 0, res.Code, "pwd exit code")
		// exec trims stdout, so res.Stdout is the bare path.
		stdout := res.Stdout
		got, err := filepath.EvalSymlinks(stdout)
		require.NoError(t, err, "EvalSymlinks(%q)", stdout)
		assert.Equal(t, wantDir, got, "pwd ran in wrong dir")
	}

	t.Run("context cwd, no dir arg", func(t *testing.T) {
		runPwd(base, "", baseReal)
	})
	t.Run("context cwd + relative nested dir", func(t *testing.T) {
		runPwd(base, "nested", subReal)
	})
	t.Run("explicit absolute dir wins", func(t *testing.T) {
		runPwd(base, sub, subReal)
	})
	t.Run("no context cwd, explicit dir", func(t *testing.T) {
		runPwd("", sub, subReal)
	})
}

// cbFunc adapts a plain func to the host.Callback interface for tests.
type cbFunc func(context.Context) error

func (f cbFunc) Call(ctx context.Context, _ ...any) ([]any, error) { return nil, f(ctx) }

func TestOsWithSlots(t *testing.T) {
	lim := cache.NewLimiter(4)
	ctx := cache.ContextWithLimiter(context.Background(), lim)

	ran := false
	err := OsWithSlots(ctx, 3, cbFunc(func(context.Context) error {
		ran = true
		assert.Equal(t, 3, lim.Snapshot().Running, "Running during with_slots")
		return nil
	}))
	require.NoError(t, err)
	assert.True(t, ran, "callback did not run")
	assert.Equal(t, 0, lim.Snapshot().Running, "Running after with_slots (slots not released)")
}

// With a held build slot, with_slots hands it back while it reserves n, so peak
// in-flight is n (not n+1).
func TestOsWithSlotsGivesBackHeldSlot(t *testing.T) {
	lim := cache.NewLimiter(3)
	require.NoError(t, lim.Acquire(context.Background())) // simulate the held build slot
	ctx := cache.WithSlotHeld(cache.ContextWithLimiter(context.Background(), lim))

	err := OsWithSlots(ctx, 3, cbFunc(func(context.Context) error {
		assert.Equal(t, 3, lim.Snapshot().Running, "Running during with_slots (held slot should be handed back)")
		return nil
	}))
	require.NoError(t, err)
	// The held slot is reacquired on return.
	assert.Equal(t, 1, lim.Snapshot().Running, "Running after with_slots (held slot reacquired)")
}

// The step's own pool was sized to the slots with_slots hands back, so the callback's
// processes get a pool of n, or none at all when n is one.
func TestOsWithSlotsReseatsTheJobserver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows has no pipe jobserver: exec.Cmd cannot hand a child extra descriptors there")
	}
	lim := cache.NewLimiter(4)
	require.NoError(t, lim.AcquireN(context.Background(), 4))
	stepCtx, closeStep, err := run.SeatJobserver(cache.ContextWithLimiter(context.Background(), lim), 4)
	require.NoError(t, err)
	defer closeStep()
	stepCtx = cache.WithSlotsHeld(stepCtx, 4)

	// Reads one token only when the pool should hold one: a read on a pool with none
	// blocks for as long as the test runs.
	probe := func(n int, script string) string {
		var got string
		err := OsWithSlots(stepCtx, n, cbFunc(func(ctx context.Context) error {
			res, err := run.Exec(ctx, "sh", []string{"-c", script}, run.ExecOptions{Capture: true, Quiet: true})
			got = res.Stdout
			return err
		}))
		require.NoError(t, err)
		return got
	}
	assert.Equal(t, "-j --jobserver-fds=3,4 --jobserver-auth=3,4|+", probe(2, `printf '%s|' "$CARGO_MAKEFLAGS"; head -c 1 <&3`))
	assert.Equal(t, os.Getenv("CARGO_MAKEFLAGS")+"|", probe(1, `printf '%s|' "$CARGO_MAKEFLAGS"`))
}

func TestOsWithSlotsNoLimiter(t *testing.T) {
	ran := false
	err := OsWithSlots(context.Background(), 4, cbFunc(func(context.Context) error {
		ran = true
		return nil
	}))
	require.NoError(t, err)
	assert.True(t, ran, "callback did not run without a limiter")
}

func TestFsExt(t *testing.T) {
	got, err := FsExt(context.Background(), "a/b/c.tar.gz")
	require.NoError(t, err)
	assert.Equal(t, ".gz", got)
}

func TestFsIsDirIsFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(file, []byte("hi"), 0o644))

	ok, _ := FsIsDir(ctx, dir)
	assert.True(t, ok, "is_dir(dir) should be true")
	ok, _ = FsIsDir(ctx, file)
	assert.False(t, ok, "is_dir(file) should be false")
	ok, _ = FsIsFile(ctx, file)
	assert.True(t, ok, "is_file(file) should be true")
	ok, _ = FsIsFile(ctx, dir)
	assert.False(t, ok, "is_file(dir) should be false")
	// A missing path is reported as neither, without error.
	ok, err := FsIsDir(ctx, filepath.Join(dir, "nope"))
	assert.False(t, ok, "is_dir(missing) should be false")
	assert.NoError(t, err, "is_dir(missing) should not error")
}

func TestFsStat(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	require.NoError(t, os.WriteFile(file, []byte("hello"), 0o640))

	st, err := FsStat(ctx, file)
	require.NoError(t, err)
	assert.Equal(t, int64(5), st.Size)
	assert.False(t, st.IsDir, "is_dir should be false")
	assert.Equal(t, int64(0o640), st.Mode)
	assert.NotZero(t, st.Mtime, "mtime should be set")

	_, err = FsStat(ctx, filepath.Join(dir, "missing"))
	assert.Error(t, err, "stat of a missing path should error")
}

func TestFsCopyFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	require.NoError(t, os.WriteFile(src, []byte("payload"), 0o644))
	require.NoError(t, FsCopyFile(ctx, src, dst))
	got, err := os.ReadFile(dst)
	require.NoError(t, err)
	assert.Equal(t, "payload", string(got))
}

func TestFsCopyDir(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0o644))

	require.NoError(t, FsCopyDir(ctx, src, dst))
	for rel, want := range map[string]string{
		"a.txt":     "a",
		"sub/b.txt": "b",
	} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		require.NoError(t, err, "read %s", rel)
		assert.Equal(t, want, string(got), rel)
	}
}

// TestFsCopyDirChecksReadOnDeniedSubtree pins that a source directory the sandbox
// denies read on stops the copy: it must not be silently enumerated and have its
// layout recreated in dest. The bug: the WalkDir callback only called checkRead
// for FILE entries, never for a directory entry (nor the src root itself), so a
// src tree with no files at all (just a denied subdirectory) copied "clean"
// with no error and no read check ever firing.
func TestFsCopyDirChecksReadOnDeniedSubtree(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	require.NoError(t, os.MkdirAll(filepath.Join(src, "secret"), 0o755))

	// Grants write under dst but no read rule covers src at all, not even a
	// directory-shaped one. Rule.Path must be pre-normalized the way policy-build
	// time does (ResolveRulePath), or the write grant silently fails to match on a
	// machine whose TMPDIR sits under a symlink (macOS: /var -> /private/var).
	p := &sandbox.Policy{
		FS: filesystem.Ruleset{Rules: []filesystem.Rule{
			{Path: filesystem.ResolveRulePath(dst), Write: true},
		}},
	}
	ctx := sandbox.WithPolicy(context.Background(), p)

	err := FsCopyDir(ctx, src, dst)
	require.Error(t, err, "a src tree with no read grant at all must not copy silently")

	_, statErr := os.Stat(filepath.Join(dst, "secret"))
	assert.True(t, os.IsNotExist(statErr), "dst/secret should never have been created for a read-denied source")
}

func TestEnvExpand(t *testing.T) {
	t.Setenv("MAGUS_EXTRA_TEST", "world")
	got, err := EnvExpand(context.Background(), "hello $MAGUS_EXTRA_TEST ${MAGUS_EXTRA_TEST}")
	require.NoError(t, err)
	assert.Equal(t, "hello world world", got)
}

func TestEnvUnset(t *testing.T) {
	t.Setenv("MAGUS_EXTRA_UNSET", "x")
	require.NoError(t, EnvUnset(context.Background(), "MAGUS_EXTRA_UNSET"))
	_, ok := os.LookupEnv("MAGUS_EXTRA_UNSET")
	assert.False(t, ok, "env.unset did not remove the variable")
}

func TestEnvHome(t *testing.T) {
	got, err := EnvHome(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, got, "env.home returned empty")
}

func TestOsNumCPU(t *testing.T) {
	got, err := OsNumCPU(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, got, 1, "num_cpu should be >= 1")
}

func TestOsHostname(t *testing.T) {
	got, err := OsHostname(context.Background())
	require.NoError(t, err)
	assert.NotEmpty(t, got, "os.hostname returned empty")
}

// os.executable exists to be paired with fs.stat by a long-lived watcher, so the
// contract that matters is not just "returns a string" but "returns a path fs.stat
// can actually resolve into a usable stamp". Assert the pairing, not the string.
func TestOsExecutable(t *testing.T) {
	ctx := context.Background()

	exe, err := OsExecutable(ctx)
	require.NoError(t, err)
	require.NotEmpty(t, exe, "os.executable returned empty")
	assert.True(t, filepath.IsAbs(exe), "os.executable must return an absolute path, got %q", exe)

	// The whole point: fs.stat must succeed on it and yield a non-zero mtime and size,
	// because a stamp built from zero values could never detect a rebuild.
	info, err := FsStat(ctx, exe)
	require.NoError(t, err, "fs.stat could not resolve os.executable's path %q", exe)
	assert.Positive(t, info.Size, "stat size must be non-zero to be usable as a staleness stamp")
	assert.Positive(t, info.Mtime, "stat mtime must be non-zero to be usable as a staleness stamp")
	assert.False(t, info.IsDir, "os.executable must not be a directory")

	// Symlinks are resolved, so calling twice is stable: a stamp that changed on its
	// own would make the watcher's tripwire fire spuriously on every tick.
	again, err := OsExecutable(ctx)
	require.NoError(t, err)
	assert.Equal(t, exe, again, "os.executable must be stable across calls")
}

// FsStat's Buzz-boundary map is what a magusfile actually reads, and `size` collides
// with a built-in map method there, so a magusfile must use st["size"] rather than
// st.size. Lock the map's key set so the field names the serve tripwire depends on
// cannot be renamed out from under it.
func TestFsStatMapKeys(t *testing.T) {
	// Relative to the package dir, which is the test's working directory.
	info, err := FsStat(context.Background(), "os.go")
	require.NoError(t, err)

	// The KEY names are pinned where the encoder lives, in
	// internal/interp/bindings/gen's TestObjectKeys; std cannot import that package.
	// What this asserts is that stat fills the fields those keys are derived from.
	require.NotZero(t, info.Mtime, "serve's staleness stamp reads the mtime")
	require.NotZero(t, info.Size, "serve's staleness stamp reads the size")
	assert.NotZero(t, info.Mode)
	assert.False(t, info.IsDir)
}

// watchCallback adapts a Go func(changed) (stop bool) to the host.Callback the
// fs.watch binding hands FsWatch.
type watchCallback struct {
	fn func(changed []string) bool
}

func (c watchCallback) Call(_ context.Context, args ...any) ([]any, error) {
	var changed []string
	if len(args) > 0 {
		changed, _ = args[0].([]string)
	}
	return []any{c.fn(changed)}, nil
}

func TestFsWatchRequiresPaths(t *testing.T) {
	t.Parallel()
	err := FsWatch(context.Background(), nil, watchCallback{fn: func([]string) bool { return true }})
	assert.Error(t, err, "FsWatch with no paths should error")
}

// TestFsWatchFiresCallbackAndStops drives FsWatch end-to-end: a real file change
// must reach the callback with a non-empty change set, and returning true must
// make the blocking call return nil.
func TestFsWatchFiresCallbackAndStops(t *testing.T) {
	dir := t.TempDir()
	fired := make(chan []string, 1)
	cb := watchCallback{fn: func(changed []string) bool {
		select {
		case fired <- changed:
		default:
		}
		return true // stop after the first batch
	}}

	done := make(chan error, 1)
	go func() { done <- FsWatch(context.Background(), []string{dir}, cb) }()

	// Poke the tree until the callback fires, which sidesteps the watcher's
	// arm-up race. Space the writes wider than the 200ms debounce so a batch
	// actually settles between them (continuous writes would keep resetting it).
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("f%d.go", i)), []byte("package p\n"), 0o644)
			time.Sleep(500 * time.Millisecond)
		}
	}()

	select {
	case changed := <-fired:
		assert.NotEmpty(t, changed, "callback received an empty change set")
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the watch callback")
	}

	select {
	case err := <-done:
		assert.NoError(t, err, "FsWatch should return nil after the callback asked to stop")
	case <-time.After(5 * time.Second):
		t.Fatal("FsWatch did not return after the callback asked to stop")
	}
}

func TestCallbackTruthy(t *testing.T) {
	t.Parallel()
	assert.False(t, callbackTruthy(nil), "nil should be falsy")
	assert.False(t, callbackTruthy([]any{nil}), "[nil] should be falsy")
	assert.False(t, callbackTruthy([]any{false}), "[false] should be falsy")
	assert.True(t, callbackTruthy([]any{true}), "[true] should be truthy")
	assert.True(t, callbackTruthy([]any{"non-bool is truthy"}), "[non-bool] should be truthy")
}

func TestRelToCwd(t *testing.T) {
	t.Parallel()
	base := filepath.FromSlash("/a/b")
	got := relToCwd(base, []string{
		filepath.FromSlash("/a/b/sub/x.go"),
		filepath.FromSlash("/a/b/y.go"),
	})
	want := []string{filepath.FromSlash("sub/x.go"), "y.go"}
	assert.Equal(t, want, got)
}

// TestWithCwdPropagatesToBuzzStdlib verifies WithCwd sets the run cwd for BOTH
// magus's exec primitives and Buzz's own stdlib (gopherbuzz io/fs/os), so a
// magusfile using the language built-ins (io.File, fs.list, os.execute) resolves
// relative paths against the project dir. Dropping the buzzstd.WithCwd bridge would
// silently regress that (e.g. `magus run serve docs` reading ../README.md).
func TestWithCwdPropagatesToBuzzStdlib(t *testing.T) {
	dir := t.TempDir()
	ctx := WithCwd(context.Background(), dir)

	got, ok := CwdFromContext(ctx)
	require.True(t, ok)
	assert.Equal(t, dir, got, "magus cwd")

	buzzCwd, ok := buzzstd.CwdFromContext(ctx)
	require.True(t, ok, "WithCwd must propagate to Buzz's stdlib (bridge dropped?)")
	assert.Equal(t, dir, buzzCwd, "buzz stdlib cwd")

	// An empty dir is a no-op for both surfaces.
	base := context.Background()
	if _, ok := CwdFromContext(WithCwd(base, "")); ok {
		t.Fatal(`WithCwd("") set a magus cwd, want none`)
	}
	if _, ok := buzzstd.CwdFromContext(WithCwd(base, "")); ok {
		t.Fatal(`WithCwd("") set a buzz cwd, want none`)
	}
}

// TestExecCancelledReportsCancellation pins the reporting fix for a cascade. A cancelled
// run kills in-flight children, ExitCode() is -1 for a signalled process, and rendering
// that as "exit -1" made one real failure look like several unrelated ones.
func TestExecCancelledReportsCancellation(t *testing.T) {
	// Cancel AFTER the child is running, which is the shape that produced the confusing
	// output: an already-cancelled context never starts the process and takes a different
	// branch, so it would not exercise this at all.
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	_, err := OsExec(ctx, "sleep", []string{"30"}, ".", nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled, "a cancelled run must say so")
	assert.NotContains(t, err.Error(), "exit -1",
		"exit -1 is the signal-kill artifact, not the reason the command failed")
}

// TestExecSignalKilledNamesTheSignal covers the case that produced an unexplained CI
// job: a child killed by SIGKILL (the OOM killer's weapon, and uncatchable) reported
// only "exit -1", so the console said nothing about why the run ended.
func TestExecSignalKilledNamesTheSignal(t *testing.T) {
	// Kill the child from outside with SIGKILL, leaving the context alone, so this is
	// the OOM shape rather than the cancellation one covered above. The child is found by
	// the pid it writes, since finding it by name reads other processes' /proc entries,
	// which a sandboxed test may not.
	pidFile := filepath.Join(t.TempDir(), "pid")
	go func() {
		for t.Context().Err() == nil {
			if b, err := os.ReadFile(pidFile); err == nil {
				if pid, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
					if p, err := os.FindProcess(pid); err == nil {
						_ = p.Kill()
					}
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// The rename publishes the pid whole, and the exec keeps it for sleep.
	_, err := OsExec(context.Background(), "sh", []string{"-c", `echo $$ > "$0.new" && mv "$0.new" "$0" && exec sleep 31`, pidFile}, ".", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "signal: killed", "the signal is the whole diagnosis")
	assert.Contains(t, err.Error(), "OOM killer", "say what usually does this on CI")
	assert.NotContains(t, err.Error(), "exit -1")
}

// quiet is read in runResult, the one path proc.exec, proc.shell, and vcs.cmd share, so
// the three cannot drift into offering different option sets. Capture stays on
// regardless: a quiet call is still consuming the value, just not echoing it.
func TestOsExecQuietStillCaptures(t *testing.T) {
	res, err := OsExec(context.Background(), "sh", []string{"-c", "echo captured"}, "",
		map[string]any{"quiet": true})
	require.NoError(t, err)
	assert.Equal(t, "captured", res.Stdout, "proc.exec trims captured output")
	assert.Equal(t, 0, res.Code)
}

func TestProcShellQuietStillCaptures(t *testing.T) {
	c, err := OsShell(context.Background(), "echo captured", "")
	require.NoError(t, err)
	res, err := OsExec(context.Background(), c.Bin, c.Args, "", map[string]any{"quiet": true})
	require.NoError(t, err)
	assert.Equal(t, "captured", res.Stdout, "proc.exec trims captured output")
}

// Absent quiet must keep the streaming default. The option is opt-in because a build
// log that silently stopped showing command output would be a worse regression than
// any noise it removed.
func TestOsExecDefaultsToNotQuiet(t *testing.T) {
	res, err := OsExec(context.Background(), "sh", []string{"-c", "echo shown"}, "", nil)
	require.NoError(t, err)
	assert.Equal(t, "shown", res.Stdout)
}

// covRetryCallback fails its first failures attempts and then answers with ret.
type covRetryCallback struct {
	calls    int
	failures int
	ret      []any
	err      error
}

func (c *covRetryCallback) Call(context.Context, ...any) ([]any, error) {
	c.calls++
	if c.calls <= c.failures {
		if c.err != nil {
			return nil, c.err
		}
		return nil, errors.New("attempt failed")
	}
	return c.ret, nil
}

// covFastRetry keeps the backoff below a millisecond so exercising the retry loop
// costs no wall clock.
var covFastRetry = map[string]any{"backoff_ms": 0.1, "max_backoff_ms": 1.0}

func TestOsRetryReturnsTheFirstSuccess(t *testing.T) {
	cb := &covRetryCallback{ret: []any{"answer"}}
	got, err := OsRetry(context.Background(), 3, cb, nil)
	require.NoError(t, err)
	assert.Equal(t, "answer", got)
	assert.Equal(t, 1, cb.calls, "a success must not be retried")
}

func TestOsRetryRetriesUntilItSucceeds(t *testing.T) {
	cb := &covRetryCallback{failures: 2, ret: []any{42}}
	got, err := OsRetry(context.Background(), 5, cb, covFastRetry)
	require.NoError(t, err)
	assert.Equal(t, 42, got)
	assert.Equal(t, 3, cb.calls)
}

// TestOsRetryWithNoReturnValue: a callback that returns nothing succeeded, so the
// result is the no-value result rather than an error.
func TestOsRetryWithNoReturnValue(t *testing.T) {
	got, err := OsRetry(context.Background(), 2, &covRetryCallback{}, nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestOsRetryExhaustsAndReportsTheLastError(t *testing.T) {
	boom := errors.New("still broken")
	cb := &covRetryCallback{failures: 99, err: boom}

	_, err := OsRetry(context.Background(), 3, cb, covFastRetry)
	require.Error(t, err)
	assert.ErrorIs(t, err, boom, "the caller needs the reason, not just the count")
	assert.Contains(t, err.Error(), "os.retry: 3 attempt(s)")
	assert.Equal(t, 3, cb.calls)
}

func TestOsRetryRejectsANilCallback(t *testing.T) {
	_, err := OsRetry(context.Background(), 3, nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fn must not be nil")
}

// TestOsRetryReadsIntegerBackoffOptions: the VM hands numbers over as float64,
// but a Go caller (and a hand-built map) may pass int or int64, so all three are
// accepted and anything else falls back to the default.
func TestOsRetryReadsIntegerBackoffOptions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		opts     map[string]any
		failures int
	}{
		{"int", map[string]any{"backoff_ms": 1, "max_backoff_ms": 2}, 1},
		{"int64", map[string]any{"backoff_ms": int64(1), "max_backoff_ms": int64(2)}, 1},
		// A value of the wrong type falls back to the 500ms default, so this case
		// must not retry: the point is that the option is READ without erroring.
		{"unusable values fall back to the defaults", map[string]any{"backoff_ms": "not a number", "max_backoff_ms": nil}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cb := &covRetryCallback{failures: tc.failures, ret: []any{"ok"}}
			got, err := OsRetry(context.Background(), 2, cb, tc.opts)
			require.NoError(t, err)
			assert.Equal(t, "ok", got)
		})
	}
}

// TestOsRetryHonorsCancellationDuringBackoff: an interrupted run must wake out of
// the backoff sleep rather than serving the full delay.
func TestOsRetryHonorsCancellationDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	cb := &covRetryCallback{failures: 99}
	_, err := OsRetry(ctx, 3, cb, map[string]any{"backoff_ms": 60000.0})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, cb.calls, "the cancellation must land in the first backoff")
}

// TestOsWithEnv: the overrides ride the context so subprocesses started inside the
// callback inherit them. The process's own environment is never touched, which is
// what keeps a server serving other workspaces unaffected.
func TestOsWithEnv(t *testing.T) {
	var inner []string
	var nested []string

	err := OsWithEnv(context.Background(), map[string]string{"MAGUS_COV_WITH_ENV": "outer"},
		cbFunc(func(ctx context.Context) error {
			inner, _ = ctx.Value(withEnvKey{}).([]string)
			return OsWithEnv(ctx, map[string]string{"MAGUS_COV_WITH_ENV_INNER": "inner"},
				cbFunc(func(ctx context.Context) error {
					nested, _ = ctx.Value(withEnvKey{}).([]string)
					return nil
				}))
		}))
	require.NoError(t, err)

	assert.Equal(t, []string{"MAGUS_COV_WITH_ENV=outer"}, inner)
	assert.Equal(t, []string{"MAGUS_COV_WITH_ENV=outer", "MAGUS_COV_WITH_ENV_INNER=inner"}, nested,
		"a nested with_env merges onto the outer overrides rather than replacing them")

	_, ok := os.LookupEnv("MAGUS_COV_WITH_ENV")
	assert.False(t, ok, "with_env must never reach the process environment")
}

func TestOsWithEnvPropagatesTheCallbackError(t *testing.T) {
	boom := errors.New("callback failed")
	err := OsWithEnv(context.Background(), map[string]string{"A": "1"},
		cbFunc(func(context.Context) error { return boom }))
	assert.ErrorIs(t, err, boom)
}

func TestOsSleep(t *testing.T) {
	ctx := context.Background()

	start := time.Now()
	require.NoError(t, OsSleep(ctx, 0))
	require.NoError(t, OsSleep(ctx, -5))
	assert.Less(t, time.Since(start), 50*time.Millisecond, "a non-positive duration must not sleep")

	require.NoError(t, OsSleep(ctx, 1))

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	assert.ErrorIs(t, OsSleep(cancelled, 60000), context.Canceled)
	assert.Less(t, time.Since(start), time.Second,
		"an interrupted run wakes immediately rather than serving the full duration")
}

// TestOsExit does not exit: it returns an ExitError the engine maps to a process
// status, and records the code on ctx so it survives a VM that stringifies the
// error type away.
func TestOsExit(t *testing.T) {
	ctx, readExit := types.WithExitCapture(context.Background())

	err := OsExit(ctx, 3)
	var exitErr types.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 3, exitErr.Code)

	code, set := readExit()
	assert.True(t, set, "the code must survive out-of-band")
	assert.Equal(t, 3, code)

	// Outside a captured run it is a plain error and nothing records the code.
	err = OsExit(context.Background(), 1)
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.Code)
}

// TestOsExitClampsToAProcessStatus pins the clamp at the source, so the CLI, the server
// reply and the out-of-band capture cannot disagree. Unclamped, os.exit(256) truncated
// to 0 in os.Exit and a failing run reported success.
func TestOsExitClampsToAProcessStatus(t *testing.T) {
	ctx, readExit := types.WithExitCapture(context.Background())

	err := OsExit(ctx, 256)
	var exitErr types.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 1, exitErr.Code, "a failure that would truncate to 0 fails as 1")

	code, set := readExit()
	require.True(t, set)
	assert.Equal(t, 1, code, "the capture must agree with the error")
}

func TestOsPlatform(t *testing.T) {
	osName, arch, variant, err := OsPlatform(context.Background())
	require.NoError(t, err)

	wantOS, wantArch, wantVariant := HostPlatform()
	assert.Equal(t, wantOS, osName)
	assert.Equal(t, wantArch, arch)
	assert.Equal(t, wantVariant, variant)
	assert.NotEmpty(t, osName)
	assert.NotEmpty(t, arch)
}

func TestOsStdinIsTerminal(t *testing.T) {
	got, err := OsStdinIsTerminal(context.Background())
	require.NoError(t, err)
	assert.False(t, got, "the test binary's stdin is never a terminal")
}
