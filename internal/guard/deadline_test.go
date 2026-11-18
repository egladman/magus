package guard

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// lapsedWork holds a wait for each piece of work a deadline stopped waiting for that no
// test has waited for since. TestMain hands leftRunning its hold.
var lapsedWork unwaitedWork

// lapsedWorkBound is how long a test waits for the work it left running before it fails.
const lapsedWorkBound = time.Minute

type unwaitedWork struct {
	mu      sync.Mutex
	waits   []func()
	waiters map[testing.TB]bool
}

func (u *unwaitedWork) hold(wait func()) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.waits = append(u.waits, wait)
}

func (u *unwaitedWork) take() []func() {
	u.mu.Lock()
	defer u.mu.Unlock()
	waits := u.waits
	u.waits = nil
	return waits
}

// enlist reports whether t had not enlisted yet.
func (u *unwaitedWork) enlist(t testing.TB) bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.waiters[t] {
		return false
	}
	if u.waiters == nil {
		u.waiters = map[testing.TB]bool{}
	}
	u.waiters[t] = true
	return true
}

func (u *unwaitedWork) dismiss(t testing.TB) {
	u.mu.Lock()
	defer u.mu.Unlock()
	delete(u.waiters, t)
}

// waitForLapsedWork makes t wait, once it returns, for every phase a deadline left
// running. Call it after t sets the dirs that work reads and before it registers the
// cleanup that releases a stalled phase: cleanups run last first, so the wait comes after
// the release and before the dirs are put back. Later calls for the same t add nothing.
func waitForLapsedWork(t testing.TB) {
	t.Helper()
	if !lapsedWork.enlist(t) {
		return
	}
	t.Cleanup(func() {
		defer lapsedWork.dismiss(t)
		drainLapsedWork(t)
	})
}

// drainLapsedWork waits for the work held so far and for what that work leaves running in
// turn, as a lapsed judgment's own phases lapse after it. It fails t past lapsedWorkBound.
func drainLapsedWork(t testing.TB) {
	t.Helper()
	bound := time.After(lapsedWorkBound)
	for waits := lapsedWork.take(); len(waits) > 0; waits = lapsedWork.take() {
		for _, wait := range waits {
			done := make(chan struct{})
			go func() {
				defer close(done)
				wait()
			}()
			select {
			case <-done:
			case <-bound:
				t.Errorf("work a deadline left running did not finish within %s", lapsedWorkBound)
				return
			}
		}
	}
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

// InheritedHookDeadlineSuite pins the deadline a delegate inherits.
type InheritedHookDeadlineSuite struct {
	suite.Suite
}

func TestInheritedHookDeadlineSuite(t *testing.T) {
	suite.Run(t, new(InheritedHookDeadlineSuite))
}

func (s *InheritedHookDeadlineSuite) TestAnEmptyValueChangesNothing() {
	ctx, cancel, err := WithInheritedHookDeadline(s.T().Context(), "")
	defer cancel()
	s.Require().NoError(err)
	_, bounded := ctx.Deadline()
	s.False(bounded)
}

func (s *InheritedHookDeadlineSuite) TestAMalformedValueIsAnError() {
	_, cancel, err := WithInheritedHookDeadline(s.T().Context(), "soon")
	defer cancel()
	s.ErrorContains(err, HookDeadlineEnv)
}

func (s *InheritedHookDeadlineSuite) TestANonPositiveValueIsAnError() {
	_, cancel, err := WithInheritedHookDeadline(s.T().Context(), "0")
	defer cancel()
	s.ErrorContains(err, "unix milliseconds")
}

func (s *InheritedHookDeadlineSuite) TestAPastDeadlineIsAnError() {
	past := strconv.FormatInt(time.Now().Add(-time.Second).UnixMilli(), 10)
	_, cancel, err := WithInheritedHookDeadline(s.T().Context(), past)
	defer cancel()
	s.ErrorContains(err, "had no budget left")
}

func (s *InheritedHookDeadlineSuite) TestADeadlineBeyondTheBudgetIsAnError() {
	far := strconv.FormatInt(time.Now().Add(hookBudget+time.Minute).UnixMilli(), 10)
	_, cancel, err := WithInheritedHookDeadline(s.T().Context(), far)
	defer cancel()
	s.ErrorContains(err, "past the hook's 7s budget")
}

func (s *InheritedHookDeadlineSuite) TestADeadlineWithinTheBudgetBoundsTheContext() {
	want := time.Now().Add(hookBudget / 2)
	ctx, cancel, err := WithInheritedHookDeadline(s.T().Context(), strconv.FormatInt(want.UnixMilli(), 10))
	defer cancel()
	s.Require().NoError(err)
	got, _ := ctx.Deadline()
	s.Equal(want.UnixMilli(), got.UnixMilli())
}

func (s *InheritedHookDeadlineSuite) TestAnInheritedDeadlineEarlierThanTheBudgetStands() {
	parent := strconv.FormatInt(time.Now().Add(time.Second).UnixMilli(), 10)
	inherited, cancelInherited, err := WithInheritedHookDeadline(s.T().Context(), parent)
	defer cancelInherited()
	s.Require().NoError(err)
	ctx, cancel := WithHookDeadline(inherited)
	defer cancel()
	got, _ := ctx.Deadline()
	s.Less(time.Until(got), 1100*time.Millisecond)
}

func (s *InheritedHookDeadlineSuite) TestTheJudgmentLapsesWithTheInheritedDeadline() {
	waitForLapsedWork(s.T())
	parent := strconv.FormatInt(time.Now().Add(100*time.Millisecond).UnixMilli(), 10)
	ctx, cancel, err := WithInheritedHookDeadline(s.T().Context(), parent)
	defer cancel()
	s.Require().NoError(err)
	release := make(chan struct{})
	defer close(release)
	req := Request{Input: "cat >> $TARGET <<'EOF'\nline\nEOF", Host: "claude-code", Session: "inherited"}
	start := time.Now()
	v := JudgeWithin(ctx, Dependencies{}, req, func(context.Context) verdict.Judgment {
		<-release
		return verdict.Judgment{Decision: verdict.Defer}
	})
	s.Equal(string(advisoryUnfinished), v.Rule)
	s.Less(time.Since(start), hookBudget/2)
}

// DelegateHookDeadlineSuite runs a shell script as a hook call's delegate.
type DelegateHookDeadlineSuite struct {
	suite.Suite
	out strings.Builder
}

func TestDelegateHookDeadlineSuite(t *testing.T) {
	suite.Run(t, new(DelegateHookDeadlineSuite))
}

func (s *DelegateHookDeadlineSuite) SetupTest() {
	s.out.Reset()
}

// run runs body as a /bin/sh delegate under ctx.
func (s *DelegateHookDeadlineSuite) run(ctx context.Context, body string) (DelegateEnd, error) {
	path := filepath.Join(s.T().TempDir(), "delegate")
	s.Require().NoError(os.WriteFile(path, fmt.Appendf(nil, "#!/bin/sh\n%s\n", body), 0o755))
	return RunDelegate(ctx, path, nil, os.Environ(), strings.NewReader(""), &s.out, io.Discard)
}

// done is a context already cancelled, so the grace runs from the start.
func (s *DelegateHookDeadlineSuite) done() context.Context {
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()
	return ctx
}

// runCancelledOnStart runs body, cancelling ctx once the script has started.
// A fresh script's first exec can stall past the grace on macOS.
func (s *DelegateHookDeadlineSuite) runCancelledOnStart(body string) (DelegateEnd, error) {
	started := filepath.Join(s.T().TempDir(), "started")
	ctx, cancel := context.WithCancel(s.T().Context())
	defer cancel()
	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(started); err == nil {
				cancel()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	return s.run(ctx, fmt.Sprintf("touch '%s'\n%s", started, body))
}

func (s *DelegateHookDeadlineSuite) TestTheDeadlineGoesWithTheDelegate() {
	ctx, cancel := context.WithTimeout(s.T().Context(), time.Minute)
	defer cancel()
	want, _ := ctx.Deadline()
	end, err := s.run(ctx, fmt.Sprintf(`printf '%%s' "$%s"`, HookDeadlineEnv))
	s.Require().NoError(err)
	s.Equal(DelegateExited, end)
	s.Equal(strconv.FormatInt(want.UnixMilli(), 10), s.out.String())
}

func (s *DelegateHookDeadlineSuite) TestAContextWithoutADeadlineHandsNoneOn() {
	end, err := s.run(s.T().Context(), fmt.Sprintf(`printf '%%s' "${%s-unset}"`, HookDeadlineEnv))
	s.Require().NoError(err)
	s.Equal(DelegateExited, end)
	s.Equal("unset", s.out.String())
}

func (s *DelegateHookDeadlineSuite) TestAFailedExitIsAnExitAndItsStatus() {
	end, err := s.run(s.T().Context(), "exit 3")
	s.Equal(DelegateExited, end)
	var exited *exec.ExitError
	s.Require().ErrorAs(err, &exited)
	s.Equal(3, exited.ExitCode())
}

func (s *DelegateHookDeadlineSuite) TestAnAnswerWithinTheGraceIsAnExit() {
	end, err := s.runCancelledOnStart("sleep 0.1; printf ok")
	s.Require().NoError(err)
	s.Equal(DelegateExited, end)
	s.Equal("ok", s.out.String())
}

func (s *DelegateHookDeadlineSuite) TestADelegateKilledBeforeWritingIsUnanswered() {
	start := time.Now()
	end, _ := s.run(s.done(), "exec sleep 60")
	s.Equal(DelegateKilledUnanswered, end)
	s.Less(time.Since(start), 30*time.Second, "well short of the 60s sleep")
}

func (s *DelegateHookDeadlineSuite) TestADelegateKilledAfterWritingIsAnswering() {
	end, _ := s.runCancelledOnStart("printf partial; exec sleep 60")
	s.Equal(DelegateKilledAnswering, end)
	s.Equal("partial", s.out.String())
}

func (s *DelegateHookDeadlineSuite) TestAGrandchildHoldingTheOutputDoesNotHoldTheKill() {
	start := time.Now()
	end, _ := s.run(s.done(), "sleep 60 & exec sleep 60")
	s.Equal(DelegateKilledUnanswered, end)
	s.Less(time.Since(start), 30*time.Second, "well short of the grandchild's 60s")
}

func (s *DelegateHookDeadlineSuite) TestAGrandchildHoldingTheOutputDoesNotHoldAnExit() {
	start := time.Now()
	end, err := s.run(s.T().Context(), "sleep 60 & printf ok")
	s.ErrorIs(err, exec.ErrWaitDelay)
	s.Equal(DelegateExited, end)
	s.Equal("ok", s.out.String())
	s.Less(time.Since(start), 30*time.Second, "well short of the grandchild's 60s")
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
	assert.Equal(t, journal.Phase{Name: phaseLoadRules, ElapsedMs: unbounded.ElapsedMs}, unbounded)
}

// A phase that runs out answers the deadline's error and is recorded lapsed, and every
// later call of it in the judgment answers at once without running. The call itself never
// sees ctx's cancel, so a computed-once answer cannot be cached as cancelled.
func TestBoundedPhaseLapsesOncePerJudgment(t *testing.T) {
	waitForLapsedWork(t)
	ctx, cancel := context.WithTimeout(withPhaseLog(t.Context(), journal.Phase{}), judgeReserve+50*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	detached := make(chan bool, 1)

	_, err := boundedPhase(ctx, phaseInspect, func(run context.Context) (string, error) {
		detached <- run.Done() == nil
		<-release
		return "ws", nil
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.True(t, <-detached, "the call runs without ctx's cancel")
	ran := false
	_, err = boundedPhase(ctx, phaseInspect, func(context.Context) (string, error) {
		ran = true
		return "ws", nil
	})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.False(t, ran, "a lapsed phase is not waited on twice")
	phases := judgmentPhases(ctx).finished()
	require.Len(t, phases, 1)
	assert.Equal(t, phaseInspect, phases[0].Name)
	assert.True(t, phases[0].Lapsed)
}

// A phase that answers in time is not lapsed, an error included: only a wait that ran out
// lapses it.
func TestBoundedPhaseAnswersInTime(t *testing.T) {
	ctx := withPhaseLog(t.Context(), journal.Phase{})
	failed := errors.New("magusfile.buzz does not load")

	got, err := boundedPhase(ctx, phaseInspect, func(context.Context) (string, error) { return "ws", nil })
	require.NoError(t, err)
	assert.Equal(t, "ws", got)
	_, err = boundedPhase(ctx, phaseTargetPolicies, func(context.Context) (string, error) { return "", failed })
	require.ErrorIs(t, err, failed)

	phases := judgmentPhases(ctx).finished()
	require.Len(t, phases, 2)
	assert.False(t, phases[0].Lapsed)
	assert.False(t, phases[1].Lapsed, "an error answered in time is the call's own, not a lapse")
	assert.Equal(t, hookBudget.Milliseconds(), phases[0].BudgetMs, "no deadline leaves the hook budget")
}

// A judgment stuck where no deadline reaches still answers in time: past the hook's bound a
// call that changes state is denied, one that only reads goes through with a note, and the
// trail records the judgment as lapsed. The host got its answer, so the mark is ended.
func TestJudgeWithinAnswersAStuckJudgmentByTheDeadline(t *testing.T) {
	// A host's read tool, as the hook glue restates it.
	read := `{"hook_event_name":"PreToolUse","session_id":"s1","tool_name":"Read","tool_input":{"command":"sed -n 1,40p notes.txt"}}`
	cases := map[string]struct {
		req      Request
		decision verdict.Decision
		tool     string
	}{
		"a command that changes state": {req: Request{Input: "touch notes.txt"}, decision: verdict.Deny, tool: hookToolCommand},
		"a file write":                 {req: Request{Input: "notes.txt", IsPath: true}, decision: verdict.Deny, tool: hookToolWrite},
		"a command that reads":         {req: Request{Input: "ls -la"}, decision: verdict.Advise, tool: hookToolCommand},
		"a host's read":                {req: Request{Input: read}, decision: verdict.Advise, tool: hookToolCommand},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			ctx, cacheDir := spawnFixture(t)
			waitForLapsedWork(t)
			ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
			defer cancel()
			release := make(chan struct{})
			defer close(release)
			req := c.req
			req.Host, req.Session = "claude-code", "s1"

			start := time.Now()
			v := JudgeWithin(ctx, Dependencies{}, req, func(context.Context) verdict.Judgment {
				<-release
				return verdict.Judgment{Decision: verdict.Defer}
			})
			assert.Less(t, time.Since(start), 2*time.Second)
			assert.Equal(t, verdictWithRule(c.decision, string(advisoryUnfinished)), unworded(v))
			if c.decision == verdict.Deny {
				assert.Contains(t, v.Reason, "the guard's judgment did not finish within 7s. A call that changes state is denied rather than let through unjudged. A retry passes only if judging costs less this time, such as on a less loaded machine; if it lapses twice, report it rather than repeat it.")
			} else {
				assert.Equal(t, unjudgedNote(), v.Context)
			}

			rows := callsOfKind(t, cacheDir, journal.CallKindAgentCommand)
			require.Len(t, rows, 1)
			assert.Equal(t, c.tool, rows[0].Name)
			require.NotNil(t, rows[0].Guard)
			require.NotEmpty(t, rows[0].Guard.Phases)
			wantPhase := rows[0].Guard.Phases[0]
			wantPhase.Name, wantPhase.BudgetMs = phaseAnswer, hookBudget.Milliseconds()
			assert.Equal(t, wantPhase, rows[0].Guard.Phases[0])
			assert.True(t, rows[0].Guard.Phases[0].Lapsed)

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
	want := verdict.Judgment{Decision: verdict.Advise, Context: "judged"}
	judge := func(context.Context) verdict.Judgment { return want }
	assert.Equal(t, want, JudgeWithin(ctx, Dependencies{}, Request{Input: "ls", Host: "claude-code", Session: "s1"}, judge))

	short, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	slow := func(context.Context) verdict.Judgment {
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
		RuleLoad:    journal.Phase{Name: phaseLoadRules, ElapsedMs: 6600, BudgetMs: 6500, Lapsed: true},
	}

	ctx, _ := spawnFixture(t)
	write := Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "notes.txt", IsPath: true})
	assert.Equal(t, verdict.Deny, write.Decision)
	assert.Contains(t, write.Reason, "magus ran out of time judging this file write: loading this workspace's guard rules did not finish within 6.5s.")

	read := Judge(ctx, deps, Request{Host: "claude-code", Session: "s1", Input: "ls"})
	assert.Equal(t, verdict.Advise, read.Decision)
	assert.Contains(t, read.Context, "The working tree's magus\\guard.command rule judged nothing: the magusfile did not load within 6.5s")
}

// A dry run grades for a person at a terminal, a fork or a verb's own edit, so its rules
// run under a bound of their own, well past the hook's budget: a cold first load there must
// not read as a lapse, and a rule that loops must not hang the fork.
func TestDryRunJudgesWithoutTheHookBudget(t *testing.T) {
	ctx, _ := spawnFixture(t)
	var left []time.Duration
	rule := func(ctx context.Context, _ types.CommandRequest, _ hint.Gate) (verdict.Verdict, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		left = append(left, time.Until(deadline))
		return verdict.Verdict{}, nil
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
	waitForLapsedWork(t)
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	loops := func(ctx context.Context, _ types.WriteRequest, _ hint.Gate) (verdict.Verdict, error) {
		<-ctx.Done()
		return verdict.Verdict{}, ctx.Err()
	}
	v := Judge(ctx, Dependencies{WriteRule: loops}, Request{Input: filepath.Join(root, "notes.txt"), IsPath: true, DryRun: true})
	assert.Equal(t, verdict.Deny, v.Decision)
	assert.Contains(t, v.Reason, "magus did not finish grading this file write: ")
	assert.Contains(t, v.Reason, "so it is not graded. A dry run is bounded at 2m0s")
}

// A session whose every judgment runs out of time is not locked: its first
// lapsesBeforeDegrade calls that change state are denied, the ones after pass with a notice
// that names the phase that stalled once and then only says the guard is still not judging,
// and a judgment that answers in time starts the count over. The judgment here is slow
// rather than stuck, the shape a loaded machine gives every phase.
// Decision 127 keeps a write in a job's checkout denied; see LapseDegradeSuite.
func TestJudgeWithinDegradesACommandAfterConsecutiveLapses(t *testing.T) {
	ctx, cacheDir := spawnFixture(t)
	waitForLapsedWork(t)
	req := Request{Input: "touch notes.txt", Host: "claude-code", Session: "s1"}
	slowLoad := func(ctx context.Context) verdict.Judgment {
		_, _, _ = TimeRuleLoad(ctx, func() (int, error) {
			time.Sleep(time.Second)
			return 0, nil
		})
		return verdict.Judgment{Decision: verdict.Defer}
	}
	lapse := func() verdict.Judgment {
		short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		return JudgeWithin(short, Dependencies{}, req, slowLoad)
	}

	for range lapsesBeforeDegrade {
		assert.Equal(t, verdict.Deny, lapse().Decision)
	}
	degraded := lapse()
	assert.Equal(t, verdictWithRule(verdict.Advise, string(advisoryUnfinished)), unworded(degraded))
	assert.Contains(t, degraded.Context, "on 3 judgments in a row in this session, so this command ran with no rule judging it")
	assert.Contains(t, degraded.Context, "This one stalled in its load_rules phase")
	assert.Contains(t, degraded.Context, "Nothing measured why")
	assert.NotContains(t, degraded.Context, "loaded machine")
	assert.NotContains(t, degraded.Context, "magus server status")
	again := lapse()
	wantAgain := again
	wantAgain.Decision = verdict.Advise
	wantAgain.Context = "The guard is still not finishing in time (4 judgments in a row), so this command ran unjudged, as the notice before said."
	assert.Equal(t, wantAgain, again)

	onTime := JudgeWithin(ctx, Dependencies{}, req, func(context.Context) verdict.Judgment { return verdict.Judgment{Decision: verdict.Defer} })
	assert.Equal(t, verdict.Defer, onTime.Decision)
	assert.Equal(t, verdict.Deny, lapse().Decision, "an answer in time starts the count over")

	rows := callsOfKind(t, cacheDir, journal.CallKindAgentCommand)
	require.NotEmpty(t, rows)
	names := make([]string, len(rows[0].Guard.Phases))
	for i, p := range rows[0].Guard.Phases {
		names[i] = p.Name
	}
	assert.Equal(t, []string{phaseLoadRules, phaseAnswer, phaseJudge}, names, "the lapse row names the phase underway")
	assert.True(t, rows[0].Guard.Phases[0].Lapsed)
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
		assert.Equal(t, want, stageAtLapses(inARow), inARow)
	}
}

// A lapse denies only a line that can change state: a pipeline of sed, grep and awk reads,
// and goes through with the note.
func TestUnjudgedPassesAReadOnlyPipeline(t *testing.T) {
	ctx, _ := spawnFixture(t)
	waitForLapsedWork(t)
	ctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	req := Request{Input: `sed -n 340,360p f; sed -n 955,990p g; grep -n x g | awk -F: '{print $1}'`, Host: "claude-code", Session: "s1"}
	v := JudgeWithin(ctx, Dependencies{}, req, func(context.Context) verdict.Judgment {
		<-release
		return verdict.Judgment{}
	})
	assert.Equal(t, verdictWithRule(verdict.Advise, string(advisoryUnfinished)), unworded(v))
	assert.Equal(t, unjudgedNote(), v.Context)
}

// A lapse passes a heredoc appended to a file outside every checkout and the workspace, such
// as a plan in the host's own directory, with a note saying so. The same append into the
// workspace, or beside a verb that changes state, is still denied.
func TestUnjudgedPassesAnAppendOutsideEveryCheckout(t *testing.T) {
	ctx, _ := spawnFixture(t)
	workspace := hookLocation(ctx, Dependencies{}).workspace
	require.NotEmpty(t, workspace)
	outside := filepath.Join(t.TempDir(), "plans", "README.md")
	nothing := func(context.Context) {}
	appendTo := func(target string) string {
		return fmt.Sprintf("cat >> %s <<'EOF'\n\n## Decision\n\n- ship it\nEOF", target)
	}

	for i, line := range []string{
		appendTo(outside),
		fmt.Sprintf("cd %s && %s", filepath.Dir(outside), appendTo("README.md")),
		fmt.Sprintf("tee -a %s <<'EOF' >/dev/null\nline\nEOF", outside),
		fmt.Sprintf("echo done > %s", outside),
	} {
		v := lapseNow(ctx, t, Dependencies{}, Request{Input: line, Host: "claude-code", Session: "s1"}, nothing)
		assert.Equal(t, verdictWithRule(verdict.Advise, string(advisoryUnfinished)), unworded(v), "line %d: %s", i, line)
		assert.Equal(t, unjudgedOutsideNote(), v.Context, line)
	}

	for i, line := range []string{
		appendTo(filepath.Join(workspace, "notes.md")),
		fmt.Sprintf("%s && touch %s.bak", appendTo(outside), outside),
		appendTo("relative.md"),
		"cat >> $TARGET <<'EOF'\nline\nEOF",
	} {
		v := lapseNow(ctx, t, Dependencies{}, Request{Input: line, Host: "claude-code", Session: "deny-" + strconv.Itoa(i)}, nothing)
		assert.Equal(t, verdictWithRule(verdict.Deny, string(advisoryUnfinished)), unworded(v), "line %d: %s", i, line)
	}
}

// A workspace open that never answers does not hold a write's judgment past its deadline:
// every rule that opens the workspace goes silent, the first by its own bound and the rest
// at once, and the verdict is on time.
func TestJudgeWithinAnswersAWriteWhoseWorkspaceOpenStalls(t *testing.T) {
	ctx, root, _ := writeFixture(t)
	waitForLapsedWork(t)
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
	v := JudgeWithin(ctx, deps, req, func(ctx context.Context) verdict.Judgment { return Judge(ctx, deps, req) })
	assert.Less(t, time.Since(start), hookBudget)
	assert.NotEqual(t, string(advisoryUnfinished), v.Rule, "judged, not lapsed")
	assert.Equal(t, int32(1), opens.Load(), "one stalled open per judgment")
}

// A pick grades each file it writes, and each file another checkout's
// job holds asks for this checkout's projects. Opens must not follow the
// file count.
func TestJudgeOpensAWorkspaceOnceForALargePick(t *testing.T) {
	r := newWorktreeRepository(t)
	for i := range 23 {
		name := fmt.Sprintf("pkg/f%d.go", i)
		require.NoError(t, os.MkdirAll(filepath.Join(r.main, "pkg"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(r.main, name), []byte("x\n"), 0o644))
		r.git(r.main, "add", "--", name)
	}
	r.git(r.main, "commit", "-q", "-m", "write 23 files")
	r.git(r.main, "branch", "picked")

	wt, sibling := r.add("lease-job"), r.add("sibling")
	worker := narrowLease()
	worker.Parent, worker.CheckoutRoot, worker.Paths.Write = "orchestrator", wt, []string{"pkg/**"}
	holder := job.Job{ID: "sibling", Agent: job.Instructed("hold the package"), Paths: job.Paths{Write: []string{"pkg/**"}}, State: job.StateRunning, Registered: 1, CheckoutRoot: sibling}
	ctx, _ := fleetFixture(t, worker, holder)

	opens := map[string]int{}
	deps := Dependencies{Inspect: func(_ context.Context, root string) (types.WorkspaceRepository, error) {
		opens[root]++
		return nil, errors.New("no workspace")
	}}
	Judge(ctx, deps, Request{Input: "git cherry-pick picked", Dir: wt, Lease: worker.ID, Host: "claude-code", Session: "s1"})
	assert.Equal(t, 1, opens[wt], "opens by root: %v", opens)
}

// inertBenchRules is a root magusfile with one rule of each kind a hook loads, so the judged
// arm pays for parsing and registering them as a hook does.
const inertBenchRules = `import "magus";

magus\guard.shell({"name": "no-frobnicate", "decision": "deny", "program": "frobnicate", "reason": "Not here."});

magus\guard.command(fun (req: CommandRequest) > GuardVerdict {
    return magus\guard.defer();
});
`

// BenchmarkInertCallLoadsNoRules is one hook call for a todo list, as Claude Code's catch-all
// entry forwards it. Each call reads the tool rows directory, as each hook process does.
// fast is the read-only fast path; judged is the same call when no fast path answers it:
// the workspace's rules loaded, as the CLI loads them inside the hook's bound, then judged.
func BenchmarkInertCallLoadsNoRules(b *testing.B) {
	testkit.Isolate(b)
	repo := testkit.NewRepository(b, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": inertBenchRules})
	root := repo.Root()
	for _, f := range testToolFiles() {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f.Path)), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f.Path), []byte(f.Content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	cacheDir := b.TempDir()
	ctx := WithLocation(b.Context(), cacheDir, root, root)
	req := Request{
		Input: fmt.Sprintf("{\"hook_event_name\":\"PreToolUse\",\"session_id\":\"bench\",\"cwd\":\"%s\",\"tool_name\":\"TodoWrite\",\"tool_input\":{\"todos\":[{\"content\":\"read the guard\",\"status\":\"pending\"}]}}", root),
		Host:  "claude-code", Form: "buzz", Session: "bench",
	}
	load := func(ctx context.Context) Dependencies {
		deps, rules := NewDependencies(ctx, Wiring{Root: root, CacheDir: func(string) (string, error) { return cacheDir, nil }})
		if rules == nil || deps.LoadFailure != nil {
			b.Fatalf("the rules did not load: %v", deps.LoadFailure)
		}
		return deps
	}
	b.Run("fast", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			req.Tools = agent.FindToolRows(root, testToolHost)
			judge := func(ctx context.Context) verdict.Judgment { return Judge(ctx, load(ctx), req) }
			if v := JudgeWithin(ctx, Dependencies{}, req, judge); v.Decision != verdict.Defer {
				b.Fatalf("a todo list was not passed: %+v", v)
			}
		}
	})
	b.Run("judged", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			req.Tools = agent.FindToolRows(root, testToolHost)
			if v := Judge(ctx, load(ctx), req); v.Decision != verdict.Defer {
				b.Fatalf("a todo list was not passed: %+v", v)
			}
		}
	})
}

// BenchmarkPlainWriteWithAPendingPolicy is one hook judging a file write while the working
// tree edits the root magusfile, so the approved sources load beside it: the compiled rules'
// settings and the write rule both read them.
func BenchmarkPlainWriteWithAPendingPolicy(b *testing.B) {
	testkit.Isolate(b)
	b.Setenv("XDG_CACHE_HOME", b.TempDir())
	b.Setenv("XDG_STATE_HOME", b.TempDir())
	const policy = "import \"magus\";\n\nmagus\\guard.write(fun (req: WriteRequest) > GuardVerdict {\n    return magus\\guard.defer();\n});\n"
	repo := testkit.NewRepository(b, map[string]string{"magus.yaml": "{}\n", "magusfile.buzz": policy})
	root := repo.Root()
	repo.Magusfile(policy + "\nfun unused() > void {}\n")
	cacheDir := b.TempDir()
	ctx := WithLocation(b.Context(), cacheDir, root, root)
	req := Request{
		Input: fmt.Sprintf(`{"hook_event_name":"PreToolUse","session_id":"bench","cwd":%q,"tool_name":"Write","tool_input":{"file_path":%q,"content":"x\n"}}`,
			root, filepath.Join(root, "notes.txt")),
		Host: "claude-code", Session: "bench",
	}
	b.ReportAllocs()
	for b.Loop() {
		deps, rules := NewDependencies(ctx, Wiring{Root: root, CacheDir: func(string) (string, error) { return cacheDir, nil }})
		if rules == nil || deps.LoadFailure != nil {
			b.Fatalf("the rules did not load: %v", deps.LoadFailure)
		}
		if v := Judge(ctx, deps, req); v.Decision == verdict.Deny {
			b.Fatalf("a plain write was denied: %+v", v)
		}
	}
}

// JudgeBudgetSuite pins what bounds a dry run's workspace open and what a lapse's
// trail row says.
type JudgeBudgetSuite struct {
	suite.Suite
}

func TestJudgeBudgetSuite(t *testing.T) {
	suite.Run(t, new(JudgeBudgetSuite))
}

func (s *JudgeBudgetSuite) inspectBudgetUnder(ctx context.Context) int64 {
	ctx = withPhaseLog(ctx, journal.Phase{})
	_, err := boundedPhase(ctx, phaseInspect, func(context.Context) (string, error) { return "ws", nil })
	s.Require().NoError(err)
	phases := judgmentPhases(ctx).finished()
	s.Require().Len(phases, 1)
	return phases[0].BudgetMs
}

func (s *JudgeBudgetSuite) TestDryRunOpenHasTheDryRunsBudget() {
	ctx, cancel := withDryRunDeadline(s.T().Context())
	defer cancel()
	s.Greater(s.inspectBudgetUnder(ctx), hookBudget.Milliseconds())
}

func (s *JudgeBudgetSuite) TestHookOpenHasTheHooksBudget() {
	ctx, cancel := WithHookDeadline(s.T().Context())
	defer cancel()
	got := s.inspectBudgetUnder(ctx)
	s.LessOrEqual(got, (hookBudget - judgeReserve).Milliseconds())
	s.Greater(got, (hookBudget - judgeReserve - time.Second).Milliseconds())
}

func (s *JudgeBudgetSuite) TestDryRunNestedInAHookHasTheHooksBudget() {
	hook, cancelHook := WithHookDeadline(s.T().Context())
	defer cancelHook()
	ctx, cancel := withDryRunDeadline(hook)
	defer cancel()
	got := s.inspectBudgetUnder(ctx)
	s.LessOrEqual(got, (hookBudget - judgeReserve).Milliseconds())
}

func (s *JudgeBudgetSuite) TestJudgeLapsedReportsARuleThatRanOut() {
	ctx, root, _ := writeFixture(s.T())
	waitForLapsedWork(s.T())
	ctx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	loops := func(ctx context.Context, _ types.WriteRequest, _ hint.Gate) (verdict.Verdict, error) {
		<-ctx.Done()
		return verdict.Verdict{}, ctx.Err()
	}
	v, lapsed := JudgeLapsed(ctx, Dependencies{WriteRule: loops}, Request{Input: filepath.Join(root, "notes.txt"), IsPath: true, DryRun: true})
	s.Equal(verdict.Deny, v.Decision)
	s.True(lapsed)
}

func (s *JudgeBudgetSuite) TestJudgeLapsedLeavesARealDenyAlone() {
	ctx, _, _ := writeFixture(s.T())
	v, lapsed := JudgeLapsed(ctx, Dependencies{}, Request{Input: "git reset --hard origin/main", DryRun: true})
	s.Equal(verdict.Deny, v.Decision)
	s.False(lapsed)
}

func (s *JudgeBudgetSuite) TestJudgeLapsedKeepsARuleDenyPastALapsedPhase() {
	ctx, _, _ := writeFixture(s.T())
	waitForLapsedWork(s.T())
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	release := make(chan struct{})
	defer close(release)
	stalls := func(context.Context, string) (types.TargetPolicyIndex, bool) {
		<-release
		return types.TargetPolicyIndex{}, false
	}
	// The policy read lapses first; the verdict is still reset's own rule.
	line := "./magus run test . && git reset --hard origin/main"
	v, lapsed := JudgeLapsed(ctx, Dependencies{TargetPolicies: stalls}, Request{Input: line, DryRun: true})
	s.Equal(verdict.Deny, v.Decision)
	s.NotEqual(string(advisoryUnfinished), v.Rule)
	s.NotContains(v.Reason, "did not finish grading")
	s.False(lapsed)
}

// A rule free to word its deny like a lapse must not read as one.
func (s *JudgeBudgetSuite) TestJudgeLapsedIgnoresARuleQuotingTheLapseWords() {
	ctx, root, _ := writeFixture(s.T())
	quotes := func(context.Context, types.WriteRequest, hint.Gate) (verdict.Verdict, error) {
		return verdict.Verdict{Decision: verdict.Deny, Reason: "magus did not finish grading this"}, nil
	}
	v, lapsed := JudgeLapsed(ctx, Dependencies{WriteRule: quotes}, Request{Input: filepath.Join(root, "notes.txt"), IsPath: true, DryRun: true})
	s.Equal(verdict.Deny, v.Decision)
	s.False(lapsed)
}

func (s *JudgeBudgetSuite) TestJudgeLapsedReportsNoLapseOnAPass() {
	ctx, _, _ := writeFixture(s.T())
	v, lapsed := JudgeLapsed(ctx, Dependencies{}, Request{Input: "ls", DryRun: true})
	s.NotEqual(verdict.Deny, v.Decision)
	s.False(lapsed)
}

func (s *JudgeBudgetSuite) TestTrailNamesBuiltinRulesAndNotices() {
	ctx, cacheDir := spawnFixture(s.T())
	Judge(ctx, Dependencies{}, Request{Input: "ls", Host: "claude-code", Session: "s1"})
	rows := callsOfKind(s.T(), cacheDir, journal.CallKindAgentCommand)
	s.Require().NotEmpty(rows)
	names := make([]string, len(rows[0].Guard.Phases))
	for i, p := range rows[0].Guard.Phases {
		names[i] = p.Name
	}
	s.Contains(names, phaseBuiltinRules)
	s.Contains(names, phaseNotices)
}

func (s *JudgeBudgetSuite) TestStallBetweenPhasesNamesTheSpan() {
	log := &phaseLog{start: time.Now()}
	log.enclose(phaseBuiltinRules)
	log.begin(phaseInspect)
	log.record(timedPhase(phaseInspect, time.Millisecond, time.Second, false))
	stalled, ok := log.stalled()
	s.True(ok)
	s.Equal(phaseBuiltinRules, stalled.Name)
}

func (s *JudgeBudgetSuite) TestLapsedRowKeepsABoundLease() {
	row := job.Job{ID: "bound-job", Agent: job.Instructed("hold a lease"), State: job.StateRunning, Paths: job.Paths{Write: []string{"internal/held/**"}}}
	ctx, _ := fleetFixture(s.T(), row)
	who := hookAttribution{Host: "claude-code", Session: "8f2c6a1e", Agent: "a1b2c3"}
	bindCaller(s.T(), ctx, who, row.ID)
	req := Request{Input: bashCall(s.T(), who.Session, who.Agent, "ls"), Host: who.Host}

	v := lapseNow(ctx, s.T(), Dependencies{}, req, func(context.Context) {})

	s.Equal(row.ID, v.Lease)
	rows := callsOfKind(s.T(), hookLocation(ctx, Dependencies{}).cacheDir, journal.CallKindAgentCommand)
	s.Require().NotEmpty(rows)
	s.Equal(row.ID, rows[len(rows)-1].Lease)
}
