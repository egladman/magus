package guard

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/file"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// filedTry is job id's first try, having filed an empty result.
func filedTry(id string) types.Attempt {
	return filedResult(id, &types.JobResult{Job: id})
}

// filedResult is job id's first try, having filed result.
func filedResult(id string, result *types.JobResult) types.Attempt {
	return types.Attempt{Job: id, N: 1, Status: types.AttemptStatus{Result: result}}
}

// storeTries appends tries to the attempts file of the job store at
// places. The job package files a result only through an exit. A copy:
// an exported fixture breaches the export-scope ceiling.
func storeTries(t *testing.T, at location, tries ...types.Attempt) {
	t.Helper()
	path, err := job.NewStore(at.jobs()).Path()
	require.NoError(t, err)
	lines := make([][]byte, 0, len(tries))
	for _, a := range tries {
		lines = append(lines, fixtureAttemptLine(t, a))
	}
	require.NoError(t, file.AppendLines(filepath.Join(filepath.Dir(path), "attempts.jsonl"), lines, 0o600))
}

// fixtureAttemptLine is a as an attempts.jsonl record, its keys
// camelCased. A copy, as storeTries is; enum values pass through as Go
// spells them, which the store reads unchanged.
func fixtureAttemptLine(t *testing.T, a types.Attempt) []byte {
	t.Helper()
	raw, err := json.Marshal(a.Status)
	require.NoError(t, err)
	var status map[string]any
	require.NoError(t, json.Unmarshal(raw, &status))
	if state, ok := status["state"]; ok {
		delete(status, "state")
		status["job_state"] = state
	}
	line, err := json.Marshal(map[string]any{
		"apiVersion": "batch.magus/v1alpha1", "kind": "Attempt",
		"spec": map[string]any{"job": a.Job, "n": a.N}, "status": fixtureCamelKeys(status),
	})
	require.NoError(t, err)
	return line
}

// fixtureCamelKeys is v with every object key camelCased.
func fixtureCamelKeys(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			parts := strings.Split(k, "_")
			for i := 1; i < len(parts); i++ {
				parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
			}
			out[strings.Join(parts, "")] = fixtureCamelKeys(x)
		}
		return out
	case []any:
		for i := range v {
			v[i] = fixtureCamelKeys(v[i])
		}
		return v
	}
	return v
}

type StopUnfiledSuite struct {
	suite.Suite
	// tries are the attempts the judged store holds.
	tries []types.Attempt
}

func TestStopUnfiledSuite(t *testing.T) { suite.Run(t, new(StopUnfiledSuite)) }

func (s *StopUnfiledSuite) SetupTest() { s.tries = nil }

func (s *StopUnfiledSuite) row() types.Job {
	return types.Job{ID: "spec/worker", Agent: agent.Instructed("build the spec"), Paths: types.JobPaths{Write: []string{"types/**"}},
		State: types.StateRunning, Registered: 1}
}

// stop is a subagent's stop for the leased row, judged in a store holding row.
func (s *StopUnfiledSuite) stop(row types.Job, event string, fields map[string]any) types.GuardJudgment {
	ctx, root := fleetFixture(s.T(), row)
	if len(s.tries) > 0 {
		storeTries(s.T(), ctx.Value(locationKey{}).(location), s.tries...)
	}
	env := map[string]any{"session_id": "8f2c6a1e", "transcript_path": "/nonexistent/root.jsonl", "cwd": root,
		"hook_event_name": event, "stop_hook_active": false}
	for k, v := range fields {
		env[k] = v
	}
	return s.judge(ctx, hookJSON(s.T(), env), row.ID)
}

// judge is the stop's judgment with the guard's version cleared, so a
// case compares the rest whole.
func (s *StopUnfiledSuite) judge(ctx context.Context, input, lease string) types.GuardJudgment {
	v := Judge(ctx, Dependencies{}, Request{Host: "claude-code", Input: input, Lease: lease})
	v.APIVersion = ""
	return v
}

func (s *StopUnfiledSuite) subagent() map[string]any {
	return map[string]any{"agent_id": "a05d44b006f8f68af", "agent_transcript_path": "/nonexistent/child.jsonl"}
}

func (s *StopUnfiledSuite) TestAnUnfiledLeasedChildIsRefusedNamingTheExit() {
	v := s.stop(s.row(), "SubagentStop", s.subagent())
	s.Equal(ruledAs(decisionDeny, string(denyRuleStopUnfiled)), judgedAs(v))
	s.Contains(v.Reason, "`magus job exit spec/worker --stdin`")
	s.Contains(v.Reason, "magus job exit --schema")
}

func (s *StopUnfiledSuite) TestAReadOnlyRowFilesAnEmptyChangeList() {
	row := s.row()
	row.ReadOnly, row.Paths.Write = true, nil
	v := s.stop(row, "SubagentStop", s.subagent())
	s.Equal(decisionDeny, v.Decision)
	s.Contains(v.Reason, `"changed_paths": []`)
}

func (s *StopUnfiledSuite) TestTheSecondStopPassesAndSaysSo() {
	fields := s.subagent()
	fields["stop_hook_active"] = true
	v := s.stop(s.row(), "SubagentStop", fields)
	s.Equal(ruledAs(decisionAdvise, string(denyRuleStopUnfiled)), judgedAs(v))
	s.Contains(v.Context, "`magus job wait spec/worker`")
}

func (s *StopUnfiledSuite) TestAFiledResultLetsTheChildStop() {
	row := s.row()
	s.tries = []types.Attempt{filedTry(row.ID)}
	v := s.stop(row, "SubagentStop", s.subagent())
	s.NotEqual(string(denyRuleStopUnfiled), v.Rule)
	s.NotEqual(decisionDeny, v.Decision)
}

func (s *StopUnfiledSuite) TestARootStopIsNotJudged() {
	v := s.stop(s.row(), "Stop", nil)
	s.NotEqual(string(denyRuleStopUnfiled), v.Rule)
}
