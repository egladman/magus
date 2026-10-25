package main

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const waitClaimCI = "import \"magus\";\n\nexport fun ci(ctx: magus\\Context, args: [str]) > void {}\n"

func totalSlots(claims []types.MachineClaim) int {
	n := 0
	for _, c := range claims {
		n += c.Slots
	}
	return n
}

// waitClaimWorkspace commits two projects, then changes both, so `affected ci --base HEAD~1`
// selects each.
func waitClaimWorkspace(t *testing.T) {
	t.Helper()
	testkit.Isolate(t)
	repo := testkit.NewRepo(t, map[string]string{
		"magus.yaml":       "{}\n",
		"magusfile.buzz":   waitClaimCI,
		"a/magusfile.buzz": waitClaimCI,
		"b/magusfile.buzz": waitClaimCI,
		"a/x.txt":          "1\n",
		"b/x.txt":          "1\n",
	})
	repo.Commit("change both", map[string]string{"a/x.txt": "2\n", "b/x.txt": "2\n"})
	resetWorkspaceMemo(t)
	t.Chdir(repo.Root())
}

// An integrator waits for a seat only as long as it said: past its deadline the wait is
// refused, naming each run that held the budget. A zero deadline waits without bound.
func TestWithinDeadlineRefusesPastItNamingTheHolders(t *testing.T) {
	t.Parallel()

	since := time.Now().Add(-4 * time.Minute)
	full := func(context.Context) (types.MachineSnapshot, error) {
		return types.MachineSnapshot{BudgetSlots: 2, HeldSlots: 2, Holders: []types.MachineClaimant{
			{PID: 21589, Project: ".", Target: "go-test", Slots: 1, Command: "./magus run go-test .", Since: since},
			{PID: 7, Project: "docs", Target: "lint", Slots: 1, Since: since},
		}}, nil
	}
	await := func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}

	err := withinDeadline(t.Context(), 20*time.Millisecond, full, await)
	require.Error(t, err)
	assert.Equal(t, "the build budget had no seat within 20ms, held by pid 21589 (`./magus run go-test .`) for 4m0s, pid 7 (lint docs) for 4m0s", err.Error())

	ran := false
	require.NoError(t, withinDeadline(t.Context(), 0, full, func(ctx context.Context) error {
		_, bounded := ctx.Deadline()
		ran = !bounded
		return nil
	}))
	assert.True(t, ran, "a zero deadline sets none")
}

// The wait holds for what the downstream run claims: a piped `affected ci` claims one
// seat per project it selects, as `run ci` does for the same projects, not the single
// slot any other command waits for.
func TestPipedAffectedClaimsWhatCIClaims(t *testing.T) {
	waitClaimWorkspace(t)

	run, reason := pipedClaims(t.Context(), "", []string{"magus", "run", "ci", ".", "a", "b"})
	require.Empty(t, reason)
	require.Equal(t, 3, totalSlots(run), "run ci claims one slot per project")

	got, reason := pipedClaims(t.Context(), "", []string{"magus", "affected", "ci", "--base", "HEAD~1"})
	require.Empty(t, reason)
	assert.Equal(t, totalSlots(run), totalSlots(got))
	assert.Len(t, got, len(run))
}

// A command the wait cannot size says so, and still asks for one slot.
func TestPipedClaimsSayWhenTheyFallBack(t *testing.T) {
	waitClaimWorkspace(t)

	for name, argv := range map[string][]string{
		"affected reads its set from stdin": {"magus", "affected", "ci", "--stdin"},
		"an unparsable target":              {"magus", "run", "bad::target::x", "a"},
	} {
		t.Run(name, func(t *testing.T) {
			got, reason := pipedClaims(t.Context(), "", argv)
			assert.Equal(t, 1, totalSlots(got))
			assert.NotEmpty(t, reason, "a fallback names why it fell back")
		})
	}
}
