package run

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
	res, err := Exec(context.Background(), "sh", []string{"-c", `printf %s "$MAGUS"`}, ExecOptions{Capture: true})
	require.NoError(t, err)
	want, err := os.Executable()
	require.NoError(t, err)
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	assert.Equal(t, want, res.Stdout, "$MAGUS in subprocess")
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
		res, err := Exec(context.Background(), "sh", []string{"-c", `printf %s "$MAGUS_LEVEL"`}, ExecOptions{Capture: true})
		require.NoError(t, err)
		return res.Stdout
	}

	// Top level: MAGUS_LEVEL absent (empty ⇒ 0) ⇒ child runs at depth 1.
	t.Setenv("MAGUS_LEVEL", "")
	assert.Equal(t, "1", level(t), "MAGUS_LEVEL at top")
	// Nested: depth 2 ⇒ child runs at depth 3.
	t.Setenv("MAGUS_LEVEL", "2")
	assert.Equal(t, "3", level(t), "MAGUS_LEVEL when nested")
}

// TestCurrentLevel pins the contract startup relies on to decide whether to stand
// up its own daemon: absent/invalid ⇒ 0 (top-level, starts a server), > 0 ⇒
// nested (must not, to keep one socket / one pool). Mutates env; not parallel.
func TestCurrentLevel(t *testing.T) {
	t.Setenv("MAGUS_LEVEL", "")
	assert.Equal(t, 0, CurrentLevel(), "top-level CurrentLevel")
	t.Setenv("MAGUS_LEVEL", "2")
	assert.Equal(t, 2, CurrentLevel(), "nested CurrentLevel")
	t.Setenv("MAGUS_LEVEL", "not-a-number")
	assert.Equal(t, 0, CurrentLevel(), "invalid CurrentLevel")
}

func TestRunSuccess(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("'true' not available")
	}
	assert.NoError(t, Run(context.Background(), t.TempDir(), "true"))
}

func TestRunFailure(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("'false' not available")
	}
	assert.Error(t, Run(context.Background(), t.TempDir(), "false"), "want non-nil exit error")
}

func TestRunContextCancel(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("'sleep' not available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.Error(t, Run(ctx, t.TempDir(), "sleep", "60"), "Run with cancelled context should return an error")
}
