package doctor

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/ledger"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tmpLedger isolates the real user state directory behind XDG_STATE_HOME, the
// same knob internal/ledger's own tests and the rest of this package's use,
// and returns a store resolved exactly as checkLeaseEnforcing resolves its own
// (no StateBase override), so a row this test puts is the row the check reads.
func tmpLedger(t *testing.T) (cacheDir, root string, store *ledger.Store) {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	cacheDir, root = t.TempDir(), t.TempDir()
	return cacheDir, root, ledger.NewStore(ledger.Location{CacheDir: cacheDir, Root: root})
}

func TestLeaseEnforcingPassesWithNoLeaseBound(t *testing.T) {
	cacheDir, root, _ := tmpLedger(t)

	got := checkLeaseEnforcing(cacheDir, root)

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Equal(t, "no lease bound; the guard advises only", got.Message)
}

func TestLeaseEnforcingFailsOnAnUnknownBoundID(t *testing.T) {
	cacheDir, root, _ := tmpLedger(t)
	require.NoError(t, ledger.BindLease(cacheDir, "adj/no-such-lease"))

	got := checkLeaseEnforcing(cacheDir, root)

	assert.Equal(t, types.DoctorFail, got.Status)
	assert.Contains(t, got.Message, `"adj/no-such-lease"`)
	assert.Contains(t, got.Message, "no row declares it")
}

func TestLeaseEnforcingAdvisesOnATerminalBoundRow(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	require.NoError(t, ledger.BindLease(cacheDir, "adj/done"))
	_, err := store.Put(context.Background(), types.Lease{ID: "adj/done", State: types.StatePass})
	require.NoError(t, err)

	got := checkLeaseEnforcing(cacheDir, root)

	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, "terminal (pass)")
}

func TestLeaseEnforcingAdvisesOnALiveRowWithNoRegisteredBase(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	require.NoError(t, ledger.BindLease(cacheDir, "adj/live"))
	_, err := store.Put(context.Background(), types.Lease{ID: "adj/live", State: types.StateRunning})
	require.NoError(t, err)

	got := checkLeaseEnforcing(cacheDir, root)

	assert.Equal(t, types.DoctorAdvice, got.Status)
	assert.Contains(t, got.Message, "no registered base")
	assert.Contains(t, got.Details[0], "magus vcs checkpoint -o name")
}

func TestLeaseEnforcingPassesOnALiveRegisteredRow(t *testing.T) {
	cacheDir, root, store := tmpLedger(t)
	require.NoError(t, ledger.BindLease(cacheDir, "adj/enforcing"))
	_, err := store.Put(context.Background(), types.Lease{ID: "adj/enforcing", State: types.StateRunning, Checkpoint: "abc123"})
	require.NoError(t, err)
	_, err = store.Register(context.Background(), "adj/enforcing", "abc123")
	require.NoError(t, err)

	got := checkLeaseEnforcing(cacheDir, root)

	assert.Equal(t, types.DoctorOK, got.Status)
	assert.Contains(t, got.Message, "live, and enforcing")
}
