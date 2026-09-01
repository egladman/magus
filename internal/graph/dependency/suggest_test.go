package dependency

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

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
