package job

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func applyMerge(t *testing.T, params map[string]any, base types.Job) types.Job {
	t.Helper()
	put, err := DecodePut(params)
	require.NoError(t, err)
	apply, err := PutMerge(put)
	require.NoError(t, err)
	apply(&base)
	return base
}

func applyPut(t *testing.T, put types.JobPut, base types.Job) types.Job {
	t.Helper()
	apply, err := PutMerge(put)
	require.NoError(t, err)
	apply(&base)
	return base
}

func TestMergeTouchesOnlyNamedFields(t *testing.T) {
	t.Parallel()

	base := types.Job{ID: "u1", Agent: &agent.Spec{Instructions: "original goal", Model: agent.Model{Tier: "bounded"}, Worker: agent.Worker{Type: agent.WorkerTypeResumed}}}
	got := applyMerge(t, map[string]any{"state": "running"}, base)
	assert.Equal(t, types.Job{ID: "u1", Agent: &agent.Spec{Instructions: "original goal", Model: agent.Model{Tier: "bounded"}, Worker: agent.Worker{Type: agent.WorkerTypeResumed}}, State: types.StateRunning}, got,
		"an absent key must not erase what an earlier put set")
	assert.Equal(t, base, applyPut(t, types.JobPut{}, base), "a put naming nothing changes nothing")
}

func TestMergeAnExplicitEmptyValueClears(t *testing.T) {
	t.Parallel()

	base := types.Job{ID: "u1", Agent: agent.Instructed("original goal")}
	got := applyMerge(t, map[string]any{"agent": nil}, base)
	assert.Nil(t, got.Agent, "agent present with a null value clears it, unlike an absent key")
}

func TestMergeListAcceptsBothWireForms(t *testing.T) {
	t.Parallel()

	spaceForm := applyMerge(t, map[string]any{"write_paths": "a/b c/d"}, types.Job{})
	assert.Equal(t, []string{"a/b", "c/d"}, spaceForm.WritePaths)

	arrayForm := applyMerge(t, map[string]any{"write_paths": []any{"a/b", "c/d"}}, types.Job{})
	assert.Equal(t, []string{"a/b", "c/d"}, arrayForm.WritePaths)
}

// compat: see the legacy fields on Row.
func TestMergeRejectsAnUnknownState(t *testing.T) {
	t.Parallel()

	_, err := DecodePut(map[string]any{"state": "passed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_return")

	_, err = PutMerge(types.JobPut{State: new(types.JobState("passed"))})
	require.ErrorContains(t, err, "no_return", "a Go caller is held to the same vocabulary")
}

func TestMergeReportsEveryMistypedFieldTogether(t *testing.T) {
	t.Parallel()

	_, err := DecodePut(map[string]any{"agent": 3, "read_only": "yes", "state": "passed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "agent")
	assert.Contains(t, err.Error(), "read_only")
	assert.Contains(t, err.Error(), "state must be one of", "a refusal PutMerge makes joins the decoding errors")
}

// A typo'd key read as a field that was taken into account, and the tool then reported
// the row as written.
func TestMergeRejectsAKeyNoRowCarries(t *testing.T) {
	t.Parallel()

	_, err := DecodePut(map[string]any{"op": "put", "id": "u1", "state": "running", "passsed": true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "passsed")
	assert.Contains(t, err.Error(), "write_paths", "the message names what a put does carry")

	_, err = DecodePut(map[string]any{"op": "put", "id": "u1", "state": "running"})
	require.NoError(t, err, "op and id name the call rather than a field")
}

func TestMergeRejectsAListElementOfTheWrongType(t *testing.T) {
	t.Parallel()

	_, err := DecodePut(map[string]any{"depends_on": []any{"a", 2}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "depends_on")
}

func TestMergeReadsMaxAttemptsAsAWholeCount(t *testing.T) {
	t.Parallel()

	for _, v := range []any{3, int64(3), float64(3), " 3 "} {
		assert.Equal(t, 3, applyMerge(t, map[string]any{"max_attempts": v}, types.Job{}).MaxAttempts, "%#v", v)
	}
	assert.Zero(t, applyMerge(t, map[string]any{"max_attempts": 0}, types.Job{MaxAttempts: 2}).MaxAttempts,
		"zero clears the cap")
	for _, v := range []any{2.5, -1, "three", true} {
		_, err := DecodePut(map[string]any{"max_attempts": v})
		require.ErrorContains(t, err, "max_attempts", "%#v", v)
	}
	_, err := PutMerge(types.JobPut{MaxAttempts: new(-1)})
	require.ErrorContains(t, err, "max_attempts")
}

func TestMergeSetsOrClearsTheCheck(t *testing.T) {
	t.Parallel()

	base := types.Job{ID: "u1"}
	set := applyMerge(t, map[string]any{"check": "test ."}, base)
	require.NotNil(t, set.Check)
	assert.Equal(t, "test", set.Check.Target)
	assert.Equal(t, set.Check.String(), set.Validation, "the row stores both halves")

	cleared := applyPut(t, types.JobPut{Check: &types.LeaseCheck{}}, set)
	assert.Nil(t, cleared.Check, "a check naming neither a target nor a script clears it")
	assert.Empty(t, cleared.Validation)
	assert.Nil(t, applyMerge(t, map[string]any{"check": " "}, set).Check, "an empty check line clears it")

	_, err := DecodePut(map[string]any{"check": "test .", "validation": "magus run test ."})
	require.ErrorContains(t, err, "not both")
}

func TestMergeKeepsAnEntryAlone(t *testing.T) {
	t.Parallel()

	_, err := PutMerge(types.JobPut{Enter: new("cmd/magus/x/a.go"), Agent: agent.Instructed("x"), ReadOnly: new(true)})
	require.ErrorContains(t, err, "so agent does not belong beside it")
	require.ErrorContains(t, err, "so read_only does not belong beside it")

	got := applyPut(t, types.JobPut{Enter: new("cmd/magus/x/a.go")}, types.Job{ID: "u1"})
	assert.Equal(t, []types.JobEntry{{Path: "cmd/magus/x/a.go"}}, got.Entries)
}

// A put sets a live row's brief format, or clears it back to magus.yaml's, and refuses a
// value that is neither format.
func TestPutSetsTheBriefFormat(t *testing.T) {
	t.Parallel()
	got := applyPut(t, types.JobPut{BriefFormat: new(types.BriefFormatBuzz)}, types.Job{ID: "u1"})
	assert.Equal(t, types.BriefFormatBuzz, got.BriefFormat)
	got = applyPut(t, types.JobPut{BriefFormat: new(types.BriefFormat(""))}, got)
	assert.Empty(t, got.BriefFormat)

	_, err := PutMerge(types.JobPut{BriefFormat: new(types.BriefFormat("json"))})
	require.ErrorContains(t, err, `brief_format "json"`)
}

func TestMergeReadsMaxClaimsAsAWholeCount(t *testing.T) {
	t.Parallel()

	for _, v := range []any{4, int64(4), float64(4), " 4 "} {
		assert.Equal(t, 4, applyMerge(t, map[string]any{"max_claims": v}, types.Job{}).MaxClaims, "%#v", v)
	}
	assert.Zero(t, applyMerge(t, map[string]any{"max_claims": 0}, types.Job{MaxClaims: 2}).MaxClaims)
	for _, v := range []any{2.5, -1, "three", true} {
		_, err := DecodePut(map[string]any{"max_claims": v})
		require.ErrorContains(t, err, "max_claims", "%#v", v)
	}
}

type putDeliverySuite struct {
	suite.Suite
}

func TestPutDelivery(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(putDeliverySuite))
}

func (s *putDeliverySuite) TestAPutSetsTheDeliveryAndParentHead() {
	got := applyPut(s.T(), types.JobPut{Delivery: new(types.DeliveryStacked), ParentHead: new("feat/p")}, types.Job{ID: "u1"})
	s.Equal(types.Job{ID: "u1", Delivery: types.DeliveryStacked, ParentHead: "feat/p"}, got)
}

func (s *putDeliverySuite) TestAnUnknownDeliveryIsRefused() {
	_, err := PutMerge(types.JobPut{Delivery: new("merge")})
	s.Require().ErrorContains(err, `delivery "merge" is not one of fold, separate, stacked`)
}

// putAgentSuite pins the agent key of a put: the whole section, strict.
type putAgentSuite struct {
	suite.Suite
}

func TestPutAgent(t *testing.T) { suite.Run(t, new(putAgentSuite)) }

func (s *putAgentSuite) TestASectionReplacesTheRowsWhole() {
	got := applyMerge(s.T(), map[string]any{"agent": map[string]any{
		"model": map[string]any{"tier": "design"}, "worker": map[string]any{"type": "resumed"},
	}}, types.Job{ID: "u1", Agent: agent.Instructed("old")})
	s.Equal(&agent.Spec{Model: agent.Model{Tier: "design"}, Worker: agent.Worker{Type: agent.WorkerTypeResumed}}, got.Agent)
}

func (s *putAgentSuite) TestASectionAsAJSONStringIsRead() {
	got := applyMerge(s.T(), map[string]any{"agent": `{"instructions": "ship it", "worker": {"type": "resumed"}}`}, types.Job{ID: "u1"})
	s.Equal(agent.Instructed("ship it"), got.Agent)
}

func (s *putAgentSuite) TestAnUnknownMemberIsRefused() {
	_, err := DecodePut(map[string]any{"agent": map[string]any{"criteria": "x", "worker": map[string]any{"type": "resumed"}}})
	s.ErrorContains(err, "job: agent must be an object")
}

func (s *putAgentSuite) TestASectionNamingNoWorkerIsRefused() {
	_, err := DecodePut(map[string]any{"agent": map[string]any{"instructions": "ship it"}})
	s.ErrorContains(err, "names no worker.type")
}

func (s *putAgentSuite) TestANewWorkerNeedsAReason() {
	_, err := DecodePut(map[string]any{"agent": map[string]any{"worker": map[string]any{"type": "new"}}})
	s.ErrorContains(err, "worker.reason")
}
