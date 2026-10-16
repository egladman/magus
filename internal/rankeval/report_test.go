package rankeval

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

func run(id string, shape QueryShape, split string, gold map[string]int, ranked ...string) Run {
	return Run{
		Judgment: Judgment{ID: id, Query: id, Verb: "query", Split: split, Shape: shape, Gold: gold},
		Ranked:   ranked,
	}
}

func sampleRuns() []Run {
	r1 := run("r1", ShapeIdentifier, "dev", map[string]int{"a": 3}, "a")
	r1.Bytes, r1.Latency = 100, 10*time.Millisecond
	r2 := run("r2", ShapeProse, "dev", map[string]int{"b": 1}, "x", "b")
	r2.Bytes, r2.Latency = 200, 30*time.Millisecond
	r3 := run("r3", ShapeProse, "holdout", map[string]int{"c": 2})
	r3.Judgment.Verb = "refs"
	r3.Latency, r3.Unknown, r3.Dropped = 20*time.Millisecond, true, 1
	r4 := run("r4", ShapePath, "holdout", map[string]int{}, "y")
	r4.Latency, r4.Dropped = 5*time.Millisecond, 2
	return []Run{r1, r2, r3, r4}
}

func TestSummarizeOverall(t *testing.T) {
	rep := Summarize(sampleRuns())
	third := 1.0 / 3
	assert.Equal(t, Metrics{
		Queries:     3,
		Unscorable:  1,
		DroppedGold: 3,
		Recall5:     2 * third,
		Recall10:    2 * third,
		Recall50:    2 * third,
		MRR:         0.5,
		NDCG10:      (1 + 1/math.Log2(3)) / 3,
		ZeroResult:  third,
		Unknown:     third,
		LatencyP50:  20 * time.Millisecond,
		LatencyP95:  30 * time.Millisecond,
		MeanBytes:   100,
	}, rep.Overall)
}

func TestSummarizeSlices(t *testing.T) {
	rep := Summarize(sampleRuns())
	require.Len(t, rep.Shapes, 3)
	require.Len(t, rep.Splits, 2)

	assert.Equal(t, Metrics{Unscorable: 1, DroppedGold: 2}, rep.Shapes[ShapePath], "an all-dropped run scores nothing")
	prose := rep.Shapes[ShapeProse]
	assert.InDelta(t, 0.25, prose.MRR, 1e-12)
	assert.Equal(t, Metrics{
		Queries:     2,
		DroppedGold: 1,
		Recall5:     0.5,
		Recall10:    0.5,
		Recall50:    0.5,
		MRR:         prose.MRR,
		NDCG10:      0.3154648767857287,
		ZeroResult:  0.5,
		Unknown:     0.5,
		LatencyP50:  20 * time.Millisecond,
		LatencyP95:  30 * time.Millisecond,
		MeanBytes:   100,
	}, prose)

	dev, holdout := rep.Splits["dev"], rep.Splits["holdout"]
	assert.Equal(t, 2, dev.Queries)
	assert.InDelta(t, 0.75, dev.MRR, 1e-12)
	assert.Equal(t, Metrics{
		Queries:     1,
		Unscorable:  1,
		DroppedGold: 3,
		ZeroResult:  1,
		Unknown:     1,
		LatencyP50:  20 * time.Millisecond,
		LatencyP95:  20 * time.Millisecond,
	}, holdout)

	require.Len(t, rep.Verbs, 2)
	assert.Equal(t, Metrics{
		Queries:     2,
		Unscorable:  1,
		DroppedGold: 2,
		Recall5:     1,
		Recall10:    1,
		Recall50:    1,
		MRR:         0.75,
		NDCG10:      0.8154648767857287,
		LatencyP50:  10 * time.Millisecond,
		LatencyP95:  30 * time.Millisecond,
		MeanBytes:   150,
	}, rep.Verbs["query"])
	assert.Equal(t, 1, rep.Verbs["refs"].Queries)
	require.Len(t, rep.VerbSplits["query"], 2)
	assert.Equal(t, dev, rep.VerbSplits["query"]["dev"], "every dev run is a query")
	assert.Equal(t, Metrics{Unscorable: 1, DroppedGold: 2}, rep.VerbSplits["query"]["holdout"])
	assert.Equal(t, holdout.MRR, rep.VerbSplits["refs"]["holdout"].MRR)
	assert.NotContains(t, rep.VerbSplits["refs"], "dev")
}

func TestSummarizeEmpty(t *testing.T) {
	rep := Summarize(nil)
	assert.Equal(t, Metrics{}, rep.Overall)
	assert.Empty(t, rep.Shapes)
	assert.Empty(t, rep.Splits)
	assert.Empty(t, rep.Verbs)
	assert.Empty(t, rep.VerbSplits)
}

func TestSummarizeTextOrdersSlices(t *testing.T) {
	text := Summarize(sampleRuns()).Text()
	var first []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		first = append(first, strings.Fields(line)[0])
	}
	assert.Equal(t, []string{
		"slice", "all", "shape=identifier", "shape=path", "shape=prose", "split=dev", "split=holdout",
		"verb=query", "verb=refs", "verb=query,split=dev", "verb=query,split=holdout", "verb=refs,split=holdout",
	}, first)
	assert.Contains(t, text, "0.500", "overall MRR")
}

func TestSummarizeReportRoundTripsJSON(t *testing.T) {
	rep := Summarize(sampleRuns())
	b, err := json.Marshal(rep)
	require.NoError(t, err)
	var back Report
	require.NoError(t, json.UnmarshalStrict(b, &back))
	assert.Equal(t, rep, back)
	assert.Empty(t, Compare(rep, back))
}

func TestCompareListsChangedMetrics(t *testing.T) {
	base := Summarize(sampleRuns())
	improved := sampleRuns()
	improved[1].Ranked = []string{"b"}
	now := Summarize(improved)

	ndcgBase := (1 + 1/math.Log2(3)) / 3
	assert.Equal(t, []Delta{
		{Metric: "mrr", Slice: "all", Base: 0.5, Now: 2.0 / 3},
		{Metric: "ndcg@10", Slice: "all", Base: ndcgBase, Now: 2.0 / 3},
		{Metric: "mrr", Slice: "shape=prose", Base: 0.25, Now: 0.5},
		{Metric: "ndcg@10", Slice: "shape=prose", Base: 1 / math.Log2(3) / 2, Now: 0.5},
		{Metric: "mrr", Slice: "split=dev", Base: 0.75, Now: 1},
		{Metric: "ndcg@10", Slice: "split=dev", Base: (1 + 1/math.Log2(3)) / 2, Now: 1},
		{Metric: "mrr", Slice: "verb=query", Base: 0.75, Now: 1},
		{Metric: "ndcg@10", Slice: "verb=query", Base: (1 + 1/math.Log2(3)) / 2, Now: 1},
		{Metric: "mrr", Slice: "verb=query,split=dev", Base: 0.75, Now: 1},
		{Metric: "ndcg@10", Slice: "verb=query,split=dev", Base: (1 + 1/math.Log2(3)) / 2, Now: 1},
	}, Compare(base, now))
}

func TestCompareSliceInOneReport(t *testing.T) {
	now := Summarize(sampleRuns()[:1])
	deltas := Compare(Report{}, now)
	assert.Contains(t, deltas, Delta{Metric: "queries", Slice: "shape=identifier", Base: 0, Now: 1})
	assert.Contains(t, deltas, Delta{Metric: "queries", Slice: "split=dev", Base: 0, Now: 1})
	assert.Contains(t, deltas, Delta{Metric: "queries", Slice: "all", Base: 0, Now: 1})
	assert.Contains(t, deltas, Delta{Metric: "queries", Slice: "verb=query,split=dev", Base: 0, Now: 1})
}
