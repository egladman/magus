package magus

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// openSandboxed opens a workspace whose magus.yaml sets the sandbox mode.
func openSandboxed(t *testing.T, mode types.SandboxMode) *Magus {
	t.Helper()
	root := writeWorkspace(t, map[string]string{
		"magusfile.buzz": "",
		"magus.yaml":     "sandbox:\n  mode: " + string(mode) + "\n",
	})
	m, err := Open(t.Context(), root, WithSpawnRule(TierCeiling))
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestApplySandboxLeavesAnOffWorkspaceAlone(t *testing.T) {
	m := openSandboxed(t, types.SandboxModeOff)
	ctx, err := m.ApplySandbox(t.Context())
	require.NoError(t, err)
	assert.Nil(t, sandbox.PolicyFromContext(ctx))
}

// Best-effort attaches the policy whatever the host: the binding checks and the env
// allowlist hold everywhere, and the kernel layer joins them where it can.
func TestApplySandboxAttachesTheWorkspacePolicy(t *testing.T) {
	m := openSandboxed(t, types.SandboxModeBestEffort)
	ctx, err := m.ApplySandbox(t.Context())
	require.NoError(t, err)
	p := sandbox.PolicyFromContext(ctx)
	require.NotNil(t, p)
	assert.Equal(t, types.SandboxModeBestEffort, p.Mode)
	assert.Equal(t, types.SandboxModeBestEffort, m.SandboxMode())
}

// The daemon applies the sandbox for a run it adopted, and its own environment names
// nobody's lease, so the client's lease on ctx is the boundary the policy narrows to.
func TestApplySandboxNarrowsToTheLeaseOnTheContext(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(trail.EnvBaggage, "")
	m := openSandboxed(t, types.SandboxModeBestEffort)
	store := job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: m.CacheDir(), Root: m.ws.Root})
	for _, id := range []string{"fleet/client", "fleet/daemon"} {
		_, err := store.Update(t.Context(), id, func(cur *types.Job) {
			*cur = types.Job{ID: id, Parent: "fleet/root", State: types.StateRunning, WritePaths: []string{"pkg/**"}}
		})
		require.NoError(t, err)
	}
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=fleet/daemon")
	leaseOf := func(ctx context.Context) string {
		t.Helper()
		ctx, err := m.ApplySandbox(ctx)
		require.NoError(t, err)
		p := sandbox.PolicyFromContext(ctx)
		require.NotNil(t, p)
		return p.Lease
	}

	assert.Equal(t, "fleet/client", leaseOf(types.WithJobLease(t.Context(), "fleet/client")))
	assert.Equal(t, "fleet/daemon", leaseOf(t.Context()), "with no lease on ctx the environment's claim stands")
}

// Required is refused where the kernel cannot confine the children, before any run
// starts, rather than at the first child.
func TestApplySandboxRefusesARequiredWorkspaceTheKernelCannotConfine(t *testing.T) {
	if abi, err := sandbox.ABI(); err == nil && abi >= sandbox.RequiredABI {
		t.Skipf("landlock ABI %d confines children here", abi)
	}
	m := openSandboxed(t, types.SandboxModeRequired)
	_, err := m.ApplySandbox(t.Context())
	require.ErrorIs(t, err, types.SandboxRequired)
	assert.ErrorContains(t, err, "sandbox mode is required for /")
}

// MGS2005 is said once, by the invocation a person started, and never by a nested magus.
func TestWarnKernelUnavailableOncePerTopLevelInvocation(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	warnedKernelUnavailable = sync.Once{}
	t.Cleanup(func() { warnedKernelUnavailable = sync.Once{} })
	ctx := context.Background()

	t.Setenv("MAGUS_LEVEL", "1")
	warnKernelUnavailable(ctx)
	assert.Empty(t, buf.String(), "a nested magus stays quiet")

	t.Setenv("MAGUS_LEVEL", "0")
	warnKernelUnavailable(ctx)
	warnKernelUnavailable(ctx)
	assert.Contains(t, buf.String(), string(types.SandboxUnsupported))
	assert.Equal(t, 1, strings.Count(buf.String(), "kernel landlock unavailable"), "once per process")
}
