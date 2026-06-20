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

func TestWriteHotspotMermaid(t *testing.T) {
	out := types.HotspotOutput{
		Commits: 100,
		Nodes: []types.Node{
			{Path: "api", Churn: 10, Authors: 3, BlastRadius: 2, Children: []string{"core"}},
			{Path: "core", Churn: 1},
		},
	}

	var b strings.Builder
	require.NoError(t, WriteHotspotMermaid(&b, out))
	s := b.String()

	assert.Contains(t, s, "graph")
	assert.Contains(t, s, "commits=10")
	assert.Contains(t, s, "authors=3")
	assert.Contains(t, s, "BR=2")
	assert.Contains(t, s, "classDef heat4")
	assert.Contains(t, s, "api --> core", "dependency edge is drawn")
}

func TestWriteAffinityMermaid(t *testing.T) {
	out := types.AffinityOutput{
		Pairs: []types.CoChange{
			{A: "api", B: "web/studio", Count: 4, Hidden: true},
		},
	}

	var b strings.Builder
	require.NoError(t, WriteAffinityMermaid(&b, out))
	s := b.String()

	assert.Contains(t, s, "graph")
	assert.Contains(t, s, `|"4"|`, "edge is labelled with the co-change count")
	assert.Contains(t, s, "-.->", "hidden affinity is drawn dashed")
	assert.Contains(t, s, "web_studio")
}

func TestWriteHotspotQuadrant(t *testing.T) {
	out := types.HotspotOutput{
		Files: []types.FileHotspot{
			{Path: "api/hot.go", Commits: 20, Complexity: 100, Score: 2000},
			{Path: "api/cool.go", Commits: 2, Complexity: 10, Score: 20},
		},
	}

	var b strings.Builder
	require.NoError(t, WriteHotspotQuadrant(&b, out))
	s := b.String()

	assert.Contains(t, s, "quadrantChart")
	assert.Contains(t, s, "Refactor now")
	assert.Contains(t, s, `"api/hot.go": [1.000, 1.000]`, "busiest+most-complex file maps to the top-right corner")
}

func TestWriteInsightMarkdown(t *testing.T) {
	r := types.InsightReport{
		Hotspots: types.HotspotOutput{
			Commits: 50,
			Nodes:   []types.Node{{Path: "api", Churn: 3}},
			Files:   []types.FileHotspot{{Path: "api/a.go", Commits: 3, Complexity: 40, Score: 120, Authors: 1}},
		},
		Affinity:  types.AffinityOutput{Pairs: []types.CoChange{{A: "api", B: "web", Count: 2, Hidden: true}}},
		Ownership: types.OwnershipOutput{Projects: []types.Ownership{{Path: "api", PrimaryShare: 100, Authors: 1, Primary: "ada", BusFactor1: true}}},
		Trend:     types.TrendOutput{Projects: []types.Trend{{Path: "api", Recent: 2, Earlier: 1, Delta: 1}}},
	}

	var b strings.Builder
	require.NoError(t, WriteInsightMarkdown(&b, r))
	s := b.String()

	assert.Contains(t, s, "# Insight")
	assert.Contains(t, s, "## Hotspots")
	assert.Contains(t, s, "## Affinity")
	assert.Contains(t, s, "## Ownership")
	assert.Contains(t, s, "quadrantChart", "embeds the churn-vs-complexity quadrant")
	assert.Contains(t, s, "## Trend")
	assert.Contains(t, s, "```mermaid", "embeds the graphs")
	assert.Contains(t, s, "`api/a.go`", "lists the hottest file")
}
