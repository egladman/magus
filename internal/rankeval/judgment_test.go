package rankeval

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

const validJudgment = `{"id":"a","query":"q","verb":"query","date":"2026-01-01","source":"transcript","session":"s1","eval_set":"agent-agreement","shape":"prose","gold":{"file:x":1}}`

func TestReadJudgments(t *testing.T) {
	text := validJudgment + "\n\n" +
		`{"id":"b","query":"ParseManifest","verb":"refs","date":"2026-09-01","source":"transcript","session":"s2","eval_set":"agent-agreement","shape":"identifier","gold":{"file:pkg/manifest/parse.go":3,"dir:pkg/manifest":1}}` + "\n"
	got, err := ReadJudgments(strings.NewReader(text))
	require.NoError(t, err)
	require.Len(t, got, 2)
	assert.Equal(t, Judgment{
		ID:      "b",
		Query:   "ParseManifest",
		Verb:    "refs",
		Date:    "2026-09-01",
		Source:  "transcript",
		Session: "s2",
		Set:     SetAgentAgreement,
		Shape:   ShapeIdentifier,
		Gold:    map[string]int{"file:pkg/manifest/parse.go": 3, "dir:pkg/manifest": 1},
	}, got[1])

	line, err := json.Marshal(got[1])
	require.NoError(t, err)
	back, err := ReadJudgments(strings.NewReader(string(line)))
	require.NoError(t, err)
	assert.Equal(t, got[1:], back, "a judgment round-trips")
}

func TestReadJudgmentsRejects(t *testing.T) {
	cases := map[string]struct {
		line string
		want string
	}{
		"unknown field":     {strings.Replace(validJudgment, `"source"`, `"extra":1,"source"`, 1), "line 2"},
		"split field":       {strings.Replace(validJudgment, `"source"`, `"split":"dev","source"`, 1), "line 2"},
		"repeated field":    {strings.Replace(validJudgment, `"verb":"query"`, `"verb":"query","verb":"refs"`, 1), "line 2"},
		"empty gold":        {strings.Replace(validJudgment, `{"file:x":1}`, `{}`, 1), "line 2: judgment a: empty gold"},
		"missing gold":      {strings.Replace(validJudgment, `,"gold":{"file:x":1}`, ``, 1), "line 2: judgment a: empty gold"},
		"unknown verb":      {strings.Replace(validJudgment, `"query","date"`, `"grep","date"`, 1), `line 2: judgment a: verb "grep"`},
		"unknown shape":     {strings.Replace(validJudgment, `"prose"`, `"regex"`, 1), `line 2: judgment a: shape "regex"`},
		"unknown set":       {strings.Replace(validJudgment, `"agent-agreement"`, `"holdout"`, 1), `line 2: judgment a: eval set "holdout"`},
		"bad date":          {strings.Replace(validJudgment, `"2026-01-01"`, `"2026-1-1"`, 1), `line 2: judgment a: date "2026-1-1"`},
		"no session":        {strings.Replace(validJudgment, `"session":"s1",`, ``, 1), "line 2: judgment a: an agent-agreement row names no session"},
		"shell variable":    {strings.Replace(validJudgment, `"query":"q"`, `"query":"$q"`, 1), "line 2: judgment a: query \"$q\" holds an unexpanded shell variable"},
		"dir dot gold":      {strings.Replace(validJudgment, `"file:x":1`, `"file:x":1,"dir:.":1`, 1), "line 2: judgment a: dir:. is gold"},
		"grade zero":        {strings.Replace(validJudgment, `"file:x":1`, `"file:x":0`, 1), "line 2: judgment a: gold file:x has grade 0, want 1-3"},
		"grade four":        {strings.Replace(validJudgment, `"file:x":1`, `"file:x":4`, 1), "line 2: judgment a: gold file:x has grade 4, want 1-3"},
		"judged grade 3":    {strings.Replace(strings.Replace(validJudgment, `"agent-agreement"`, `"definition-lookup"`, 1), `"file:x":1`, `"file:x":3`, 1), "line 2: judgment a: gold file:x has grade 3, want 0-2"},
		"empty id":          {strings.Replace(validJudgment, `"id":"a"`, `"id":""`, 1), "line 2: empty id"},
		"empty query":       {strings.Replace(validJudgment, `"query":"q"`, `"query":""`, 1), "line 2: judgment a: empty query"},
		"not json":          {"{", "line 2"},
		"string for object": {`"a"`, "line 2"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ReadJudgments(strings.NewReader(validJudgment + "\n" + tc.line + "\n"))
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

func TestReadJudgmentsMarshalRefusesInvalid(t *testing.T) {
	_, err := json.Marshal(Judgment{ID: "a"})
	require.ErrorContains(t, err, "judgment a: empty query")
}

func TestJudgmentResolvedDropsUnknownGold(t *testing.T) {
	j := Judgment{ID: "j", Gold: map[string]int{"file:a.go": 3, "file:gone.go": 2, "dir:a": 1}}
	known := map[string]bool{"file:a.go": true, "dir:a": true}

	got, dropped := j.Resolved(func(id string) bool { return known[id] })
	assert.Equal(t, 1, dropped)
	assert.Equal(t, map[string]int{"file:a.go": 3, "dir:a": 1}, got.Gold)
	assert.Len(t, j.Gold, 3, "the receiver's gold is left as read")

	none, dropped := j.Resolved(func(string) bool { return false })
	assert.Equal(t, 3, dropped)
	assert.Empty(t, none.Gold)
}
