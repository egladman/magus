package job

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/agent/model"
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

	got, calls := attemptWork(base, "a", since)
	assert.Len(t, calls, 4, "one fact per call of this holder")

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

	got, _ := attemptWork(base, "a", since)

	require.NotNil(t, got)
	require.NotNil(t, got.SetupMs)
	assert.Equal(t, types.JobWork{
		Calls: 2, Writes: 1, SetupCalls: 1,
		SetupTokens: ptr(int64(30_000)), // the first write itself reported one
		SetupMs:     got.SetupMs,
	}, *got)
}

func TestAttemptWorkOfAHolderThatNeverWroteHasNoSetupFigures(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	call(t, base, "a", "file.read", 10_000, "pass")

	got, _ := attemptWork(base, "a", time.Now().Unix()-1)

	require.NotNil(t, got)
	assert.Equal(t, types.JobWork{
		Calls: 1, SetupCalls: 1,
		ContextTokens: ptr(int64(10_000)), PeakTokens: ptr(int64(10_000)),
	}, *got)
}

// No call under the lease is a holder the guard never saw, and no trail is nothing to read:
// both are unknown, never an idle holder.
func TestAttemptWorkIsUnknownWithoutAnObservedCall(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	call(t, base, "other", "file.write", 10_000, "pass")
	work := func(base string, since int64) *types.JobWork {
		w, _ := attemptWork(base, "a", since)
		return w
	}
	assert.Nil(t, work(base, time.Now().Unix()-1), "no call of this lease")
	assert.Nil(t, work(t.TempDir(), 0), "an empty trail")
	assert.Nil(t, work("", 0), "no trail placed")

	call(t, base, "a", "file.write", 10_000, "pass")
	assert.Nil(t, work(base, time.Now().Unix()+60), "calls before the attempt began are not its")
}

// The outcome's tokens and cost are the cache-read floor the session cost report prices
// too, and unknown rather than zero when a call reported no context or a model is unpriced.
func TestPriceAttemptIsTheContextFloorOrUnknown(t *testing.T) {
	t.Parallel()

	table := model.InEffect([]types.ModelPricing{{ID: "m", CacheRead: 1}}, nil)
	s := &Store{prices: func() model.Table { return table }}
	metered := func(tokens int64, id string) types.AgentFacts {
		return types.AgentFacts{ContextTokens: tokens, ContextModel: id, Billing: types.AgentBillingMetered}
	}
	known := &types.JobWork{ContextTokens: ptr(int64(3_000_000))}

	tokens, cost := s.priceAttempt(known, []types.AgentFacts{metered(1_000_000, "m"), metered(1_000_000, "m"), metered(2_000_000, "m")})
	assert.Equal(t, ptr(int64(3_000_000)), tokens, "a repeated context is one model call")
	assert.Equal(t, ptr(3.0), cost)

	tokens, cost = s.priceAttempt(known, []types.AgentFacts{metered(1_000_000, "unpriced")})
	assert.Nil(t, tokens, "a model the table does not know")
	assert.Nil(t, cost)

	tokens, cost = s.priceAttempt(&types.JobWork{}, []types.AgentFacts{metered(1_000_000, "m")})
	assert.Nil(t, tokens, "a call that reported no context")
	assert.Nil(t, cost)

	tokens, cost = (&Store{}).priceAttempt(known, []types.AgentFacts{metered(1_000_000, "m")})
	assert.Nil(t, tokens, "no model table")
	assert.Nil(t, cost)
}

func ptr[T any](v T) *T { return &v }
