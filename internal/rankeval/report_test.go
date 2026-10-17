package rankeval

import (
	"math"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

// fileOfFixture maps a symbol to the file it is defined in, a file to itself and a
// directory to no file, as the eval's graph lookup does.
func fileOfFixture(id string) string {
	switch {
	case strings.HasPrefix(id, "file:"):
		return id
	case strings.HasPrefix(id, "symbol:"):
		file, _, _ := strings.Cut(strings.TrimPrefix(id, "symbol:"), "#")
		return "file:" + file
	}
	return ""
}

func TestFileCutMixedIDs(t *testing.T) {
	ranked := []string{
		"symbol:a.go#A", "dir:.", "file:a.go", "symbol:b.go#B", "dir:pkg",
		"symbol:a.go#C", "file:c.go", "target:pkg:build", "symbol:b.go#D", "file:d.go",
	}
	assert.Equal(t, []string{"file:a.go", "file:b.go", "file:c.go", "file:d.go"}, FileCut(ranked, fileOfFixture, cutoff),
		"each hit becomes its file at its first position; dirs and targets take none")
	assert.Equal(t, []string{"file:a.go", "file:b.go"}, FileCut(ranked, fileOfFixture, 2))
	assert.Empty(t, FileCut(nil, fileOfFixture, cutoff))
}

func TestFileCutStopsAtTenFiles(t *testing.T) {
	var ranked []string
	for i := range 30 {
		ranked = append(ranked, "symbol:f"+strconv.Itoa(i)+".go#S", "dir:d"+strconv.Itoa(i), "file:f"+strconv.Itoa(i)+".go")
	}
	files := FileCut(ranked, fileOfFixture, cutoff)
	require.Len(t, files, cutoff)
	assert.Equal(t, "file:f9.go", files[9], "ten files, not ten nodes: 28 nodes reach the tenth")

	run := Run{Judgment: Judgment{ID: "q", Set: SetJudgedDev, Gold: map[string]int{"file:f10.go": 2}}, Files: FileCut(ranked, fileOfFixture, FileDepth)}
	rep := Summarize(SetJudgedDev, []Run{run})
	assert.Zero(t, rep.Overall.NDCG10, "the eleventh file is outside the cut")
	assert.Zero(t, rep.Overall.Recall10)
}

func run(id string, set EvalSet, shape QueryShape, gold map[string]int, files ...string) Run {
	return Run{
		Judgment: Judgment{ID: id, Query: id, Verb: "query", Set: set, Shape: shape, Gold: gold},
		Files:    files,
	}
}

func sampleRuns() []Run {
	r1 := run("r1", SetJudgedDev, ShapeIdentifier, map[string]int{"file:a": 2}, "file:a")
	r1.Bytes, r1.Latency = 100, 10*time.Millisecond
	r2 := run("r2", SetJudgedDev, ShapeProse, map[string]int{"file:b": 1, "file:x": 0}, "file:x", "file:u", "file:b")
	r2.Bytes, r2.Latency = 200, 30*time.Millisecond
	r3 := run("r3", SetJudgedDev, ShapeProse, map[string]int{"file:c": 2})
	r3.Latency, r3.Unknown, r3.Dropped = 20*time.Millisecond, true, 1
	r4 := run("r4", SetJudgedDev, ShapePath, map[string]int{"file:z": 0}, "file:y")
	r4.Latency, r4.Dropped = 5*time.Millisecond, 2
	return []Run{r1, r2, r3, r4}
}

func TestSummarizeOverall(t *testing.T) {
	rep := Summarize(SetJudgedDev, sampleRuns())
	third := 1.0 / 3
	// TestSummarizeSlices covers the per-shape slices.
	rep.Shapes = nil
	// r2 ranks b third behind an unjudged u; condensed, u goes and b is second. r4 has
	// nothing relevant and no score, and only agent-agreement has a node-level report.
	assert.Equal(t, SetReport{
		Set: SetJudgedDev,
		Overall: Metrics{
			Queries:         3,
			Unscorable:      1,
			DroppedGold:     3,
			NDCG10:          (1 + 1/math.Log2(4)) / 3,
			CondensedNDCG10: (1 + 1/math.Log2(3)) / 3,
			Judged10:        (1 + 2*third) / 2,
			Recall10:        2 * third,
			RR10:            (1 + third) / 3,
			ZeroResult:      third,
			Unknown:         third,
			LatencyP50:      20 * time.Millisecond,
			LatencyP95:      30 * time.Millisecond,
			MeanBytes:       100,
		},
		Scores: []QueryScore{
			{ID: "r1", NDCG10: 1, CondensedNDCG10: 1},
			{ID: "r2", NDCG10: 1 / math.Log2(4), CondensedNDCG10: 1 / math.Log2(3)},
			{ID: "r3"},
		},
	}, rep)
}

func TestSummarizeSlices(t *testing.T) {
	rep := Summarize(SetJudgedDev, sampleRuns())
	require.Len(t, rep.Shapes, 3)
	assert.Equal(t, Metrics{Unscorable: 1, DroppedGold: 2}, rep.Shapes[ShapePath], "a run with nothing relevant scores nothing")
	prose := rep.Shapes[ShapeProse]
	assert.Equal(t, 2, prose.Queries)
	assert.InDelta(t, 1.0/6, prose.RR10, 1e-12)
	assert.Nil(t, rep.Verbs, "one verb is no slice")
}

func TestSummarizeAgentAgreementIsBinaryWithNodes(t *testing.T) {
	r := run("h", SetAgentAgreement, ShapeIdentifier, map[string]int{"file:a": 3, "file:b": 1, "dir:pkg": 1}, "file:b", "file:a")
	r.Judgment.Session = "s1"
	r.Ranked = []string{"symbol:b#B", "file:b", "dir:pkg", "symbol:a#A", "file:a"}
	rep := Summarize(SetAgentAgreement, []Run{r})
	assert.InDelta(t, 1.0, rep.Overall.NDCG10, 1e-12, "a written file gains no more than a read one")
	assert.Equal(t, []QueryScore{{ID: "h", Cluster: "s1", NDCG10: rep.Overall.NDCG10, CondensedNDCG10: 1}}, rep.Scores)
	require.NotNil(t, rep.Nodes)
	assert.Equal(t, 1, rep.Nodes.Queries)
	assert.InDelta(t, 1.0, rep.Nodes.Recall10, 1e-12)
	assert.InDelta(t, 0.5, rep.Nodes.MRR, 1e-12)
}

func TestSummarizeEmpty(t *testing.T) {
	rep := Summarize(SetJudgedDev, nil)
	assert.Equal(t, SetReport{Set: SetJudgedDev}, rep)
}

func TestSummarizeText(t *testing.T) {
	text := Report{Sets: []SetReport{
		Summarize(SetJudgedDev, sampleRuns()),
		{Set: SetJudgedHoldout, Absent: "sealed"},
		{Set: SetAgentAgreement, Absent: "not built"},
	}}.Text()
	var first []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		first = append(first, strings.Fields(line)[0])
	}
	assert.Equal(t, []string{"==", "slice", "all", "shape=identifier", "shape=path", "shape=prose", "==", "=="}, first)
	assert.Contains(t, text, "== judged-holdout: sealed\n")
	assert.Contains(t, text, "== agent-agreement (report-only): not built\n")
}

func TestSummarizeReportRoundTripsJSON(t *testing.T) {
	rep := Report{Sets: []SetReport{Summarize(SetJudgedDev, sampleRuns()), {Set: SetFigures, Absent: "not built"}}}
	b, err := json.Marshal(rep)
	require.NoError(t, err)
	var back Report
	require.NoError(t, json.UnmarshalStrict(b, &back))
	assert.Equal(t, rep, back)
}

func scores(cluster func(i int) string, values ...float64) []QueryScore {
	out := make([]QueryScore, len(values))
	for i, v := range values {
		out[i] = QueryScore{ID: "q" + strconv.Itoa(i), Cluster: cluster(i), NDCG10: v, CondensedNDCG10: v}
	}
	return out
}

func TestSummarizeCompare(t *testing.T) {
	none := func(int) string { return "" }
	base := Report{Sets: []SetReport{
		{Set: SetJudgedDev, Scores: scores(none, 0.2, 0.4, 0.1, 0.5, 0.3, 0.2, 0.6, 0.1)},
		{Set: SetDefinitionLookup, Scores: scores(none, 1)},
		{Set: SetJudgedHoldout, Absent: "sealed"},
	}}
	now := Report{Sets: []SetReport{
		{Set: SetJudgedDev, Scores: scores(none, 0.5, 0.6, 0.3, 0.9, 0.6, 0.4, 0.9, 0.4)},
		{Set: SetDefinitionLookup, Scores: scores(none, 0)},
		{Set: SetJudgedHoldout, Absent: "sealed"},
		{Set: SetFigures, Scores: scores(none, 1, 1)},
	}}
	cs := Compare(base, now)
	require.Len(t, cs, 4, "two metrics for each set both reports scored")

	dev := cs[0]
	// The floats are bounded below; the whole value pins every other field.
	assert.Equal(t, Comparison{
		Set: SetJudgedDev, Metric: "ndcg@10", Pairs: 8,
		Base: dev.Base, Now: dev.Now, Delta: dev.Delta, Lo: dev.Lo, Hi: dev.Hi, Effect: dev.Effect, P: dev.P, PHolm: dev.PHolm,
	}, dev)
	assert.InDelta(t, 0.275, dev.Delta, 1e-12)
	assert.InDelta(t, dev.Now-dev.Base, dev.Delta, 1e-12)
	// Every difference is positive: of the 2^8 sign patterns only the observed one and
	// its negation reach |sum|, so p is near 2/256.
	assert.InDelta(t, 2.0/256, dev.P, 0.002)
	assert.Less(t, dev.Lo, dev.Delta)
	assert.Greater(t, dev.Hi, dev.Delta)
	assert.Greater(t, dev.Effect, 0.0)
	assert.Equal(t, min(1, 2*dev.P), cs[1].PHolm, "Holm over the two tested comparisons; tied p keeps the larger")

	def := cs[2]
	assert.Equal(t, "1 paired unit(s); a test needs 2", def.Skipped)
	assert.Zero(t, def.PHolm)
}

func TestSummarizeCompareClustersBySession(t *testing.T) {
	session := func(i int) string { return "s" + strconv.Itoa(i/3) }
	base := Report{Sets: []SetReport{{Set: SetAgentAgreement, Scores: scores(session, 0, 0, 0, 0, 0, 0)}}}
	now := Report{Sets: []SetReport{{Set: SetAgentAgreement, Scores: scores(session, 1, 1, 1, 1, 1, 0.4)}}}
	c := Compare(base, now)[0]
	assert.Equal(t, Comparison{
		Set: SetAgentAgreement, Metric: "ndcg@10", Pairs: 6, Sessions: 2,
		Base: c.Base, Now: c.Now, Delta: c.Delta, Lo: c.Lo, Hi: c.Hi, Effect: c.Effect, P: c.P, PHolm: c.PHolm,
	}, c)
	// Two sessions give four equally likely sign patterns, two of which reach |sum|.
	assert.InDelta(t, 0.5, c.P, 0.01)
	assert.InDelta(t, 0.9, c.Delta, 1e-12)

	one := Report{Sets: []SetReport{{Set: SetAgentAgreement, Scores: scores(func(int) string { return "s" }, 1, 1, 1)}}}
	assert.Equal(t, "1 paired unit(s); a test needs 2", Compare(one, one)[0].Skipped)
}

func TestSummarizeComparisonText(t *testing.T) {
	text := ComparisonText([]Comparison{
		{Set: SetJudgedDev, Metric: "ndcg@10", Pairs: 8, Base: 0.3, Now: 0.5, Delta: 0.2, Lo: 0.1, Hi: 0.3, Effect: math.NaN(), P: 0.01, PHolm: 0.02},
		{Set: SetDefinitionLookup, Metric: "ndcg@10", Pairs: 1, Skipped: "1 paired unit(s); a test needs 2"},
	})
	assert.Contains(t, text, "+0.200")
	assert.Contains(t, text, "[+0.100, +0.300]")
	assert.Contains(t, text, "n/a")
	assert.Contains(t, text, "not tested: 1 paired unit(s)")
}
