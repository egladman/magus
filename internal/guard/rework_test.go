package guard

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/agent/model"
	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const reworkTitle = "orchestrator/integrator guard-facts"

type reworkFixture struct {
	at    location
	facts hint.Gate
	store *job.Store
}

func newReworkFixture(t *testing.T) reworkFixture {
	t.Helper()
	testkit.Isolate(t)
	root := testkit.NewWorkspace(t).Root()
	cacheDir := t.TempDir()
	return reworkFixture{
		at:    location{cacheDir: cacheDir, workspace: root, dir: root},
		facts: hint.NewGate(cacheDir, FactsKey("claude-code", "8f2c6a1e")),
		store: job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}),
	}
}

func (f reworkFixture) putJob(t *testing.T, state jobtypes.State) {
	t.Helper()
	row := jobtypes.Job{ID: "guard-facts", Agent: jobtypes.Instructed("prove it"), Paths: jobtypes.Paths{Write: []string{"a.go"}}, Validation: "magus run test", State: state}
	_, err := f.store.Update(f.testContext(t), row.ID, func(cur *jobtypes.Job) { *cur = row })
	require.NoError(t, err)
}

func (f reworkFixture) rounds(t *testing.T) int {
	t.Helper()
	rows, err := f.store.List(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	return rows[0].ReworkRounds
}

func TestRecordReworkCountsAContinuationSentAfterTheWorkerHandedBack(t *testing.T) {
	tests := []struct {
		name  string
		state jobtypes.State
		want  int
	}{
		{"an exited job", jobtypes.StateExited, 1},
		{"a passed job", jobtypes.StatePass, 1},
		{"a failed job", jobtypes.StateFail, 1},
		{"a job still running", jobtypes.StateRunning, 0},
		{"a job nobody took", jobtypes.StateDeclared, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReworkFixture(t)
			f.putJob(t, tc.state)
			writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle, Name: "brisk-heron"})

			recordRework(f.testContext(t), f.at, f.facts, "a1b2c3")

			assert.Equal(t, tc.want, f.rounds(t))
		})
	}
}

func TestRecordReworkAttributesOnlyWhatTheSpawnRecordNames(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, jobtypes.StateExited)

	recordRework(f.testContext(t), f.at, f.facts, "ghost")
	assert.Zero(t, f.rounds(t), "an agent magus never saw names no job")

	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle, UntrustedJob: "guard-facts"})
	recordRework(f.testContext(t), f.at, f.facts, "a1b2c3")
	assert.Zero(t, f.rounds(t), "a title its spawner could not hand out attributes nothing")

	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: "orchestrator/integrator another-job"})
	recordRework(f.testContext(t), f.at, f.facts, "a1b2c3")
	assert.Zero(t, f.rounds(t), "a title naming a job that does not exist")

	recordRework(f.testContext(t), location{}, f.facts, "a1b2c3")
	recordRework(f.testContext(t), f.at, f.facts, "")
	assert.Zero(t, f.rounds(t), "no cache or no address is no record")
}

func TestRecordReworkResolvesAnAgentByItsName(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, jobtypes.StateExited)
	writeAgentMarker(f.facts, agentAliasKind("brisk-heron"), []byte("a1b2c3"))
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle, Name: "brisk-heron"})

	recordRework(f.testContext(t), f.at, f.facts, "brisk-heron")

	assert.Equal(t, 1, f.rounds(t))
}

func TestRecordReworkWritesNothingInADryRun(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, jobtypes.StateExited)
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle})
	dry, cancel := withDryRunDeadline(f.testContext(t))
	defer cancel()

	recordRework(dry, f.at, f.facts, "a1b2c3")

	assert.Zero(t, f.rounds(t))
}

func TestColdContinuationPinsExpiryAndKnownWarmCost(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, jobtypes.StateRunning)
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{ID: "a1b2c3", Description: reworkTitle, Model: "sonnet", ContextTokens: int64Pointer(100)})
	f.at.models = func() []model.Row { return []model.Row{{ID: "sonnet"}} }
	now := time.Now().Truncate(time.Second)
	row, ok := continuedJob(f.testContext(t), f.at, f.facts, "a1b2c3")
	require.True(t, ok)
	_, err := f.store.Update(f.testContext(t), row.ID, func(cur *jobtypes.Job) { cur.Updated = now.Unix() })
	require.NoError(t, err)

	_, cold := coldContinuation(f.testContext(t), f.at, f.facts, "a1b2c3", now)
	assert.False(t, cold, "known model, context, and recent activity establish a warm dispatch")

	ttl := resumeTTL(accountAt(f.at.workspace)).ttl
	_, err = f.store.Update(f.testContext(t), row.ID, func(cur *jobtypes.Job) { cur.Updated = now.Add(-ttl).Unix() })
	require.NoError(t, err)
	ageAgentSeen(t, f, "a1b2c3", now.Add(-ttl))
	reason, cold := coldContinuation(f.testContext(t), f.at, f.facts, "a1b2c3", now)
	assert.True(t, cold, "the exact TTL boundary is expired")
	assert.Contains(t, reason, "past its")
}

func TestColdContinuationRequiresEvidenceBeforeDispatch(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, jobtypes.StateRunning)
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{ID: "a1b2c3", Description: reworkTitle})

	reason, cold := coldContinuation(f.testContext(t), f.at, f.facts, "a1b2c3", time.Now())
	assert.True(t, cold)
	assert.Contains(t, reason, "context size is unknown")
}

func TestColdContinuationAsksTheRootBeforeDispatch(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, jobtypes.StateRunning)
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{ID: "a1b2c3", Description: reworkTitle, Name: "brisk-heron"})
	ageAgentSeen(t, f, "a1b2c3", time.Now().Add(-time.Hour))
	row, ok := continuedJob(f.testContext(t), f.at, f.facts, "a1b2c3")
	require.True(t, ok)
	_, err := f.store.Update(f.testContext(t), row.ID, func(cur *jobtypes.Job) { cur.Updated = time.Now().Add(-time.Hour).Unix() })
	require.NoError(t, err)
	continueTo := `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"brisk-heron","message":"Continue the work."},"tool_use_id":"toolu_03"}`

	v := Judge(f.testContext(t), Dependencies{}, Request{Input: continueTo, Host: "claude-code", RendersAsk: true})

	assert.Equal(t, verdict.Judgment{Decision: verdict.Ask, Rule: string(denyRuleSpawnColdContinuation)}, judgedAs(v), v.Reason)
	assert.Contains(t, v.Reason, "Approve this cold continuation")
}

func TestColdContinuationCannotBeApprovedByAWorker(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, jobtypes.StateRunning)
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{ID: "a1b2c3", Description: reworkTitle, Name: "brisk-heron"})
	ageAgentSeen(t, f, "a1b2c3", time.Now().Add(-time.Hour))
	continueTo := `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"brisk-heron","message":"Continue the work."},"tool_use_id":"toolu_04"}`

	v := Judge(f.testContext(t), Dependencies{}, Request{Input: continueTo, Host: "claude-code", Lease: "guard-facts", RendersAsk: true})

	assert.Equal(t, verdict.Judgment{Decision: verdict.Deny, Rule: string(denyRuleSpawnColdContinuation)}, judgedAs(v), v.Reason)
	assert.Contains(t, v.Reason, "cannot approve its own spend")
}

func TestColdContinuationWithoutHumanPromptIsDenied(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, jobtypes.StateRunning)
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{ID: "a1b2c3", Description: reworkTitle, Name: "brisk-heron"})
	ageAgentSeen(t, f, "a1b2c3", time.Now().Add(-time.Hour))
	continueTo := `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"a1b2c3","message":"Continue the work."},"tool_use_id":"toolu_05"}`

	v := Judge(f.testContext(t), Dependencies{}, Request{Input: continueTo, Host: "claude-code"})

	assert.Equal(t, verdict.Judgment{Decision: verdict.Deny, Rule: string(denyRuleSpawnColdContinuation)}, judgedAs(v), v.Reason)
	assert.Contains(t, v.Reason, "cannot render a human approval prompt")
	reason, _, _ := strings.Cut(v.Reason, "\n")
	fields := strings.Fields(reason)
	rule := fields[len(fields)-1]
	require.True(t, continuationApproval(rule), v.Reason)
	row, ok := continuedJob(f.testContext(t), f.at, f.facts, "a1b2c3")
	require.True(t, ok)
	require.NoError(t, GrantJobRule(f.testContext(t), f.store, io.Discard, nil, row.ID, rule, 0))
	retry := func(input string) verdict.Decision {
		return Judge(f.testContext(t), Dependencies{}, Request{Input: input, Host: "claude-code"}).Decision
	}
	assert.Equal(t, verdict.Deny, retry(strings.Replace(continueTo, "Continue the work.", "Change scope.", 1)))
	assert.NotEqual(t, verdict.Deny, retry(strings.Replace(continueTo, "toolu_05", "toolu_06", 1)))
	assert.Equal(t, verdict.Deny, retry(continueTo), "a second cold retry needs another approval")
}

func int64Pointer(n int64) *int64 { return &n }

func ageAgentSeen(t *testing.T, f reworkFixture, agent string, at time.Time) {
	t.Helper()
	kind := agentSeenKind(agent)
	f.facts.Touch(kind)
	path := hint.MarkerPath(f.facts.CacheDir(), f.facts.Session(), kind)
	require.NoError(t, os.Chtimes(path, at, at))
}

func TestAContinueJudgedByTheGuardCountsAsReworkOnce(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, jobtypes.StateExited)
	deps := Dependencies{}
	Judge(f.testContext(t), deps, Request{Input: finishedSpawn(t, reworkTitle, "brisk-heron", "sonnet", "", "a1b2c3"), Host: "claude-code"})
	require.Zero(t, f.rounds(t), "the spawn is not a continuation")

	continueTo := `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"brisk-heron","message":"Now fix the review comments."},"tool_use_id":"toolu_02"}`
	Judge(f.testContext(t), deps, Request{Input: continueTo, Host: "claude-code", RendersAsk: true})
	assert.Zero(t, f.rounds(t), "asking does not dispatch a turn")
	after := strings.Replace(continueTo, "PreToolUse", "PostToolUse", 1)
	Judge(f.testContext(t), deps, Request{Input: after, Host: "claude-code"})
	assert.Equal(t, 1, f.rounds(t))
}

// ContinueToExitedWorkerSuite pins the advice the root gets when it continues
// a worker whose job exited or ended. It informs and never denies.
type ContinueToExitedWorkerSuite struct {
	suite.Suite
	f reworkFixture
}

func TestContinueToExitedWorkerSuite(t *testing.T) {
	suite.Run(t, new(ContinueToExitedWorkerSuite))
}

func (s *ContinueToExitedWorkerSuite) SetupTest() {
	s.f = newReworkFixture(s.T())
	s.f.putJob(s.T(), jobtypes.StateRunning)
	Judge(s.f.testContext(s.T()), Dependencies{}, Request{Input: finishedSpawn(s.T(), reworkTitle, "brisk-heron", "sonnet", "", "a1b2c3"), Host: "claude-code"})
}

func (s *ContinueToExitedWorkerSuite) continueWorker(state jobtypes.State) verdict.Judgment {
	s.f.putJob(s.T(), state)
	continueTo := `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"brisk-heron","message":"Widen with job apply and fix the review comments."},"tool_use_id":"toolu_02"}`
	return Judge(s.f.testContext(s.T()), Dependencies{}, Request{Input: continueTo, Host: "claude-code", RendersAsk: true})
}

func (s *ContinueToExitedWorkerSuite) TestContinueToAnExitedWorkerAdvisesTheRoot() {
	v := s.continueWorker(jobtypes.StateExited)
	s.Equal(verdict.Ask, v.Decision)
	s.Contains(v.Context, "job guard-facts is exited; widen it yourself with `")
	s.Contains(v.Context, "job exec guard-facts")
}

func (s *ContinueToExitedWorkerSuite) TestContinueToAnEndedWorkerAdvisesTheRoot() {
	v := s.continueWorker(jobtypes.StatePass)
	s.Equal(verdict.Ask, v.Decision)
	s.Contains(v.Context, "job guard-facts is pass; fork a new job for more work")
}

// A continued worker is still bound: no advice and no rework round.
func (s *ContinueToExitedWorkerSuite) TestContinueToAStoppedWorkerSaysNothing() {
	v := s.continueWorker(jobtypes.StateRunning)
	s.Equal(verdict.Ask, v.Decision)
	s.NotContains(v.Context, "job guard-facts is")
	s.Zero(s.f.rounds(s.T()))
}

func (s *ContinueToExitedWorkerSuite) TestContinueToAnOtherwiseEndedNoReturnWorkerAdvisesFork() {
	v := s.continueWorker(jobtypes.StateNoReturn)
	s.Equal(verdict.Ask, v.Decision)
	s.Contains(v.Context, "fork a new job for more work")
	s.NotContains(v.Context, "job exec guard-facts")
}

func (s *ContinueToExitedWorkerSuite) TestContinueToARunningWorkerSaysNothing() {
	v := s.continueWorker(jobtypes.StateRunning)
	s.Equal(verdict.Ask, v.Decision)
	s.NotContains(v.Context, "job guard-facts is")
}

func (s *ContinueToExitedWorkerSuite) TestContinueToAnExitedWorkerNeverDenies() {
	v := s.continueWorker(jobtypes.StateExited)
	s.Equal(verdict.Ask, v.Decision, v.Reason)
}

func (f reworkFixture) testContext(t *testing.T) context.Context {
	return withSecrets(WithLocation(t.Context(), f.at.cacheDir, f.at.workspace, f.at.dir))
}
