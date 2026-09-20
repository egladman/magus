package guard

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGradeLeasedWriteDeniesAnOverdueLease(t *testing.T) {
	leases := fleetLeases()
	leases[1].Deadline = time.Now().Add(-time.Minute).Unix()
	ctx, root := fleetFixture(t, leases...)

	got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/main.go"))
	require.Equal(t, "deny", got.Decision, "an overdue lease writing inside its own write paths is denied")
	assert.Contains(t, got.Reason, "lease-b")
	assert.Contains(t, got.Reason, "deadline")
	assert.Contains(t, got.Reason, time.Unix(leases[1].Deadline, 0).UTC().Format(time.RFC3339))

	leases[1].Deadline = time.Now().Add(time.Hour).Unix()
	ctx, root = fleetFixture(t, leases...)
	assert.Empty(t, gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "cmd/magus/main.go")).Decision,
		"a deadline still ahead bounds nothing yet")
}

func TestGradeLeasedWriteIgnoresAnOverdueOwner(t *testing.T) {
	leases := fleetLeases()
	leases[0].Deadline = time.Now().Add(-time.Minute).Unix()
	ctx, root := fleetFixture(t, leases...)

	got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go"))
	assert.NotContains(t, got.Reason, "is owned by lease lease-a", "an overdue lease no longer holds its write paths against others")
}

func TestGradeLeasedWriteNamesHowToReleaseAnOwner(t *testing.T) {
	ctx, root := fleetFixture(t, fleetLeases()...)

	got := gradeLeasedWrite(ctx, Dependencies{}, "lease-b", filepath.Join(root, "internal/ledger/store.go"))
	require.Equal(t, "deny", got.Decision)
	assert.Contains(t, got.Reason, "last updated")
	assert.Contains(t, got.Reason, "ago")
	assert.Contains(t, got.Reason, hint.JobExit.With("lease-a"))
}
