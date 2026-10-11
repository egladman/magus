package sessions

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordResults files each result under its own invocation, in the order given, so a later
// one is newer by name the way minted ids are.
func recordResults(t *testing.T, dir string, results ...TargetResult) {
	t.Helper()
	for i, r := range results {
		w, err := Open(dir, fmt.Sprintf("invmv%04d1", i), InvocationStart{Command: "run"})
		require.NoError(t, err)
		require.NoError(t, w.Append(KindTargetResult, r))
	}
}

func TestLastRealDurationIsTheNewestRealRun(t *testing.T) {
	dir := t.TempDir()
	recordResults(t, dir,
		TargetResult{Target: "go-test", Project: ".", Outcome: OutcomePass, DurationMs: 400_000},
		TargetResult{Target: "go::go-test:rw", Project: ".", Outcome: OutcomeFail, DurationMs: 300_000},
		TargetResult{Target: "go-test", Project: ".", Outcome: OutcomePass, DurationMs: 5, Replayed: true},
		TargetResult{Target: "go-test", Project: ".", Outcome: OutcomeFail, DiagnosticCode: "MGS3009"},
		TargetResult{Target: "go-test", Project: "docs", Outcome: OutcomePass, DurationMs: 9_000},
		TargetResult{Target: "lint", Project: ".", Outcome: OutcomePass, DurationMs: 70_000},
	)

	got, ok := LastRealDuration(dir, ".", "go-test")
	require.True(t, ok)
	assert.Equal(t, 300*time.Second, got, "the newest run that ran: a replay, a refusal and another project do not count, and a spell filter or charm names the same target")

	got, ok = LastRealDuration(dir, "", "go_test:rw")
	require.True(t, ok)
	assert.Equal(t, 300*time.Second, got, "an empty project is the root, and the name is compared normalized")

	_, ok = LastRealDuration(dir, ".", "build")
	assert.False(t, ok, "a target that never ran here has no duration")
	_, ok = LastRealDuration(t.TempDir(), ".", "go-test")
	assert.False(t, ok, "an empty store")
	_, ok = LastRealDuration(dir+"/absent", ".", "go-test")
	assert.False(t, ok, "a missing store")
}

func TestLastRealDurationReadsOnlyTheNewestFiles(t *testing.T) {
	dir := t.TempDir()
	results := make([]TargetResult, lastRunScan+1)
	results[0] = TargetResult{Target: "go-test", Project: ".", Outcome: OutcomePass, DurationMs: 60_000}
	for i := 1; i < len(results); i++ {
		results[i] = TargetResult{Target: "lint", Project: ".", Outcome: OutcomePass, DurationMs: 1_000}
	}
	recordResults(t, dir, results...)

	_, ok := LastRealDuration(dir, ".", "go-test")
	assert.False(t, ok, "a run older than the newest %d files is not read", lastRunScan)
}
