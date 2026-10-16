package rankeval

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadJudgmentsFixture(t *testing.T) {
	f, err := os.Open("testdata/judgments.jsonl")
	require.NoError(t, err)
	defer f.Close()

	got, err := ReadJudgments(f)
	require.NoError(t, err)
	require.Len(t, got, 8)
	assert.Equal(t, Judgment{
		ID:     "j1",
		Query:  "ParseManifest",
		Verb:   "refs",
		Date:   "2026-09-01",
		Source: "fixture",
		Split:  "dev",
		Shape:  ShapeIdentifier,
		Gold:   map[string]int{"file:pkg/manifest/parse.go": 3, "dir:pkg/manifest": 1},
	}, got[0])
	ids := make([]string, len(got))
	for i, j := range got {
		ids[i] = j.ID
	}
	assert.Equal(t, []string{"j1", "j2", "j3", "j4", "j5", "j6", "j7", "j8"}, ids)
}

func TestReadJudgmentsRejects(t *testing.T) {
	const valid = `{"id":"a","query":"q","verb":"query","date":"2026-01-01","source":"s","split":"dev","shape":"prose","gold":{"file:x":1}}`
	cases := map[string]struct {
		line string
		want string
	}{
		"unknown field": {strings.Replace(valid, `"source"`, `"extra":1,"source"`, 1), "line 2"},
		"empty gold":    {strings.Replace(valid, `{"file:x":1}`, `{}`, 1), "line 2: judgment a: empty gold"},
		"missing gold":  {strings.Replace(valid, `,"gold":{"file:x":1}`, ``, 1), "line 2: judgment a: empty gold"},
		"unknown verb":  {strings.Replace(valid, `"query","date"`, `"grep","date"`, 1), `line 2: judgment a: verb "grep"`},
		"unknown shape": {strings.Replace(valid, `"prose"`, `"regex"`, 1), `line 2: judgment a: shape "regex"`},
		"unknown split": {strings.Replace(valid, `"dev"`, `"test"`, 1), `line 2: judgment a: split "test"`},
		"grade zero":    {strings.Replace(valid, `"file:x":1`, `"file:x":0`, 1), "line 2: judgment a: gold file:x has grade 0"},
		"grade four":    {strings.Replace(valid, `"file:x":1`, `"file:x":4`, 1), "line 2: judgment a: gold file:x has grade 4"},
		"empty id":      {strings.Replace(valid, `"id":"a"`, `"id":""`, 1), "line 2: empty id"},
		"empty query":   {strings.Replace(valid, `"query":"q"`, `"query":""`, 1), "line 2: judgment a: empty query"},
		"not json":      {"{", "line 2"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadJudgments(strings.NewReader(valid + "\n" + tc.line + "\n"))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "judgments "+tc.want)
		})
	}
}

func TestReadJudgmentsEmpty(t *testing.T) {
	got, err := ReadJudgments(strings.NewReader("\n\n"))
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestJudgmentResolvedDropsUnknownGold(t *testing.T) {
	j := Judgment{ID: "j", Gold: map[string]int{"file:a.go": 3, "file:gone.go": 2, "dir:.": 1}}
	known := map[string]bool{"file:a.go": true, "dir:.": true}

	got, dropped := j.Resolved(func(id string) bool { return known[id] })
	assert.Equal(t, 1, dropped)
	assert.Equal(t, map[string]int{"file:a.go": 3, "dir:.": 1}, got.Gold)
	assert.Len(t, j.Gold, 3, "the receiver's gold is left as read")

	none, dropped := j.Resolved(func(string) bool { return false })
	assert.Equal(t, 3, dropped)
	assert.Empty(t, none.Gold)
}
