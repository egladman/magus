package job

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	base := types.Job{ID: "u1", Criteria: "original goal", Model: "standard"}
	got := applyMerge(t, map[string]any{"state": "running"}, base)
	assert.Equal(t, types.Job{ID: "u1", Criteria: "original goal", Model: "standard", State: types.StateRunning}, got,
		"an absent key must not erase what an earlier put set")
	assert.Equal(t, base, applyPut(t, types.JobPut{}, base), "a put naming nothing changes nothing")
}

func TestMergeAnExplicitEmptyValueClears(t *testing.T) {
	t.Parallel()

	base := types.Job{ID: "u1", Criteria: "original goal"}
	got := applyMerge(t, map[string]any{"criteria": ""}, base)
	assert.Empty(t, got.Criteria, "goal present with an empty value clears it, unlike an absent key")
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

	_, err := DecodePut(map[string]any{"criteria": 3, "read_only": "yes", "state": "passed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "criteria")
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

	_, err := PutMerge(types.JobPut{Enter: new("cmd/magus/x/a.go"), Criteria: new("x"), ReadOnly: new(true)})
	require.ErrorContains(t, err, "so criteria does not belong beside it")
	require.ErrorContains(t, err, "so read_only does not belong beside it")

	got := applyPut(t, types.JobPut{Enter: new("cmd/magus/x/a.go")}, types.Job{ID: "u1"})
	assert.Equal(t, []types.JobEntry{{Path: "cmd/magus/x/a.go"}}, got.Entries)
}
