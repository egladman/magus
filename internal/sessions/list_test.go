package sessions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSummarizeTargetsCollapsesRepeats(t *testing.T) {
	t.Parallel()
	got := summarizeTargets([]TargetResult{
		{Target: "build", Project: "api", Outcome: OutcomePass},
		{Target: "build", Project: "api", Outcome: OutcomePass},
		{Target: "build", Project: "web", Outcome: OutcomeFail},
		{Target: "lint", Project: "web", Outcome: OutcomePass, Replayed: true},
	})
	assert.Equal(t, "build api (pass), build web (fail), lint web (pass, cached)", got)
}

// sessionsLeaseCell returns the LEASE cell of the row for session, which is the column the console
// drawer's join is waiting on. Reading it positionally rather than searching the whole listing is
// what makes the "-" case assertable: a dash appears in every unattributed column.
func TestSessionParentResolvesOnlyWhatTheStoreHolds(t *testing.T) {
	t.Parallel()
	bySpan := map[string]string{"00f067aa0ba902b7": "invParent"}

	assert.Equal(t, "invParent", invocationParent(bySpan, "00f067aa0ba902b7"))
	assert.Equal(t, "a1b2c3d4e5f60718", invocationParent(bySpan, "a1b2c3d4e5f60718"))
	assert.Empty(t, invocationParent(bySpan, ""), "a session that claimed no parent has nothing to resolve")
}

// The listing answers "what happened", but its reader is often looking for "what needs
// me", so an open queue gets one cross-reference line, and a quiet queue gets silence
// rather than a reassurance nobody asked for.
func TestParseSinceAcceptsBothSpellings(t *testing.T) {
	t.Parallel()

	empty, err := ParseSince("")
	require.NoError(t, err)
	assert.True(t, empty.IsZero(), "no cutoff admits every session")

	back, err := ParseSince("2h")
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(-2*time.Hour), back, time.Minute)

	// A caller who writes the minus sign means the same window, not the future.
	negated, err := ParseSince("-2h")
	require.NoError(t, err)
	assert.WithinDuration(t, back, negated, time.Minute)

	instant, err := ParseSince("2026-08-20T09:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), instant.UTC())
}

func TestParseSinceRejectsAValueThatIsNeitherForm(t *testing.T) {
	t.Parallel()

	_, err := ParseSince("last tuesday")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "neither a duration nor an RFC3339 timestamp")
	assert.Contains(t, err.Error(), "2h", "the error shows a value that would work")
}

// The filter turns on the LAST fact rather than the first, so a session that began
// before the window and is still working stays listed. Hiding it is exactly the wrong
// answer for the question --since asks.
func TestInvocationsSinceKeepsALongInvocationStillWorking(t *testing.T) {
	t.Parallel()

	now := time.Now()
	old := Summary{Invocation: "stale", StartedMs: now.Add(-48 * time.Hour).UnixMilli(), LastMs: now.Add(-47 * time.Hour).UnixMilli()}
	long := Summary{Invocation: "long", StartedMs: now.Add(-48 * time.Hour).UnixMilli(), LastMs: now.Add(-time.Minute).UnixMilli()}

	got := InvocationsSince([]Summary{old, long}, now.Add(-2*time.Hour))
	require.Len(t, got, 1)
	assert.Equal(t, "long", got[0].Invocation)
}

func TestInvocationsSinceWithNoCutoffKeepsEverything(t *testing.T) {
	t.Parallel()

	all := []Summary{{Invocation: "a"}, {Invocation: "b"}}
	assert.Equal(t, all, InvocationsSince(all, time.Time{}))
}

func TestInvocationsInCheckoutKeepsThisCheckoutAndFactsWithNoWorkspace(t *testing.T) {
	t.Parallel()

	here, elsewhere := t.TempDir(), t.TempDir()
	all := []Summary{
		{Invocation: "here", Workspace: here},
		{Invocation: "elsewhere", Workspace: elsewhere},
		{Invocation: "legacy"},
	}
	got := InvocationsInCheckout(all, here)
	require.Len(t, got, 2)
	assert.Equal(t, "here", got[0].Invocation)
	assert.Equal(t, "legacy", got[1].Invocation, "a fact written before the workspace was recorded cannot be placed, so it stays")
}

// recordSessionIn files one passing session under the repository's shared store but
// attributed to workspace, which is how another worktree's run looks from here.
