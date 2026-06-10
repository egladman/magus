package std

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/run"
)

// TestOsExecTeesToOutputWriters verifies os.exec sends output through the run's
// output writers (reusing the CLI's live+cached-log sink) while still capturing
// it into the returned record.
func TestOsExecTeesToOutputWriters(t *testing.T) {
	var tee bytes.Buffer
	ctx := run.WithOutputWriters(context.Background(), &tee, &tee)
	res, err := OsExec(ctx, "printf", []string{"hello"}, "", nil)
	if err != nil {
		t.Fatalf("OsExec: %v", err)
	}
	if got, _ := res["stdout"].(string); got != "hello" {
		t.Errorf("captured stdout = %q, want %q", got, "hello")
	}
	if !strings.Contains(tee.String(), "hello") {
		t.Errorf("output was not teed to the run writer; tee = %q", tee.String())
	}
}

// TestOsExecStdin verifies opts.stdin is fed to the process — the plumbing under
// pipe-style chaining (a prior call's stdout becomes the next call's stdin).
func TestOsExecStdin(t *testing.T) {
	res, err := OsExec(context.Background(), "cat", nil, "", map[string]any{"stdin": "piped-input"})
	if err != nil {
		t.Fatalf("OsExec: %v", err)
	}
	if got, _ := res["stdout"].(string); got != "piped-input" {
		t.Errorf("stdin not delivered: stdout = %q, want %q", got, "piped-input")
	}
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
		if !looksLikeShellCommand(c) {
			t.Errorf("looksLikeShellCommand(%q) = false, want true", c)
		}
	}

	plain := []string{"go", "gofmt", "docker", "golangci-lint", "/usr/bin/env", "my-tool"}
	for _, c := range plain {
		if looksLikeShellCommand(c) {
			t.Errorf("looksLikeShellCommand(%q) = true, want false", c)
		}
	}
}

func TestShellExe(t *testing.T) {
	// An override is used verbatim as the shell; the flag is /c only for cmd.
	cases := []struct{ override, wantFlag string }{
		{"bash", "-c"},
		{"/bin/bash", "-c"},
		{"sh", "-c"},
		{"zsh", "-c"},
		{"dash", "-c"},
		{"cmd", "/c"},
		{"cmd.exe", "/c"},
		{"CMD.EXE", "/c"},
	}
	for _, tc := range cases {
		shell, flag := shellExe(tc.override)
		if shell != tc.override {
			t.Errorf("shellExe(%q) shell = %q, want %q", tc.override, shell, tc.override)
		}
		if flag != tc.wantFlag {
			t.Errorf("shellExe(%q) flag = %q, want %q", tc.override, flag, tc.wantFlag)
		}
	}
	// No override: platform default (never $SHELL), with a matching flag.
	if shell, flag := shellExe(""); shell == "" || flag == "" {
		t.Errorf(`shellExe("") = (%q, %q), want non-empty defaults`, shell, flag)
	}
}

// TestOsExecResolvesCwd is the load-bearing test for the unified exec
// primitives: the working directory comes from the context (WithCwd) when no
// explicit dir is passed, and an explicit dir nests relative to that context
// cwd. It runs `pwd` (whose output is the resolved working directory) and
// checks where the subprocess actually ran.
func TestOsExecResolvesCwd(t *testing.T) {
	base := t.TempDir()
	sub := filepath.Join(base, "nested")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Resolve symlinks so the comparison holds on platforms (e.g. macOS) where
	// TempDir lives under a symlinked /var → /private/var.
	baseReal, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	subReal := filepath.Join(baseReal, "nested")

	cases := []struct {
		name    string
		ctxCwd  string // WithCwd value ("" = unset)
		dirArg  string // explicit trailing dir ("" = omitted)
		wantDir string
	}{
		{"context cwd, no dir arg", base, "", baseReal},
		{"context cwd + relative nested dir", base, "nested", subReal},
		{"explicit absolute dir wins", base, sub, subReal},
		{"no context cwd, explicit dir", "", sub, subReal},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithCwd(context.Background(), tc.ctxCwd)
			res, err := OsExec(ctx, "pwd", nil, tc.dirArg, nil)
			if err != nil {
				t.Fatalf("OsExec: %v", err)
			}
			if code := res["code"].(int); code != 0 {
				t.Fatalf("pwd exit code = %d", code)
			}
			// exec trims stdout, so res["stdout"] is the bare path.
			stdout := res["stdout"].(string)
			got, err := filepath.EvalSymlinks(stdout)
			if err != nil {
				t.Fatalf("EvalSymlinks(%q): %v", stdout, err)
			}
			if got != tc.wantDir {
				t.Errorf("pwd ran in %q, want %q", got, tc.wantDir)
			}
		})
	}
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
		if got := lim.Snapshot().InUse; got != 3 {
			t.Errorf("InUse during with_slots = %d, want 3", got)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("OsWithSlots: %v", err)
	}
	if !ran {
		t.Fatal("callback did not run")
	}
	if got := lim.Snapshot().InUse; got != 0 {
		t.Errorf("InUse after with_slots = %d, want 0 (slots not released)", got)
	}
}

// With a held build slot, with_slots hands it back while it reserves n, so peak
// in-flight is n (not n+1).
func TestOsWithSlotsGivesBackHeldSlot(t *testing.T) {
	lim := cache.NewLimiter(3)
	if err := lim.Acquire(context.Background()); err != nil { // simulate the held build slot
		t.Fatal(err)
	}
	ctx := cache.WithSlotHeld(cache.ContextWithLimiter(context.Background(), lim))

	err := OsWithSlots(ctx, 3, cbFunc(func(context.Context) error {
		if got := lim.Snapshot().InUse; got != 3 {
			t.Errorf("InUse during with_slots = %d, want 3 (held slot should be handed back)", got)
		}
		return nil
	}))
	if err != nil {
		t.Fatalf("OsWithSlots: %v", err)
	}
	// The held slot is reacquired on return.
	if got := lim.Snapshot().InUse; got != 1 {
		t.Errorf("InUse after with_slots = %d, want 1 (held slot reacquired)", got)
	}
}

func TestOsWithSlotsNoLimiter(t *testing.T) {
	ran := false
	if err := OsWithSlots(context.Background(), 4, cbFunc(func(context.Context) error {
		ran = true
		return nil
	})); err != nil {
		t.Fatalf("OsWithSlots without a limiter: %v", err)
	}
	if !ran {
		t.Fatal("callback did not run without a limiter")
	}
}

func TestFsExt(t *testing.T) {
	got, err := FsExt(context.Background(), "a/b/c.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if want := ".gz"; got != want {
		t.Fatalf("ext = %q, want %q", got, want)
	}
}

func TestFsIsDirIsFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	if ok, _ := FsIsDir(ctx, dir); !ok {
		t.Error("is_dir(dir) = false, want true")
	}
	if ok, _ := FsIsDir(ctx, file); ok {
		t.Error("is_dir(file) = true, want false")
	}
	if ok, _ := FsIsFile(ctx, file); !ok {
		t.Error("is_file(file) = false, want true")
	}
	if ok, _ := FsIsFile(ctx, dir); ok {
		t.Error("is_file(dir) = true, want false")
	}
	// A missing path is reported as neither, without error.
	if ok, err := FsIsDir(ctx, filepath.Join(dir, "nope")); ok || err != nil {
		t.Errorf("is_dir(missing) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestFsStat(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o640); err != nil {
		t.Fatal(err)
	}

	st, err := FsStat(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	if got := st["size"].(int64); got != 5 {
		t.Errorf("size = %d, want 5", got)
	}
	if got := st["is_dir"].(bool); got {
		t.Error("is_dir = true, want false")
	}
	if got := st["mode"].(int64); got != 0o640 {
		t.Errorf("mode = %o, want 640", got)
	}
	if _, ok := st["mtime"].(float64); !ok {
		t.Errorf("mtime is %T, want float64", st["mtime"])
	}

	if _, err := FsStat(ctx, filepath.Join(dir, "missing")); err == nil {
		t.Error("stat of a missing path should error")
	}
}

func TestFsCopyFile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := FsCopyFile(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "payload" {
		t.Fatalf("copied content = %q, want %q", got, "payload")
	}
}

func TestFsCopyDir(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	if err := os.MkdirAll(filepath.Join(src, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "sub", "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := FsCopyDir(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]string{
		"a.txt":     "a",
		"sub/b.txt": "b",
	} {
		got, err := os.ReadFile(filepath.Join(dst, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
}

func TestJSONStringify(t *testing.T) {
	ctx := context.Background()
	val := map[string]any{"a": 1.0}

	// No indent → compact (single line).
	compact, err := JSONStringify(ctx, val, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(compact, "\n") {
		t.Fatalf("no-indent output should be compact, got:\n%s", compact)
	}

	// A non-empty indent → pretty, multi-line with that indent.
	tabbed, err := JSONStringify(ctx, val, "\t")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(tabbed, "\n") || !strings.Contains(tabbed, "\t") {
		t.Fatalf("indented output should be multi-line and tab-indented, got:\n%s", tabbed)
	}
}

func TestEnvExpand(t *testing.T) {
	t.Setenv("MAGUS_EXTRA_TEST", "world")
	got, err := EnvExpand(context.Background(), "hello $MAGUS_EXTRA_TEST ${MAGUS_EXTRA_TEST}")
	if err != nil {
		t.Fatal(err)
	}
	if want := "hello world world"; got != want {
		t.Fatalf("expand = %q, want %q", got, want)
	}
}

func TestEnvUnset(t *testing.T) {
	t.Setenv("MAGUS_EXTRA_UNSET", "x")
	if err := EnvUnset(context.Background(), "MAGUS_EXTRA_UNSET"); err != nil {
		t.Fatal(err)
	}
	if _, ok := os.LookupEnv("MAGUS_EXTRA_UNSET"); ok {
		t.Fatal("env.unset did not remove the variable")
	}
}

func TestEnvHome(t *testing.T) {
	got, err := EnvHome(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("env.home returned empty")
	}
}

func TestOsNumCPU(t *testing.T) {
	got, err := OsNumCPU(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got < 1 {
		t.Fatalf("num_cpu = %d, want >= 1", got)
	}
}

func TestOsHostname(t *testing.T) {
	got, err := OsHostname(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == "" {
		t.Fatal("os.hostname returned empty")
	}
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
	if err == nil {
		t.Fatal("FsWatch with no paths should error")
	}
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
		if len(changed) == 0 {
			t.Error("callback received an empty change set")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for the watch callback")
	}

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("FsWatch returned %v, want nil after the callback asked to stop", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("FsWatch did not return after the callback asked to stop")
	}
}

func TestCallbackTruthy(t *testing.T) {
	t.Parallel()
	cases := []struct {
		ret  []any
		want bool
	}{
		{nil, false},
		{[]any{nil}, false},
		{[]any{false}, false},
		{[]any{true}, true},
		{[]any{"non-bool is truthy"}, true},
	}
	for _, tc := range cases {
		if got := callbackTruthy(tc.ret); got != tc.want {
			t.Errorf("callbackTruthy(%v) = %v, want %v", tc.ret, got, tc.want)
		}
	}
}

func TestRelToCwd(t *testing.T) {
	t.Parallel()
	base := filepath.FromSlash("/a/b")
	got := relToCwd(base, []string{
		filepath.FromSlash("/a/b/sub/x.go"),
		filepath.FromSlash("/a/b/y.go"),
	})
	want := []string{filepath.FromSlash("sub/x.go"), "y.go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("relToCwd = %v, want %v", got, want)
	}
}
