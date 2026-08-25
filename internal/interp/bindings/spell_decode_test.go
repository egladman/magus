package bindings

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A field of the wrong type NAMES ITSELF. The alternative every ad hoc decoder reaches for is a
// zero value, and a silent zero is a wrong answer wearing a right answer's clothes: a mistyped
// pull-request number reads as "no pull request for this branch", which sends the reader to
// look at their branch when the fault is in their spell.
func TestAMistypedFieldNamesItselfRatherThanZeroing(t *testing.T) {
	_, err := intField(map[string]any{"number": "482"}, "number", "spell \"x\": open_review")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `spell "x": open_review`, "the context has to travel")
	assert.Contains(t, err.Error(), `"number"`, "and the field")
	assert.Contains(t, err.Error(), "want int", "and what was expected")

	_, err = strField(map[string]any{"repo": 7}, "repo", "where")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "want str")
}

// Absent and null are the ZERO VALUE, not an error. A Buzz object carries every declared field,
// so "declared nothing" and "did not declare" are one statement and must decode alike -
// otherwise two spellings of the same answer produce two different records.
func TestAbsentAndNullBothReadAsTheZeroValue(t *testing.T) {
	for _, m := range []map[string]any{{}, {"repo": nil, "number": nil}} {
		s, err := strField(m, "repo", "where")
		require.NoError(t, err)
		assert.Equal(t, "", s)

		n, err := intField(m, "number", "where")
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	}
}

// A Buzz integer that crossed a JSON boundary arrives as a float64, so accepting one is the
// ordinary case rather than a leniency. A float with a fractional part is still refused: a
// spell answering 4.5 for a pull-request number meant something this cannot guess.
func TestAWholeFloatIsAnIntegerAndAFractionalOneIsNot(t *testing.T) {
	n, err := intField(map[string]any{"number": float64(482)}, "number", "where")
	require.NoError(t, err)
	assert.Equal(t, 482, n)

	_, err = intField(map[string]any{"number": 4.5}, "number", "where")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "whole number")
}

// A non-string element is rejected rather than dropped, for the same reason everywhere in this
// file: a dropped source glob is a silently wrong cache key.
func TestAListRejectsAForeignElementRatherThanDroppingIt(t *testing.T) {
	_, err := strListField(map[string]any{"sources": []any{"a.go", 3}}, "sources", "where")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `"sources"[1]`, "the index names which element")
}
