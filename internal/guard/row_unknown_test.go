package guard

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
)

// plantUnknownMember appends id's stored row again carrying a member no build declares,
// as a newer magus would write it; the store's own writers carry the stored bag instead.
func plantUnknownMember(t *testing.T, ctx context.Context, id, member string) {
	t.Helper()
	at := ctx.Value(locationKey{}).(location)
	store := job.NewStore(job.Location{CacheDir: at.cacheDir, StateBase: fixture.StateBase(t), Root: at.workspace})
	rows, err := store.List()
	require.NoError(t, err)
	i := slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })
	require.GreaterOrEqual(t, i, 0, "%s is seeded", id)
	row := rows[i]
	row.Unknown = map[string]json.RawMessage{member: json.RawMessage(`["**"]`)}
	line, err := json.Marshal(row)
	require.NoError(t, err)
	path, err := store.Path()
	require.NoError(t, err)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	require.NoError(t, err)
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	require.NoError(t, err)
}

func TestRowUnknownRefusesARowCarryingAMemberThisBuildDoesNotDeclare(t *testing.T) {
	row := narrowLease()
	ctx, root := fleetFixture(t, row)
	plantUnknownMember(t, ctx, row.ID, "widens_paths")
	deps := Dependencies{Binary: "/work/magus", BinaryVersion: "v0.4.3-dev"}

	got := denyUnknownRowMembers(ctx, deps, row.ID, "magus describe job "+row.ID)
	require.True(t, got.refused())
	assert.Contains(t, got.Say, "widens_paths")
	assert.Contains(t, got.Say, "/work/magus")
	assert.Contains(t, got.Say, "v0.4.3-dev")
	assert.Contains(t, got.Why, ward.BootstrapCommand)
	assert.Contains(t, got.Why, "server stop")

	write := gradeLeasedEdit(ctx, deps, row.ID, filepath.Join(root, "cmd/magus/agent.go"), writeFields{})
	require.Equal(t, decisionDeny, write.Decision)
	assert.Equal(t, string(denyRuleLeaseRowUnknown), write.Rule)
	assert.Contains(t, write.Reason, "widens_paths")

	assert.Equal(t, denyRuleLeaseRowUnknown, roleScopedCommandRules()[0].name, "judged before any rule that reads the row")
}

func TestRowUnknownPassesARowItCanRead(t *testing.T) {
	row := narrowLease()
	ctx, root := fleetFixture(t, row)

	assert.False(t, denyUnknownRowMembers(ctx, Dependencies{}, row.ID, "magus describe job "+row.ID).refused())
	assert.False(t, denyUnknownRowMembers(ctx, Dependencies{}, "", "magus describe job "+row.ID).refused(), "no lease, no row")
	assert.Empty(t, gradeLeasedEdit(ctx, Dependencies{}, row.ID, filepath.Join(root, "cmd/magus/agent.go"), writeFields{}).Decision)

	outside := gradeLeasedEdit(ctx, Dependencies{}, row.ID, filepath.Join(root, "README.md"), writeFields{})
	require.Equal(t, decisionDeny, outside.Decision, "the other rules still judge the row")
	assert.NotEqual(t, string(denyRuleLeaseRowUnknown), outside.Rule)
}
