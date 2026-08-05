package bindings

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A failing probe must stop the op BEFORE it forks, and say what is actually wrong.
// Without this the op ran, the tool failed on its own terms, and magus reported a
// build failure for a project with nothing wrong with it.
func TestReadinessGateBlocksOpAndCarriesCode(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Command{
		"faketool": {Bin: "sh", Args: []string{"-c", "exit 1"}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	err := checkReady(context.Background(), readiness, op, t.TempDir())
	require.Error(t, err)
	assert.True(t, errors.Is(err, types.ToolNotReady), "want MGS3004, got %v", err)
	assert.Contains(t, err.Error(), "faketool")
	assert.Contains(t, err.Error(), "not ready")
}

// The tool's own diagnosis is what tells someone what to fix - `docker info` names the
// socket and asks whether the daemon is running. Paraphrasing it discarded the most
// actionable line available, so the probe's output must reach the error.
func TestReadinessErrorCarriesProbeOutput(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Command{
		"faketool": {Bin: "sh", Args: []string{"-c", "echo 'Cannot connect to the daemon at /var/run/x.sock' >&2; exit 1"}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	err := checkReady(context.Background(), readiness, op, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "/var/run/x.sock", "the probe's own message was discarded")
	assert.True(t, errors.Is(err, types.ToolNotReady), "wrapping must not lose the code")
}

// A silent probe still produces a usable error rather than a dangling colon.
func TestReadinessErrorWithoutProbeOutput(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Command{
		"faketool": {Bin: "sh", Args: []string{"-c", "exit 1"}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	err := checkReady(context.Background(), readiness, op, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs it running")
	assert.True(t, errors.Is(err, types.ToolNotReady))
}

// A passing probe lets the op through.
func TestReadinessGateAllowsWhenProbePasses(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Command{
		"faketool": {Bin: "sh", Args: []string{"-c", "exit 0"}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}
	assert.NoError(t, checkReady(context.Background(), readiness, op, t.TempDir()))
}

// An op whose tool declares no probe is never gated - the hadolint-beside-docker case.
func TestReadinessGateIgnoresUndeclaredTools(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Command{
		"docker": {Bin: "sh", Args: []string{"-c", "exit 1"}},
	}
	lint := spells.Op{Command: spells.Command{Bin: "hadolint"}}
	assert.NoError(t, checkReady(context.Background(), readiness, lint, t.TempDir()),
		"linting a Dockerfile must not wait on the docker daemon")
}

// The probe forks, so it must run once per (bin, dir) and not once per op.
func TestReadinessProbeIsMemoized(t *testing.T) {
	readinessMemo.Clear()
	dir := t.TempDir()
	// A probe that succeeds only while the marker is absent: if it ran twice, the
	// second call would see the file it created and fail.
	readiness := map[string]spells.Command{
		"faketool": {Bin: "sh", Args: []string{"-c", "test ! -e ran && touch ran"}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	require.NoError(t, checkReady(context.Background(), readiness, op, dir))
	assert.NoError(t, checkReady(context.Background(), readiness, op, dir),
		"probe re-ran; a forked check must be memoized per (bin, dir)")
}

// The grace period is interactive-only, and this pins the half that would otherwise
// go unnoticed: under CI or an agent (no TTY) a failing probe must return AT ONCE.
// Waiting there burns the grace period per project to reach the same failure, and
// nobody is going to start a daemon mid-run.
func TestReadinessDoesNotWaitWhenNotInteractive(t *testing.T) {
	readinessMemo.Clear()
	readiness := map[string]spells.Command{
		"faketool": {Bin: "sh", Args: []string{"-c", "exit 1"}},
	}
	op := spells.Op{Command: spells.Command{Bin: "faketool"}}

	start := time.Now()
	err := checkReady(context.Background(), readiness, op, t.TempDir())
	elapsed := time.Since(start)

	require.Error(t, err)
	assert.Less(t, elapsed, readinessGrace/2,
		"a non-interactive run waited %s; the grace period must not apply without a TTY", elapsed)
}
