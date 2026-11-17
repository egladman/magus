package impact

import (
	"testing"

	"github.com/egladman/magus/internal/ci/forecast"
	"github.com/egladman/magus/types/impact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImpactCostRefusesToPriceAnUnmeasuredReach pins the one number this section must never
// print.
//
// forecast always answers PredictDuration (it falls back to a workspace percentile and then to
// a compiled-in constant), so a total is computable for a workspace nobody has ever timed. That
// total is a fabrication wearing a duration, and quoting it would teach a reader to trust every
// later one.
func TestImpactCostRefusesToPriceAnUnmeasuredReach(t *testing.T) {
	affected := []impact.Project{{Path: "root", Seed: true, Files: []string{"a.go"}}, {Path: "docs"}}

	t.Run("no history at all", func(t *testing.T) {
		assert.Nil(t, estimateCost(&forecast.History{}, affected))
	})

	t.Run("a project below the sample floor is not a measurement", func(t *testing.T) {
		h := &forecast.History{Projects: map[string]map[string]forecast.Stats{
			"root": {"ci": {P75Ms: 90_000, Samples: minSamples - 1}},
		}}
		assert.Nil(t, estimateCost(h, affected))
	})

	t.Run("only the measured projects are counted, and the rest are not guessed at", func(t *testing.T) {
		h := &forecast.History{Projects: map[string]map[string]forecast.Stats{
			"root": {"ci": {P75Ms: 90_000, Samples: 12}},
			"docs": {"lint": {P75Ms: 5_000, Samples: 40}},
		}}
		c := estimateCost(h, affected)
		require.NotNil(t, c)
		require.Len(t, c.Projects, 1, "docs declares no ci or test target the history has timed")
		assert.Equal(t, &costEstimate{
			TotalMs:  90_000,
			Projects: []projectCost{{Project: "root", Target: "ci", Ms: 90_000, Samples: 12}},
		}, c)
	})

	t.Run("ci outranks test as the estimate of a whole rebuild", func(t *testing.T) {
		h := &forecast.History{Projects: map[string]map[string]forecast.Stats{
			"root": {
				"ci":   {P75Ms: 90_000, Samples: 12},
				"test": {P75Ms: 30_000, Samples: 99},
			},
		}}
		c := estimateCost(h, affected)
		require.NotNil(t, c)
		assert.Equal(t, "ci", c.Projects[0].Target)
	})
}
