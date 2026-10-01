//go:build !wasm

package std

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// withVCSLeaseGate registers gate for the test and restores whatever was registered.
func withVCSLeaseGate(t *testing.T, gate VCSLeaseGate) {
	t.Helper()
	prev := vcsLeaseGate
	RegisterVCSLeaseGate(gate)
	t.Cleanup(func() { RegisterVCSLeaseGate(prev) })
}

// Not parallel: the job store resolves the per-repository state directory, and the
// environment is what keeps these rows out of the developer's own store.
func TestVcsLeaseRefusalAsksTheGateUnderALiveLease(t *testing.T) {
	testkit.Isolate(t)
	ws := types.WithWorkspace(t.Context(), &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})
	_, err := MagusPutJob(ws, "worker", map[string]any{"criteria": "ship it", "state": "running"})
	require.NoError(t, err)

	var asked []string
	refusal := errors.New("lease-vcs: refused")
	withVCSLeaseGate(t, func(_ context.Context, row types.Job, backend string, args []string, dir string) error {
		asked = append(asked, row.ID+" "+backend+" "+args[0]+" "+dir)
		return refusal
	})

	assert.NoError(t, vcsLeaseRefusal(ws, "git", []string{"push"}, "/repo"), "no acting lease, nothing to ask")
	assert.NoError(t, vcsLeaseRefusal(proc.WithLease(ws, "absent"), "git", []string{"push"}, "/repo"), "a lease with no row bounds nothing")
	assert.ErrorIs(t, vcsLeaseRefusal(proc.WithLease(ws, "worker"), "git", []string{"push"}, "/repo"), refusal)
	assert.Equal(t, []string{"worker git push /repo"}, asked)
}

// An acting lease with no gate registered refuses rather than running the command
// unguarded: a binary that links no guard has no way to know the call is allowed.
func TestVcsLeaseRefusalFailsClosedWithNoGate(t *testing.T) {
	testkit.Isolate(t)
	ws := types.WithWorkspace(t.Context(), &fakeLedgerWorkspace{cacheDir: t.TempDir(), root: t.TempDir()})
	withVCSLeaseGate(t, nil)

	assert.NoError(t, vcsLeaseRefusal(ws, "git", []string{"commit", "-m", "x"}, "/repo"))
	err := vcsLeaseRefusal(proc.WithLease(ws, "worker"), "git", []string{"commit", "-m", "x"}, "/repo")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "lease worker is acting and this binary registers no lease-vcs gate")
	assert.Contains(t, err.Error(), "`git commit -m x`")
}
