package job

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applyMerge(t *testing.T, params map[string]any, base types.Job) types.Job {
	t.Helper()
	apply, err := ParseMerge(params)
	require.NoError(t, err)
	apply(&base)
	return base
}

func TestMergeTouchesOnlyNamedFields(t *testing.T) {
	t.Parallel()

	base := types.Job{ID: "u1", Goal: "original goal", Model: "standard"}
	got := applyMerge(t, map[string]any{"state": "running"}, base)
	assert.Equal(t, types.StateRunning, got.State)
	assert.Equal(t, "original goal", got.Goal, "an absent key must not erase what an earlier put set")
	assert.Equal(t, "standard", got.Model)
}

func TestMergeAnExplicitEmptyValueClears(t *testing.T) {
	t.Parallel()

	base := types.Job{ID: "u1", Goal: "original goal"}
	got := applyMerge(t, map[string]any{"goal": ""}, base)
	assert.Empty(t, got.Goal, "goal present with an empty value clears it, unlike an absent key")
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

	_, err := ParseMerge(map[string]any{"state": "passed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_return")
}

func TestMergeReportsEveryMistypedFieldTogether(t *testing.T) {
	t.Parallel()

	_, err := ParseMerge(map[string]any{"goal": 3, "read_only": "yes"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "goal")
	assert.Contains(t, err.Error(), "read_only")
}

// A typo'd key read as a field that was taken into account, and the tool then reported
// the row as written.
func TestMergeRejectsAKeyNoRowCarries(t *testing.T) {
	t.Parallel()

	_, err := ParseMerge(map[string]any{"op": "put", "id": "u1", "state": "running", "passsed": true})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "passsed")
	assert.Contains(t, err.Error(), "write_paths", "the message names what a put does carry")

	_, err = ParseMerge(map[string]any{"op": "put", "id": "u1", "state": "running"})
	assert.NoError(t, err, "op and id name the call rather than a field")
}

func TestMergeRejectsAListElementOfTheWrongType(t *testing.T) {
	t.Parallel()

	_, err := ParseMerge(map[string]any{"depends_on": []any{"a", 2}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "depends_on")
}
