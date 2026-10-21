package rankeval

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	validQrel  = `{"query_id":"q1","file":"internal/textrank/bm25f.go","grade":2,"guideline":"v1","judge":"m","rationale":"defines BM25F"}`
	validQuery = `{"query_id":"q1","query":"where bm25f scores a field","source":"commit","eval_set":"judged-dev","shape":"prose","date":"2026-10-01"}`
)

func TestReadQrels(t *testing.T) {
	text := validQrel + "\n\n" + `{"query_id":"q1","file":"a.go","grade":0,"guideline":"v1","judge":"m","batch":"probe-1"}` + "\n"
	got, err := ReadQrels(strings.NewReader(text))
	require.NoError(t, err)
	assert.Equal(t, []Qrel{
		{QueryID: "q1", File: "internal/textrank/bm25f.go", Grade: 2, Guideline: "v1", Judge: "m", Rationale: "defines BM25F"},
		{QueryID: "q1", File: "a.go", Grade: 0, Guideline: "v1", Judge: "m", Batch: "probe-1"},
	}, got)

	line, err := marshalJSON(got[1])
	require.NoError(t, err)
	assert.JSONEq(t, `{"query_id":"q1","file":"a.go","grade":0,"guideline":"v1","judge":"m","batch":"probe-1"}`, string(line))
}

func TestReadQrelsRejects(t *testing.T) {
	cases := map[string]struct {
		line string
		want string
	}{
		"unknown field":   {strings.Replace(validQrel, `"judge"`, `"system":"bm25","judge"`, 1), "line 2"},
		"repeated field":  {strings.Replace(validQrel, `"grade":2`, `"grade":2,"grade":0`, 1), "line 2"},
		"grade 3":         {strings.Replace(validQrel, `"grade":2`, `"grade":3`, 1), "line 2: qrel q1 internal/textrank/bm25f.go: grade 3, want 0-2"},
		"negative grade":  {strings.Replace(validQrel, `"grade":2`, `"grade":-1`, 1), "grade -1, want 0-2"},
		"missing grade":   {strings.Replace(validQrel, `"grade":2,`, ``, 1), "line 2: qrel q1 internal/textrank/bm25f.go: grade -1, want 0-2"},
		"string grade":    {strings.Replace(validQrel, `"grade":2`, `"grade":"2"`, 1), "line 2"},
		"empty file":      {strings.Replace(validQrel, `"internal/textrank/bm25f.go"`, `""`, 1), "line 2: qrel q1: empty file"},
		"empty guideline": {strings.Replace(validQrel, `"guideline":"v1"`, `"guideline":""`, 1), "empty guideline"},
		"no judge":        {strings.Replace(validQrel, `,"judge":"m"`, ``, 1), "empty judge"},
		"no query id":     {strings.Replace(validQrel, `"query_id":"q1",`, ``, 1), "line 2: empty query_id"},
		"not json":        {"[", "line 2"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadQrels(strings.NewReader(validQrel + "\n" + tc.line + "\n"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "qrels line 2")
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestReadQrelsQueries(t *testing.T) {
	got, err := ReadQueries(strings.NewReader(validQuery + "\n"))
	require.NoError(t, err)
	assert.Equal(t, []Query{{ID: "q1", Query: "where bm25f scores a field", Source: "commit", Set: SetJudgedDev, Shape: ShapeProse, Date: "2026-10-01"}}, got)

	cases := map[string]struct {
		text string
		want string
	}{
		"unknown field": {strings.Replace(validQuery, `"date"`, `"verb":"query","date"`, 1), "queries line 2"},
		"guard set":     {strings.Replace(validQuery, `"judged-dev"`, `"definition-lookup"`, 1), `queries line 2: query q1: eval set "definition-lookup"`},
		"bad date":      {strings.Replace(validQuery, `"2026-10-01"`, `"yesterday"`, 1), `queries line 2: query q1: date "yesterday"`},
		"no source":     {strings.Replace(validQuery, `"commit"`, `""`, 1), "queries line 2: query q1: empty source"},
		"twice":         {validQuery, "queries: query_id q1 appears twice"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadQueries(strings.NewReader(validQuery + "\n" + tc.text + "\n"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestReadQrelsJudgedGold(t *testing.T) {
	qrels := []Qrel{
		{QueryID: "q1", File: "a.go", Grade: 1, Guideline: "v1", Judge: "m"},
		{QueryID: "q1", File: "a.go", Grade: 2, Guideline: "v1", Judge: "m"},
		{QueryID: "q1", File: "b.go", Grade: 0, Guideline: "v1", Judge: "m"},
		{QueryID: "q1", File: "b.go", Grade: 2, Guideline: "v1", Judge: "m", Batch: "rejudge-1"},
		{QueryID: "q2", File: "c.go", Grade: 1, Guideline: "v1", Judge: "m"},
	}
	gold, err := JudgedGold(qrels)
	require.NoError(t, err)
	assert.Equal(t, map[string]map[string]int{
		"q1": {"file:a.go": 2, "file:b.go": 0},
		"q2": {"file:c.go": 1},
	}, gold, "the last row wins and a batch row never scores")

	_, err = JudgedGold(append(qrels, Qrel{QueryID: "q2", File: "c.go", Grade: 2, Guideline: "v2", Judge: "m"}))
	require.ErrorContains(t, err, "q2 c.go is graded under 2 guideline/judge pairs")

	queries := []Query{
		{ID: "q1", Query: "x", Source: "s", Set: SetJudgedDev, Shape: ShapeProse, Date: "2026-10-01"},
		{ID: "q3", Query: "y", Source: "s", Set: SetJudgedDev, Shape: ShapePath, Date: "2026-10-01"},
		{ID: "q2", Query: "z", Source: "s", Set: SetJudgedHoldout, Shape: ShapeProse, Date: "2026-10-01"},
	}
	js := JudgedJudgments(queries, gold, SetJudgedDev)
	require.Len(t, js, 2)
	assert.Equal(t, Judgment{ID: "q1", Query: "x", Verb: "query", Date: "2026-10-01", Source: "s", Set: SetJudgedDev, Shape: ShapeProse, Gold: gold["q1"]}, js[0])
	assert.Empty(t, js[1].Gold, "an unjudged query stays, to count as unscorable")
}

func TestReadQrelsDigestQueries(t *testing.T) {
	a := Query{ID: "a", Query: "x", Source: "s", Set: SetJudgedHoldout, Shape: ShapeProse, Date: "2026-10-01"}
	b := Query{ID: "b", Query: "y", Source: "s", Set: SetJudgedHoldout, Shape: ShapeProse, Date: "2026-10-01"}
	dev := Query{ID: "c", Query: "z", Source: "s", Set: SetJudgedDev, Shape: ShapeProse, Date: "2026-10-01"}

	d1, err := DigestQueries([]Query{a, b, dev}, SetJudgedHoldout)
	require.NoError(t, err)
	d2, err := DigestQueries([]Query{b, a}, SetJudgedHoldout)
	require.NoError(t, err)
	assert.Equal(t, d1, d2, "order and other sets do not move the digest")
	assert.Len(t, d1, 64)

	b.Query = "y2"
	d3, err := DigestQueries([]Query{a, b}, SetJudgedHoldout)
	require.NoError(t, err)
	assert.NotEqual(t, d1, d3, "a changed query text moves it")
}
