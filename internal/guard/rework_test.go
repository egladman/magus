package guard

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/fixture"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const reworkTitle = "orchestrator/integrator guard-facts"

type reworkFixture struct {
	ctx   context.Context
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
		ctx:   WithLocation(t.Context(), cacheDir, root, root),
		at:    location{cacheDir: cacheDir, workspace: root, dir: root},
		facts: hint.NewGate(cacheDir, FactsKey("claude-code", "8f2c6a1e")),
		store: job.NewStore(job.Location{StateBase: fixture.StateBase(t), CacheDir: cacheDir, Root: root}),
	}
}

func (f reworkFixture) putJob(t *testing.T, state types.JobState) {
	t.Helper()
	row := types.Job{ID: "guard-facts", Criteria: "prove it", WritePaths: []string{"a.go"}, Validation: "magus run test", State: state}
	_, err := f.store.Update(f.ctx, row.ID, func(cur *types.Job) { *cur = row })
	require.NoError(t, err)
}

func (f reworkFixture) rounds(t *testing.T) int {
	t.Helper()
	rows, err := f.store.List()
	require.NoError(t, err)
	require.Len(t, rows, 1)
	return rows[0].ReworkRounds
}

func TestRecordReworkCountsAContinuationSentAfterTheWorkerHandedBack(t *testing.T) {
	tests := []struct {
		name  string
		state types.JobState
		want  int
	}{
		{"an exited job", types.StateExited, 1},
		{"a passed job", types.StatePass, 1},
		{"a failed job", types.StateFail, 1},
		{"a job still running", types.StateRunning, 0},
		{"a job nobody took", types.StateDeclared, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newReworkFixture(t)
			f.putJob(t, tc.state)
			writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle, Name: "brisk-heron"})

			recordRework(f.ctx, f.at, f.facts, "a1b2c3")

			assert.Equal(t, tc.want, f.rounds(t))
		})
	}
}

func TestRecordReworkAttributesOnlyWhatTheSpawnRecordNames(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, types.StateExited)

	recordRework(f.ctx, f.at, f.facts, "ghost")
	assert.Zero(t, f.rounds(t), "an agent magus never saw names no job")

	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle, UntrustedJob: "guard-facts"})
	recordRework(f.ctx, f.at, f.facts, "a1b2c3")
	assert.Zero(t, f.rounds(t), "a title its spawner could not hand out attributes nothing")

	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: "orchestrator/integrator another-job"})
	recordRework(f.ctx, f.at, f.facts, "a1b2c3")
	assert.Zero(t, f.rounds(t), "a title naming a job that does not exist")

	recordRework(f.ctx, location{}, f.facts, "a1b2c3")
	recordRework(f.ctx, f.at, f.facts, "")
	assert.Zero(t, f.rounds(t), "no cache or no address is no record")
}

func TestRecordReworkResolvesAnAgentByItsName(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, types.StateExited)
	writeAgentMarker(f.facts, agentAliasKind("brisk-heron"), []byte("a1b2c3"))
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle, Name: "brisk-heron"})

	recordRework(f.ctx, f.at, f.facts, "brisk-heron")

	assert.Equal(t, 1, f.rounds(t))
}

func TestRecordReworkWritesNothingInADryRun(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, types.StateExited)
	writeSpawnedAgent(f.facts, "a1b2c3", spawnedAgent{Description: reworkTitle})
	dry, cancel := withDryRunDeadline(f.ctx)
	defer cancel()

	recordRework(dry, f.at, f.facts, "a1b2c3")

	assert.Zero(t, f.rounds(t))
}

func TestAContinueJudgedByTheGuardCountsAsReworkOnce(t *testing.T) {
	f := newReworkFixture(t)
	f.putJob(t, types.StateExited)
	deps := Dependencies{}
	Judge(f.ctx, deps, Request{Input: finishedSpawn(t, reworkTitle, "brisk-heron", "sonnet", "", "a1b2c3"), Host: "claude-code"})
	require.Zero(t, f.rounds(t), "the spawn is not a continuation")

	continueTo := `{"session_id":"8f2c6a1e","cwd":"/Users/dev/repo","hook_event_name":"PreToolUse",` +
		`"tool_name":"SendMessage","tool_input":{"to":"brisk-heron","message":"Now fix the review comments."},"tool_use_id":"toolu_02"}`
	Judge(f.ctx, deps, Request{Input: continueTo, Host: "claude-code"})

	assert.Equal(t, 1, f.rounds(t))
}
