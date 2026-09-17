package sessions

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGateRoundTrip proves a gate verdict written by one producer is read back
// by another through the folded store, the way two worktrees of one repo share
// it.
func TestGateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	start := SessionStart{Workspace: "/repo", Command: "gate"}
	g := GateResult{
		Target:      "ci",
		Ref:         "polish",
		Commit:      "abc123",
		Outcome:     OutcomePass,
		Fingerprint: "f1",
		Projects:    []string{".", "docs"},
		Charms:      []string{"quiet"},
		// The MGS1028 debt rides the same record: a gate's cost is not readable from
		// its verdict, and the projects only containment selected are the part of it
		// that could not have changed the verdict.
		UndeclaredSeeds: []string{"."},
	}
	require.NoError(t, RecordGate(dir, g, start))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	rec, ok := LatestGate(fold, "polish", "ci")
	require.True(t, ok)
	assert.Equal(t, g, rec.GateResult)
	assert.WithinDuration(t, time.Now(), rec.At, time.Minute)

	_, ok = LatestGate(fold, "other-branch", "ci")
	assert.False(t, ok, "a record is scoped to its ref")
	_, ok = LatestGate(fold, "polish", "nightly")
	assert.False(t, ok, "and to its target")
}

// TestGateNewestWins proves a later fail supersedes an earlier pass: the
// redundancy check must see a red branch, not the stale green behind it.
func TestGateNewestWins(t *testing.T) {
	dir := t.TempDir()
	start := SessionStart{Workspace: "/repo"}
	pass := GateResult{Target: "ci", Ref: "b", Commit: "c1", Outcome: OutcomePass, Fingerprint: "f1"}
	require.NoError(t, RecordGate(dir, pass, start))
	// Records order by (Ts, Session, Seq); a same-millisecond write would tie.
	time.Sleep(5 * time.Millisecond)
	fail := GateResult{Target: "ci", Ref: "b", Commit: "c2", Outcome: OutcomeFail, Fingerprint: "f2"}
	require.NoError(t, RecordGate(dir, fail, start))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	rec, ok := LatestGate(fold, "b", "ci")
	require.True(t, ok)
	assert.Equal(t, OutcomeFail, rec.Outcome)
	assert.Equal(t, "c2", rec.Commit)
}

// TestLatestGateEmptyStore is the inert state: nothing recorded means no
// finding, so the first gate on a branch always runs.
func TestLatestGateEmptyStore(t *testing.T) {
	fold, err := ReadAll(t.TempDir())
	require.NoError(t, err)
	_, ok := LatestGate(fold, "b", "ci")
	assert.False(t, ok)
}

// TestGateAtMatchesTheCommitOnAnyRef pins the lookup the push rule reads: a verdict at a
// commit answers for that commit whichever branch recorded it, an abbreviation matches
// the full id, and a deferral never counts as a verdict.
func TestGateAtMatchesTheCommitOnAnyRef(t *testing.T) {
	dir := t.TempDir()
	start := SessionStart{Workspace: "/repo"}
	require.NoError(t, RecordGate(dir, GateResult{Target: "ci", Ref: "a", Commit: "a89dee5c7f00", Outcome: OutcomePass}, start))
	time.Sleep(5 * time.Millisecond)
	require.NoError(t, RecordGate(dir, GateResult{Target: "ci", Ref: "b", Commit: "a89dee5c7f00", Outcome: OutcomeDeferred}, start))

	fold, err := ReadAll(dir)
	require.NoError(t, err)
	rec, ok := GateAt(fold, "a89dee5c", "ci")
	require.True(t, ok)
	assert.Equal(t, OutcomePass, rec.Outcome, "the deferral is skipped for the verdict behind it")

	_, ok = GateAt(fold, "0000000", "ci")
	assert.False(t, ok)
	_, ok = GateAt(fold, "", "ci")
	assert.False(t, ok, "no commit is no answer, not a match on everything")
}
