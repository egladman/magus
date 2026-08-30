package ledger

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func applyMerge(t *testing.T, params map[string]any, base types.Lease) types.Lease {
	t.Helper()
	apply, err := Merge(params)
	require.NoError(t, err)
	apply(&base)
	return base
}

func TestMergeTouchesOnlyNamedFields(t *testing.T) {
	t.Parallel()

	base := types.Lease{ID: "u1", Goal: "original goal", Tier: "standard"}
	got := applyMerge(t, map[string]any{"state": "running"}, base)
	assert.Equal(t, types.StateRunning, got.State)
	assert.Equal(t, "original goal", got.Goal, "an absent key must not erase what an earlier put set")
	assert.Equal(t, "standard", got.Tier)
}

func TestMergeAnExplicitEmptyValueClears(t *testing.T) {
	t.Parallel()

	base := types.Lease{ID: "u1", Goal: "original goal"}
	got := applyMerge(t, map[string]any{"goal": ""}, base)
	assert.Empty(t, got.Goal, "goal present with an empty value clears it, unlike an absent key")
}

func TestMergeListAcceptsBothWireForms(t *testing.T) {
	t.Parallel()

	spaceForm := applyMerge(t, map[string]any{"owned_paths": "a/b c/d"}, types.Lease{})
	assert.Equal(t, []string{"a/b", "c/d"}, spaceForm.OwnedPaths)

	arrayForm := applyMerge(t, map[string]any{"owned_paths": []any{"a/b", "c/d"}}, types.Lease{})
	assert.Equal(t, []string{"a/b", "c/d"}, arrayForm.OwnedPaths)
}

func TestMergeRejectsAnUnknownState(t *testing.T) {
	t.Parallel()

	_, err := Merge(map[string]any{"state": "passed"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no_return")
}

func TestMergeReportsEveryMistypedFieldTogether(t *testing.T) {
	t.Parallel()

	_, err := Merge(map[string]any{"goal": 3, "read_only": "yes"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "goal")
	assert.Contains(t, err.Error(), "read_only")
}

func TestMergeRejectsAListElementOfTheWrongType(t *testing.T) {
	t.Parallel()

	_, err := Merge(map[string]any{"depends_on": []any{"a", 2}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "depends_on")
}
