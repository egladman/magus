package render

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestHeatBucket(t *testing.T) {
	assert.Equal(t, 0, heatBucket(0, 10), "no churn → coldest")
	assert.Equal(t, 0, heatBucket(5, 0), "no max → coldest")
	assert.Equal(t, 1, heatBucket(1, 10), "low churn → bucket 1")
	assert.Equal(t, 4, heatBucket(10, 10), "max churn → hottest")
	assert.Equal(t, 4, heatBucket(99, 10), "over max clamps to hottest")
}

func TestWriteInsightMarkdown(t *testing.T) {
	r := types.InsightReport{
		Hotspots: types.HotspotOutput{
			Commits: 50,
			Nodes:   []types.Node{{Path: "api", Churn: 3}},
			Files:   []types.FileHotspot{{Path: "api/a.go", Commits: 3, Complexity: 40, Score: 120, Authors: 1}},
		},
		Affinity:  types.AffinityOutput{Pairs: []types.CoChange{{A: "api", B: "web", Count: 2, Hidden: true}}},
		Ownership: types.OwnershipOutput{Projects: []types.OwnershipEntry{{Path: "api", PrimaryShare: 100, Authors: 1, Primary: "ada", BusFactor1: true}}},
		Trend:     types.TrendOutput{Projects: []types.TrendEntry{{Path: "api", Recent: 2, Earlier: 1, Delta: 1}}},
	}

	var b strings.Builder
	require.NoError(t, WriteInsightMarkdown(&b, r))
	s := b.String()

	assert.Contains(t, s, "# Insight")
	assert.Contains(t, s, "## Hotspots")
	assert.Contains(t, s, "## Affinity")
	assert.Contains(t, s, "## Ownership")
	assert.Contains(t, s, "```mermaid", "flowcharts provide dependency context")
	assert.NotContains(t, s, "quadrantChart", "the rich default omits the GitHub-incompatible quadrant chart")
	assert.Contains(t, s, "## Trend")
	assert.Contains(t, s, "**Next:**", "each lens gives an actionable next step")
	assert.Contains(t, s, "`api/a.go`", "lists the hottest file")
}

func TestWriteInsightMarkdownGitHubSafe(t *testing.T) {
	report := types.InsightReport{
		Hotspots: types.HotspotOutput{Nodes: []types.Node{{Path: ".", Name: "magus", Dir: "/repo/magus"}}},
		Affinity: types.AffinityOutput{Pairs: []types.CoChange{{A: ".", AName: "magus", B: "docs", BName: "docs", Count: 1}}},
	}
	var b strings.Builder
	require.NoError(t, WriteInsightMarkdown(&b, report))
	got := b.String()
	assert.Contains(t, got, "```mermaid")
	assert.NotContains(t, got, "quadrantChart")
	assert.NotContains(t, got, "click ")
	assert.NotContains(t, got, "---\nconfig:")
	assert.NotContains(t, got, "subgraph ")
	assert.NotContains(t, got, "classDef ")
	assert.NotContains(t, got, `|"1"|`, "the table retains affinity counts; the diagram stays in GitHub's portable flowchart subset")
	assert.Contains(t, got, `_["magus"]`)
	assert.NotContains(t, got, `_["."]`)
}
