package std

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	buzzstd "github.com/egladman/gopherbuzz/std"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/run"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOsExecTeesToOutputWriters verifies os.exec sends output through the run's
// output writers (reusing the CLI's live+cached-log sink) while still capturing
// it into the returned record.
func TestOsExecTeesToOutputWriters(t *testing.T) {
	var tee bytes.Buffer
	ctx := run.WithOutputWriters(context.Background(), &tee, &tee)
	res, err := OsExec(ctx, "printf", []string{"hello"}, "", nil)
	require.NoError(t, err)
	got := res.Stdout
	assert.Equal(t, "hello", got)
	assert.Contains(t, tee.String(), "hello", "output was not teed to the run writer")
}

// TestOsExecStdin verifies opts.stdin is fed to the process — the plumbing under
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
	// TempDir lives under a symlinked /var → /private/var.
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
		assert.Equal(t, 3, lim.Snapshot().InUse, "InUse during with_slots")
		return nil
	}))
	require.NoError(t, err)
	assert.True(t, ran, "callback did not run")
	assert.Equal(t, 0, lim.Snapshot().InUse, "InUse after with_slots (slots not released)")
}

// With a held build slot, with_slots hands it back while it reserves n, so peak
// in-flight is n (not n+1).
func TestOsWithSlotsGivesBackHeldSlot(t *testing.T) {
	lim := cache.NewLimiter(3)
	require.NoError(t, lim.Acquire(context.Background())) // simulate the held build slot
	ctx := cache.WithSlotHeld(cache.ContextWithLimiter(context.Background(), lim))

	err := OsWithSlots(ctx, 3, cbFunc(func(context.Context) error {
		assert.Equal(t, 3, lim.Snapshot().InUse, "InUse during with_slots (held slot should be handed back)")
		return nil
	}))
	require.NoError(t, err)
	// The held slot is reacquired on return.
	assert.Equal(t, 1, lim.Snapshot().InUse, "InUse after with_slots (held slot reacquired)")
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

func TestJSONStringify(t *testing.T) {
	ctx := context.Background()
	val := map[string]any{"a": 1.0}

	// No indent → compact (single line).
	compact, err := JSONStringify(ctx, val, "")
	require.NoError(t, err)
	assert.NotContains(t, compact, "\n", "no-indent output should be compact")

	// A non-empty indent → pretty, multi-line with that indent.
	tabbed, err := JSONStringify(ctx, val, "\t")
	require.NoError(t, err)
	assert.Contains(t, tabbed, "\n", "indented output should be multi-line")
	assert.Contains(t, tabbed, "\t", "indented output should be tab-indented")
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
// silently regress that (e.g. `magus run serve website` reading ../README.md).
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
