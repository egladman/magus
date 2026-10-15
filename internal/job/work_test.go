package job

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// call records one guard observation of lease's holder in the trail at base.
func call(t *testing.T, base, lease, tool string, tokens int64, decision string) {
	t.Helper()
	trail.AppendAgentCommand(t.Context(), base, trail.AgentCommand{
		Host: "claude-code", Session: "s1", Lease: lease, Tool: tool, Path: "internal/job/x.go",
		Decision: decision, AgentFacts: types.AgentFacts{ContextTokens: tokens},
	})
}

func TestAttemptWorkFoldsTheHoldersCallsUpToAndAfterItsFirstWrite(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	since := time.Now().Unix() - 1
	call(t, base, "a", "file.read", 10_000, "pass")
	call(t, base, "a", "shell.command", 20_000, "deny")
	call(t, base, "other", "file.write", 99_000, "pass")
	call(t, base, "a", "file.write", 30_000, "pass")
	call(t, base, "a", "file.write", 50_000, "pass")

	got := attemptWork(base, "a", since)

	require.NotNil(t, got)
	require.NotNil(t, got.SetupMs)
	assert.GreaterOrEqual(t, *got.SetupMs, int64(0))
	want := types.JobWork{
		Calls: 4, Writes: 2, SetupCalls: 2, Denials: 1,
		SetupContext: ptr(int64(30_000)), SetupTokens: ptr(int64(30_000)),
		ContextTokens: ptr(int64(110_000)), PeakTokens: ptr(int64(50_000)),
		SetupMs: got.SetupMs,
	}
	assert.Equal(t, want, *got, "another lease's calls are not this holder's")
}

// A sum over the calls that reported a size would read as the job's, so one unreported
// call leaves the sums unknown while the counts stand.
func TestAttemptWorkLeavesTheContextUnknownWhenACallReportedNone(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	since := time.Now().Unix() - 1
	call(t, base, "a", "file.read", 0, "pass")
	call(t, base, "a", "file.write", 30_000, "pass")

	got := attemptWork(base, "a", since)

	require.NotNil(t, got)
	assert.Equal(t, 2, got.Calls)
	assert.Equal(t, ptr(int64(30_000)), got.SetupTokens, "the first write itself reported one")
	assert.Nil(t, got.SetupContext)
	assert.Nil(t, got.ContextTokens)
	assert.Nil(t, got.PeakTokens)
}

func TestAttemptWorkOfAHolderThatNeverWroteHasNoSetupFigures(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	call(t, base, "a", "file.read", 10_000, "pass")

	got := attemptWork(base, "a", time.Now().Unix()-1)

	require.NotNil(t, got)
	assert.Equal(t, 1, got.SetupCalls)
	assert.Zero(t, got.Writes)
	assert.Nil(t, got.SetupMs)
	assert.Nil(t, got.SetupTokens)
	assert.Nil(t, got.SetupContext)
	assert.Equal(t, ptr(int64(10_000)), got.ContextTokens)
}

// No call under the lease is a holder the guard never saw, and no trail is nothing to read:
// both are unknown, never an idle holder.
func TestAttemptWorkIsUnknownWithoutAnObservedCall(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	call(t, base, "other", "file.write", 10_000, "pass")
	assert.Nil(t, attemptWork(base, "a", time.Now().Unix()-1), "no call of this lease")
	assert.Nil(t, attemptWork(t.TempDir(), "a", 0), "an empty trail")
	assert.Nil(t, attemptWork("", "a", 0), "no trail placed")

	call(t, base, "a", "file.write", 10_000, "pass")
	assert.Nil(t, attemptWork(base, "a", time.Now().Unix()+60), "calls before the attempt began are not its")
}

func ptr[T any](v T) *T { return &v }
