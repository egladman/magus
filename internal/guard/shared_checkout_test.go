package guard

import (
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGuardGradesTwoSessionsInOneCheckoutSeparately is the enforcement half of the
// per-session binding: each session's own marker decides which lane its writes are graded
// against, so two workers sharing a checkout are each denied outside their own paths
// rather than both running ungraded.
func TestGuardGradesTwoSessionsInOneCheckoutSeparately(t *testing.T) {
	one := types.Job{
		ID: "wave/one", Criteria: "the store", WritePaths: []string{"internal/job/**"},
		State: types.StateRunning, Checkpoint: "rev", ReportedBase: "rev", BaseVerdict: types.BaseMatch, Registered: 1,
	}
	two := types.Job{
		ID: "wave/two", Criteria: "the guard", WritePaths: []string{"internal/guard/**"},
		State: types.StateRunning, Checkpoint: "rev", ReportedBase: "rev", BaseVerdict: types.BaseMatch, Registered: 1,
	}
	ctx, _ := fleetFixture(t, one, two)
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir

	require.NoError(t, job.Checkout{CacheDir: cacheDir, Session: "session-one"}.Bind(one.ID))
	require.NoError(t, job.Checkout{CacheDir: cacheDir, Session: "session-two"}.Bind(two.ID))

	assert.Equal(t, one.ID, job.Checkout{CacheDir: cacheDir, Session: "session-one"}.ActingLease())
	assert.Equal(t, two.ID, job.Checkout{CacheDir: cacheDir, Session: "session-two"}.ActingLease())

	first := Judge(ctx, Dependencies{}, Request{
		Input: "internal/guard/spawn.go", IsPath: true, Session: "session-one", Host: "test-host",
	})
	assert.Equal(t, one.ID, first.Lease, "session one is graded under its own row")

	second := Judge(ctx, Dependencies{}, Request{
		Input: "internal/guard/spawn.go", IsPath: true, Session: "session-two", Host: "test-host",
	})
	assert.Equal(t, two.ID, second.Lease, "session two is graded under its own row, in the same checkout")
	assert.NotEqual(t, first.Lease, second.Lease)
}

// TestSpawnIsAdvisedWhenTheCheckoutIsAlreadyHeld pins the advisory that makes the
// collision proof unskippable: an orchestrator handing out a second worker in a checkout
// somebody is already writing in is told so, with the union of the lanes to check and the
// worktree that makes the check unnecessary.
func TestSpawnIsAdvisedWhenTheCheckoutIsAlreadyHeld(t *testing.T) {
	held := types.Job{
		ID: "wave/holder", Criteria: "the store", WritePaths: []string{"internal/job", "types/job.go"},
		State: types.StateRunning, Registered: 1,
	}
	ctx, _ := fleetFixture(t, held)
	cacheDir := hookLocation(ctx, Dependencies{}).cacheDir
	at := hookLocation(ctx, Dependencies{})

	t.Run("nothing bound here says nothing", func(t *testing.T) {
		assert.Empty(t, adviseSharedCheckoutSpawn(ctx, hint.NewGate(cacheDir, "quiet"), at))
	})

	require.NoError(t, job.Checkout{CacheDir: cacheDir, Session: "holder"}.Bind(held.ID))

	gate := hint.NewGate(cacheDir, "orchestrator")
	note := adviseSharedCheckoutSpawn(ctx, gate, at)
	require.NotEmpty(t, note)
	assert.Contains(t, note, "wave/holder")
	assert.Contains(t, note, "internal/job")
	assert.Contains(t, note, "types/job.go")
	assert.Contains(t, note, hint.DescribeFile.String())
	assert.Contains(t, note, "worktree")

	t.Run("it fires once per session", func(t *testing.T) {
		again := adviseSharedCheckoutSpawn(ctx, gate, at)
		assert.NotEqual(t, note, again, "the full text is spent once; a brief line is what repeats")
	})
}
