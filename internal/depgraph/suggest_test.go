package depgraph

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"lib", "libx", 1},
		{"flaw", "lawn", 2},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, levenshtein(c.a, c.b), "levenshtein(%q, %q)", c.a, c.b)
		// Edit distance is symmetric.
		assert.Equalf(t, c.want, levenshtein(c.b, c.a), "levenshtein(%q, %q) reversed", c.b, c.a)
	}
}

func TestNearestProjectPath(t *testing.T) {
	w := workspace(
		[]string{"services/api"},
		[]string{"services/web"},
		[]string{"libs/core"},
	)

	// One transposition/typo within threshold resolves to the closest path.
	assert.Equal(t, "services/api", nearestProjectPath("services/apo", w))
	// A near-miss on a different candidate.
	assert.Equal(t, "libs/core", nearestProjectPath("libs/cor", w))
}

func TestNearestProjectPath_NoneWithinThreshold(t *testing.T) {
	w := workspace(
		[]string{"services/api"},
		[]string{"libs/core"},
	)
	assert.Empty(t, nearestProjectPath("totally-different-name", w))
}

func TestNearestProjectPath_ShortInputHasZeroThreshold(t *testing.T) {
	// len(want)/3 == 0 for inputs shorter than 3, so nothing is ever suggested.
	w := workspace([]string{"ab"})
	assert.Empty(t, nearestProjectPath("ax", w))
}

func TestNearestProjectPath_TieBreaksOnLexicallySmallerPath(t *testing.T) {
	// Both "aaa" and "aab" are distance 1 from "aac"; the smaller path wins.
	w := &types.Workspace{
		Root: "/repo",
		Projects: map[string]*types.Project{
			"aab": {Path: "aab"},
			"aaa": {Path: "aaa"},
		},
	}
	assert.Equal(t, "aaa", nearestProjectPath("aac", w))
}
