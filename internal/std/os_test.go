package std

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
