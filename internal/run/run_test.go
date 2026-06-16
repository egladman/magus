package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestExecInjectsMAGUS pins that Exec exports MAGUS — the running binary's
// resolved path — into the subprocess environment, so a spell or recipe can
// re-invoke magus via "${MAGUS:-magus}" without relying on PATH (the GNU Make
// $(MAKE) convention). Here the "running binary" is the test executable.
func TestExecInjectsMAGUS(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	res, err := Exec(context.Background(), "sh", []string{"-c", `printf %s "$MAGUS"`}, ExecSpec{Capture: true})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	want, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable: %v", err)
	}
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	if res.Stdout != want {
		t.Errorf("$MAGUS in subprocess = %q, want %q", res.Stdout, want)
	}
}

// TestExecInjectsMagusLevel pins the GNU Make MAKELEVEL semantics: a subprocess
// sees MAGUS_LEVEL = this process's depth + 1, so the counter climbs by one per
// magus process (top-level, with MAGUS_LEVEL unset, is depth 0). Not parallel: it
// mutates the process env via t.Setenv.
func TestExecInjectsMagusLevel(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("'sh' not available")
	}
	level := func(t *testing.T) string {
		t.Helper()
		res, err := Exec(context.Background(), "sh", []string{"-c", `printf %s "$MAGUS_LEVEL"`}, ExecSpec{Capture: true})
		if err != nil {
			t.Fatalf("Exec: %v", err)
		}
		return res.Stdout
	}

	// Top level: MAGUS_LEVEL absent (empty ⇒ 0) ⇒ child runs at depth 1.
	t.Setenv("MAGUS_LEVEL", "")
	if got := level(t); got != "1" {
		t.Errorf("MAGUS_LEVEL at top = %q, want \"1\"", got)
	}
	// Nested: depth 2 ⇒ child runs at depth 3.
	t.Setenv("MAGUS_LEVEL", "2")
	if got := level(t); got != "3" {
		t.Errorf("MAGUS_LEVEL when nested = %q, want \"3\"", got)
	}
}

// TestCurrentLevel pins the contract startup relies on to decide whether to stand
// up its own daemon: absent/invalid ⇒ 0 (top-level, starts a server), > 0 ⇒
// nested (must not, to keep one socket / one pool). Mutates env; not parallel.
func TestCurrentLevel(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	if got := CurrentLevel(); got != 0 {
		t.Errorf("top-level CurrentLevel = %d, want 0", got)
	}
	t.Setenv("MAGUS_LEVEL", "2")
	if got := CurrentLevel(); got != 2 {
		t.Errorf("nested CurrentLevel = %d, want 2", got)
	}
	t.Setenv("MAGUS_LEVEL", "not-a-number")
	if got := CurrentLevel(); got != 0 {
		t.Errorf("invalid CurrentLevel = %d, want 0", got)
	}
}

func TestRunSuccess(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("'true' not available")
	}
	if err := Run(context.Background(), t.TempDir(), "true"); err != nil {
		t.Errorf("Run('true') = %v, want nil", err)
	}
}

func TestRunFailure(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("'false' not available")
	}
	if err := Run(context.Background(), t.TempDir(), "false"); err == nil {
		t.Error("Run('false') = nil, want non-nil exit error")
	}
}

func TestRunContextCancel(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("'sleep' not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(ctx, t.TempDir(), "sleep", "60")
	if err == nil {
		t.Error("Run with cancelled context should return an error")
	}
}
