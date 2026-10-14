package guard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// A phase spends its shares' part of what remains, leaves the rest to the phases after it,
// runs unbounded with no deadline, and is never handed a negative budget.
func TestPhaseSharesSplitWhatRemains(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), judgeReserve+2*time.Second)
	defer cancel()
	shares := phaseShares{phaseApprovedResolve: 2, phaseApprovedRule: 1, phaseWorktreeRule: 1}
	got, bounded := shares.take(ctx, phaseApprovedResolve)
	require.True(t, bounded)
	assert.LessOrEqual(t, got, time.Second)
	assert.Greater(t, got, 900*time.Millisecond)
	assert.NotContains(t, shares, phaseApprovedResolve, "a phase that started holds no shares")
	got, _ = shares.take(ctx, phaseApprovedRule)
	assert.Greater(t, got, 900*time.Millisecond, "half of what remains, the approved resolve having spent none")

	_, bounded = phaseShares{phaseWorktreeRule: 1}.take(t.Context(), phaseWorktreeRule)
	assert.False(t, bounded, "with no deadline, a phase runs to completion")

	gone, cancelGone := context.WithTimeout(t.Context(), judgeReserve/2)
	defer cancelGone()
	got, bounded = phaseShares{phaseWorktreeRule: 1}.take(gone, phaseWorktreeRule)
	assert.True(t, bounded)
	assert.Zero(t, got, "past the reserve, nothing is left, and never less")
}

func TestWithHookDeadlineKeepsAnEarlierOne(t *testing.T) {
	early, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	want, _ := early.Deadline()
	ctx, cancelHook := WithHookDeadline(early)
	defer cancelHook()
	got, ok := ctx.Deadline()
	require.True(t, ok)
	assert.Equal(t, want, got)

	ctx, cancelHook = WithHookDeadline(t.Context())
	defer cancelHook()
	got, ok = ctx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, time.Now().Add(hookBudget), got, time.Second)
}

// A load that failed after the deadline passed lapsed; one that answered did not, however
// late, and one with no deadline has no budget to lapse against. The load's own answer
// comes back beside the phase.
func TestTimeRuleLoadLapsesOnlyAFailurePastTheDeadline(t *testing.T) {
	expired, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	<-expired.Done()
	failed := errors.New("magusfile: context deadline exceeded")
	load := func(err error) func() (string, error) { return func() (string, error) { return "rules", err } }

	_, phase, err := TimeRuleLoad(expired, load(failed))
	assert.True(t, phase.Lapsed)
	require.ErrorIs(t, err, failed)
	got, phase, err := TimeRuleLoad(expired, load(nil))
	require.NoError(t, err)
	assert.Equal(t, "rules", got)
	assert.False(t, phase.Lapsed)

	_, unbounded, _ := TimeRuleLoad(t.Context(), load(failed))
	assert.Equal(t, trail.Phase{Name: phaseLoadRules, ElapsedMs: unbounded.ElapsedMs}, unbounded)
}

// A judgment stuck where no deadline reaches still answers in time: past the hook's bound a
// call that changes state is denied, one that only reads goes through with a note, and the
// trail records the judgment as lapsed. The host got its answer, so the mark is ended.
func TestJudgeWithinAnswersAStuckJudgmentByTheDeadline(t *testing.T) {
	// A host's read tool, as the hook glue restates it.
	read := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Read","tool_input":{"command":"sed -n 1,40p notes.txt"}}`
	cases := map[string]struct {
		req      Request
		decision string
		tool     string
	}{
		"a command that changes state": {req: Request{Input: "touch notes.txt"}, decision: decisionDeny, tool: hookToolCommand},
		"a file write":                 {req: Request{Input: "notes.txt", IsPath: true}, decision: decisionDeny, tool: hookToolWrite},
		"a command that reads":         {req: Request{Input: "ls -la"}, decision: decisionAdvise, tool: hookToolCommand},
		"a host's read":                {req: Request{Input: read}, decision: decisionAdvise, tool: hookToolCommand},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			release := make(chan struct{})
			defer close(release)
			req := c.req
			req.Host, req.Session = "claude-code", "s1"

			start := time.Now()
			v := JudgeWithin(ctx, Dependencies{}, req, func(context.Context) Verdict {
				<-release
				return Verdict{Decision: decisionPass}
			})
			assert.Less(t, time.Since(start), 2*time.Second)
			assert.Equal(t, verdictWithRule(c.decision, string(advisoryUnfinished)), unworded(v))
			if c.decision == decisionDeny {
				assert.Contains(t, v.Reason, "the guard's judgment did not finish within 7s. A call that changes state is denied rather than let through unjudged; retry it.")
			} else {
				assert.Equal(t, unjudgedNote(), v.Context)
			}

			rows := trailEvents(t, cacheDir, trail.KindAgentCommand)
			require.Len(t, rows, 1)
			assert.Equal(t, c.tool, rows[0].Action)
			require.NotEmpty(t, rows[0].Phases)
			assert.Equal(t, phaseAnswer, rows[0].Phases[0].Name)
			assert.True(t, rows[0].Phases[0].Lapsed)
			assert.Equal(t, hookBudget.Milliseconds(), rows[0].Phases[0].BudgetMs)

			entries, err := os.ReadDir(filepath.Join(cacheDir, unfinishedDir))
			require.NoError(t, err)
			for _, e := range entries {
				marks, err := os.ReadDir(filepath.Join(cacheDir, unfinishedDir, e.Name()))
				require.NoError(t, err)
				assert.Empty(t, marks, "the host was answered, so no later check reports this one")
			}
		})
	}
}

// A judgment that answers in time is the verdict, and a dry run is judged to the end.
func TestJudgeWithinKeepsAJudgmentThatAnswersBeforeTheDeadline(t *testing.T) {
	ctx, _ := spawnFixture(t)
	want := Verdict{Decision: decisionAdvise, Context: "judged"}
	judge := func(context.Context) Verdict { return want }
	assert.Equal(t, want, JudgeWithin(ctx, Dependencies{}, Request{Input: "ls", Host: "claude-code", Session: "s1"}, judge))

	short, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	slow := func(context.Context) Verdict {
		time.Sleep(700 * time.Millisecond)
		return want
	}
	assert.Equal(t, want, JudgeWithin(short, Dependencies{}, Request{Input: "ls", DryRun: true}, slow))
}

// A lapse is worded against its budget, rounded for a reader, or as having had none.
func TestLapseWordingNamesTheBudget(t *testing.T) {
	rule := lapseWording{"the rule", "answer"}
	assert.Equal(t, "the rule did not answer within 2.1s", rule.clause(2140*time.Millisecond))
	assert.Equal(t, "the rule did not answer within 50ms", rule.clause(50*time.Millisecond))
	assert.Equal(t, "the rule had no time left before the guard's deadline", rule.clause(0))
}

// A rule load that ran out of time is judged as a phase that lapsed, not as a broken
// magusfile: a write is denied naming the load and its budget, a read goes through with the
// same named in its note.
func TestLapsedRuleLoadDeniesAWriteAndAdvisesARead(t *testing.T) {
	deps := Dependencies{
		LoadFailure: errors.New("magusfile: context deadline exceeded"),
		RuleLoad:    trail.Phase{Name: phaseLoadRules, ElapsedMs: 6600, BudgetMs: 6500, Lapsed: true},
	}

	ctx, _ := spawnFixture(t)
	write := Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "notes.txt", IsPath: true})
	assert.Equal(t, decisionDeny, write.Decision)
	assert.Contains(t, write.Reason, "magus ran out of time judging this file write: loading this workspace's guard rules did not finish within 6.5s.")

	read := Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.Equal(t, decisionAdvise, read.Decision)
	assert.Contains(t, read.Context, "The working tree's magus\\guard.command rule judged nothing: the magusfile did not load within 6.5s")
}

// A dry run grades for a person at a terminal, a fork or a verb's own edit, so its rules
// run under a bound of their own, well past the hook's budget: a cold first load there must
// not read as a lapse, and a rule that loops must not hang the fork.
func TestDryRunJudgesWithoutTheHookBudget(t *testing.T) {
	ctx, _ := spawnFixture(t)
	var left []time.Duration
	rule := func(ctx context.Context, _ types.CommandRequest, _ hint.Gate) (types.GuardVerdict, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		left = append(left, time.Until(deadline))
		return types.GuardVerdict{}, nil
	}
	Judge(ctx, Dependencies{CommandRule: rule}, Request{Input: "ls", DryRun: true})
	Judge(ctx, Dependencies{CommandRule: rule}, Request{Input: "ls"})
	require.Len(t, left, 2)
	assert.Greater(t, left[0], time.Minute, "a dry run")
	assert.Less(t, left[1], hookBudget, "a hook")
}

// A dry run whose rule runs past its bound denies a call that changes state, saying it was
// not graded rather than that a host would have cut it off.
func TestDryRunLapseIsNotGraded(t *testing.T) {
	ctx, root, _ := writeFixture(t)
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	loops := func(ctx context.Context, _ types.WriteRequest, _ hint.Gate) (types.GuardVerdict, error) {
		<-ctx.Done()
		return types.GuardVerdict{}, ctx.Err()
	}
	v := Judge(ctx, Dependencies{WriteRule: loops}, Request{Input: filepath.Join(root, "notes.txt"), IsPath: true, DryRun: true})
	assert.Equal(t, decisionDeny, v.Decision)
	assert.Contains(t, v.Reason, "magus did not finish grading this file write: ")
	assert.Contains(t, v.Reason, "so it is not graded. A dry run is bounded at 2m0s")
}

// A session whose every judgment runs out of time is not locked: its first
// lapsesBeforeDegrade calls that change state are denied, the ones after pass with a notice
// that names the phase that stalled once and then only says the guard is still not judging,
// and a judgment that answers in time starts the count over. The judgment here is slow
// rather than stuck, the shape a loaded machine gives every phase.
func TestJudgeWithinDegradesAfterConsecutiveLapses(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	req := Request{Input: "touch notes.txt", Host: "claude-code", Session: "s1"}
	slowLoad := func(ctx context.Context) Verdict {
		_, _, _ = TimeRuleLoad(ctx, func() (int, error) {
			time.Sleep(time.Second)
			return 0, nil
		})
		return Verdict{Decision: decisionPass}
	}
	lapse := func() Verdict {
		short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		return JudgeWithin(short, Dependencies{}, req, slowLoad)
	}

	for range lapsesBeforeDegrade {
		assert.Equal(t, decisionDeny, lapse().Decision)
	}
	degraded := lapse()
	assert.Equal(t, verdictWithRule(decisionAdvise, string(advisoryUnfinished)), unworded(degraded))
	assert.Contains(t, degraded.Context, "on 3 judgments in a row in this session, so this command ran with no rule judging it")
	assert.Contains(t, degraded.Context, "This one stalled in its load_rules phase")
	assert.Contains(t, degraded.Context, "`magus server status`")
	again := lapse()
	assert.Equal(t, decisionAdvise, again.Decision)
	assert.Equal(t, "The guard is still not finishing in time (4 judgments in a row), so this command ran unjudged, as the notice before said.", again.Context)

	onTime := JudgeWithin(ctx, Dependencies{}, req, func(context.Context) Verdict { return Verdict{Decision: decisionPass} })
	assert.Equal(t, decisionPass, onTime.Decision)
	assert.Equal(t, decisionDeny, lapse().Decision, "an answer in time starts the count over")

	rows := trailEvents(t, cacheDir, trail.KindAgentCommand)
	require.NotEmpty(t, rows)
	names := make([]string, len(rows[0].Phases))
	for i, p := range rows[0].Phases {
		names[i] = p.Name
	}
	assert.Equal(t, []string{phaseLoadRules, phaseAnswer, phaseJudge}, names, "the lapse row names the phase underway")
	assert.True(t, rows[0].Phases[0].Lapsed)
}

// An uncounted lapse denies, as do the first lapsesBeforeDegrade; the next names the cause
// once and every later one only says the guard is still not judging.
func TestStageFor(t *testing.T) {
	t.Parallel()
	for inARow, want := range map[int]lapseStage{
		0:                       lapseDenies,
		1:                       lapseDenies,
		lapsesBeforeDegrade:     lapseDenies,
		lapsesBeforeDegrade + 1: lapseDegrades,
		lapsesBeforeDegrade + 2: lapseStillDegraded,
		lapsesBeforeDegrade + 9: lapseStillDegraded,
	} {
		assert.Equal(t, want, stageFor(inARow), inARow)
	}
}

// A lapse denies only a line that can change state: a pipeline of sed, grep and awk reads,
// and goes through with the note.
func TestUnjudgedPassesAReadOnlyPipeline(t *testing.T) {
	ctx, _ := spawnFixture(t)
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	req := Request{Input: `sed -n 340,360p f; sed -n 955,990p g; grep -n x g | awk -F: '{print $1}'`, Host: "claude-code", Session: "s1"}
	v := JudgeWithin(ctx, Dependencies{}, req, func(context.Context) Verdict {
		<-release
		return Verdict{}
	})
	assert.Equal(t, verdictWithRule(decisionAdvise, string(advisoryUnfinished)), unworded(v))
	assert.Equal(t, unjudgedNote(), v.Context)
}

// A workspace open that never answers does not hold a write's judgment past its deadline:
// every rule that opens the workspace goes silent, the first by its own bound and the rest
// at once, and the verdict is on time.
func TestJudgeWithinAnswersAWriteWhoseWorkspaceOpenStalls(t *testing.T) {
	ctx, root, _ := writeFixture(t)
	release := make(chan struct{})
	defer close(release)
	var opens atomic.Int32
	deps := Dependencies{Inspect: func(context.Context, string) (types.WorkspaceRepository, error) {
		opens.Add(1)
		<-release
		return nil, errors.New("released")
	}}
	req := Request{Input: filepath.Join(root, "notes.txt"), IsPath: true, Host: "claude-code", Session: "s1"}
	start := time.Now()
	v := JudgeWithin(ctx, deps, req, func(ctx context.Context) Verdict { return Judge(ctx, deps, req) })
	assert.Less(t, time.Since(start), hookBudget)
	assert.NotEqual(t, string(advisoryUnfinished), v.Rule, "judged, not lapsed")
	assert.Equal(t, int32(1), opens.Load(), "one stalled open per judgment")
}
