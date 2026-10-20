package rankeval

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// trecRunFixture is in trec_eval's run format, with the rank column out of score
// order, which trec_eval ignores, and scores in exponent and integer forms.
const trecRunFixture = `q1 Q0 d3 1 0.5 sys
q1 Q0 d1 2 2.25 sys
q1 Q0 d2 3 1e-3 sys

q2	Q0	d9	1	7	sys
`

func TestReadRunOrdersByScoreAndRoundTrips(t *testing.T) {
	run, err := ReadRun(strings.NewReader(trecRunFixture))
	require.NoError(t, err)
	assert.Equal(t, TRECRun{Tag: "sys", Queries: map[string][]Hit{
		"q1": {{"d1", 2.25}, {"d3", 0.5}, {"d2", 0.001}},
		"q2": {{"d9", 7}},
	}}, run)

	var out bytes.Buffer
	require.NoError(t, WriteRun(&out, run))
	assert.Equal(t, `q1 Q0 d1 1 2.25 sys
q1 Q0 d3 2 0.5 sys
q1 Q0 d2 3 0.001 sys
q2 Q0 d9 1 7 sys
`, out.String())
	again, err := ReadRun(&out)
	require.NoError(t, err)
	assert.Equal(t, run, again)

	// d1, d3, d2 against gold {d3:1, d2:2}.
	ranked := []string{}
	for _, h := range run.Queries["q1"] {
		ranked = append(ranked, h.Doc)
	}
	want := (1/math.Log2(3) + 3/math.Log2(4)) / (3 + 1/math.Log2(3))
	assert.InDelta(t, want, NDCGAt(ranked, map[string]int{"d3": 1, "d2": 2}, 10), 1e-12)
}

func TestReadRunKeepsScoresExact(t *testing.T) {
	scores := []float64{0.1, 1.0 / 3, math.Nextafter(1, 2), -2.5e-300, 123456789.123456789}
	run := TRECRun{Tag: "exact", Queries: map[string][]Hit{}}
	for i, s := range scores {
		run.Queries["q"] = append(run.Queries["q"], Hit{Doc: string(rune('a' + i)), Score: s})
	}
	var out bytes.Buffer
	require.NoError(t, WriteRun(&out, run))
	back, err := ReadRun(&out)
	require.NoError(t, err)
	assert.Equal(t, Rank(slices.Clone(run.Queries["q"]), len(scores)), back.Queries["q"])
}

func TestReadRunRefusesMalformedLines(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"five fields":   {"q1 Q0 d1 1 0.5\n", "run line 1: 5 fields"},
		"bad rank":      {"q1 Q0 d1 one 0.5 s\n", `run line 1: rank "one"`},
		"bad score":     {"q1 Q0 d1 1 high s\n", `run line 1: score "high"`},
		"nan score":     {"q1 Q0 d1 1 NaN s\n", `run line 1: score "NaN"`},
		"second tag":    {"q1 Q0 d1 1 0.5 a\nq1 Q0 d2 2 0.4 b\n", `run line 2: tag "b" in a run tagged "a"`},
		"repeated hit":  {"q1 Q0 d1 1 0.5 a\nq1 Q0 d1 2 0.4 a\n", "run line 2: query q1 ranks d1 twice"},
		"negative rank": {"q1 Q0 d1 -1 0.5 a\n", `run line 1: rank "-1"`},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ReadRun(strings.NewReader(tc.in))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestWriteRunRefusesWhiteSpaceInIDs(t *testing.T) {
	assert.ErrorContains(t, WriteRun(&bytes.Buffer{}, TRECRun{}), "run tag")
	assert.ErrorContains(t, WriteRun(&bytes.Buffer{}, TRECRun{Tag: "s", Queries: map[string][]Hit{"a b": {{"d", 1}}}}), "query id")
	assert.ErrorContains(t, WriteRun(&bytes.Buffer{}, TRECRun{Tag: "s", Queries: map[string][]Hit{"q": {{"d 1", 1}}}}), "document id")
}

func TestRankBreaksTiesByHashWhateverTheInputOrder(t *testing.T) {
	tied := []Hit{{"dir:a", 1}, {"file:a", 1}, {"symbol:a", 1}, {"file:b", 1}, {"top", 2}}
	want := Rank(slices.Clone(tied), len(tied))
	assert.Equal(t, "top", want[0].Doc, "a higher score always leads")
	for i := 1; i+1 < len(want); i++ {
		assert.Less(t, tieHash(want[i].Doc), tieHash(want[i+1].Doc))
	}
	reversed := slices.Clone(tied)
	slices.Reverse(reversed)
	assert.Equal(t, want, Rank(reversed, len(tied)))
	assert.Equal(t, want[:2], Rank(slices.Clone(tied), 2))
}

func TestTopMatchesRankWithTies(t *testing.T) {
	var hits []Hit
	for i := range 500 {
		hits = append(hits, Hit{Doc: fmt.Sprintf("d%03d", i), Score: float64(i % 7)})
	}
	want := Rank(slices.Clone(hits), 40)
	assert.Equal(t, want, Top(slices.Values(hits), 40))
	assert.Equal(t, Rank(slices.Clone(hits), 500), Top(slices.Values(hits), 1000), "k past the end")
	assert.Empty(t, Top(slices.Values(hits), 0))
}

func TestWilsonInterval(t *testing.T) {
	// statsmodels proportion_confint(..., method="wilson") at alpha 0.05.
	lo, hi := WilsonInterval(50, 100)
	assert.InDelta(t, 0.40383153, lo, 1e-6)
	assert.InDelta(t, 0.59616847, hi, 1e-6)
	lo, hi = WilsonInterval(0, 10)
	assert.Zero(t, lo)
	assert.InDelta(t, 0.27753279, hi, 1e-6)
	assert.Panics(t, func() { WilsonInterval(3, 2) })
}

func TestMeanInterval(t *testing.T) {
	// mean 2.5, sd sqrt(5/3), t(0.975, 3) = 3.182446305284263.
	mean, lo, hi := MeanInterval([]float64{1, 2, 3, 4})
	half := 3.182446305284263 * math.Sqrt(5.0/3) / 2
	assert.InDelta(t, 2.5, mean, 1e-12)
	assert.InDelta(t, 2.5-half, lo, 1e-9)
	assert.InDelta(t, 2.5+half, hi, 1e-9)
}
