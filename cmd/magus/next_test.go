package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/hint"
	json "github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var nextFixture = []hint.Next{
	{ID: "query-explain", Run: "magus explain spell:go", Why: "explain names a node's edges."},
	{ID: "query-path", Run: "magus path spell:go spell:gomod", Why: "path prints the chain."},
}

// The Run line is navigation and prints every time; the Why is advice and says nothing
// the second time, so it fires once per session and the command survives it.
func TestPrintNextFiresEachWhyOnce(t *testing.T) {
	gate := newAdvisoryGate(t.TempDir(), "session-1")

	var first, second bytes.Buffer
	printNext(&first, gate, nextFixture)
	printNext(&second, gate, nextFixture)

	assert.Equal(t, "\nnext:\n"+
		"  magus explain spell:go  (explain names a node's edges.)\n"+
		"  magus path spell:go spell:gomod  (path prints the chain.)\n", first.String())
	assert.Equal(t, "\nnext:\n"+
		"  magus explain spell:go\n"+
		"  magus path spell:go spell:gomod\n", second.String())
}

// A separate session meets every Why again: the suppression is per session, not a
// permanent silence.
func TestPrintNextRepeatsWhyInANewSession(t *testing.T) {
	base := t.TempDir()

	var first, other bytes.Buffer
	printNext(&first, newAdvisoryGate(base, "session-1"), nextFixture)
	printNext(&other, newAdvisoryGate(base, "session-2"), nextFixture)

	assert.Equal(t, first.String(), other.String())
}

// -s drops the Why without spending its firing: a quiet run must not be what silences
// the explanation for the next full one.
func TestPrintNextUnderSilentKeepsRunAndSpendsNothing(t *testing.T) {
	gate := newAdvisoryGate(t.TempDir(), "session-1")
	prev := global.silent
	global.silent = true
	defer func() { global.silent = prev }()

	var quiet bytes.Buffer
	printNext(&quiet, gate, nextFixture)
	assert.Equal(t, "\nnext:\n"+
		"  magus explain spell:go\n"+
		"  magus path spell:go spell:gomod\n", quiet.String())

	global.silent = false
	var loud bytes.Buffer
	printNext(&loud, gate, nextFixture)
	assert.Contains(t, loud.String(), "(explain names a node's edges.)")
}

// Nothing to suggest prints nothing at all, label included.
func TestPrintNextPrintsNothingWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	printNext(&buf, newAdvisoryGate(t.TempDir(), "session-1"), nil)
	assert.Empty(t, buf.String())
}

// The wrappers add one key beside the result they carry, and drop it entirely when
// there is nothing to suggest, so absence stays measurable.
func TestQueryAndExplainResultsCarryTheNextField(t *testing.T) {
	q, err := json.Marshal(queryWithNext{
		KnowledgeQueryOutput: types.KnowledgeQueryOutput{Query: "kind=spell go", MatchCount: 1},
		Next:                 nextFixture,
	})
	require.NoError(t, err)
	assert.Contains(t, string(q), `"query":"kind=spell go"`, "the result is flattened, not nested under the embedded type")
	assert.Contains(t, string(q), `"next":[{"id":"query-explain","run":"magus explain spell:go"`)

	x, err := json.Marshal(explainWithNext{
		KnowledgeExplainOutput: types.KnowledgeExplainOutput{Node: types.KnowledgeNode{ID: "spell:go"}},
	})
	require.NoError(t, err)
	assert.Contains(t, string(x), `"id":"spell:go"`)
	assert.NotContains(t, string(x), "next", "an empty set carries no field, never an empty list")

	f, err := json.Marshal(filesWithNext{
		FileReport: types.NewFileReport([]types.FileEntry{{Path: "MAGUS.md", Role: "output"}}),
		Next:       nextFixture[:1],
	})
	require.NoError(t, err)
	assert.Contains(t, string(f), `"path":"MAGUS.md"`)
	assert.Contains(t, string(f), `"next":[{"id":"query-explain"`)
}

// -o jsonl on a query already refuses: the result carries three collections and
// declares no primary. The added field must not become the stream that ambiguity was
// protecting against, so the walk descends into the embedded result.
func TestJSONLStaysAmbiguousForAQueryCarryingNext(t *testing.T) {
	var buf bytes.Buffer
	err := writeJSONL(&buf, queryWithNext{Next: nextFixture})

	require.Error(t, err)
	for _, key := range []string{"matches", "nodes", "links", "next"} {
		assert.Contains(t, err.Error(), key, "the error names every candidate, embedded ones included")
	}
	assert.Empty(t, buf.String())
}

// NextStreamable stands in for a result that declares its own stream. Exported because
// an embedded unexported type is not a promoted field the walk reads.
type NextStreamable struct {
	Rows []string `json:"rows" jsonl:"primary"`
}

// A declared primary inside an embedded result still wins, so wrapping a streamable
// record does not silently stop it streaming.
func TestJSONLFindsAPrimaryInsideAnEmbeddedResultNext(t *testing.T) {
	v := struct {
		NextStreamable
		Next []hint.Next `json:"next,omitempty"`
	}{NextStreamable: NextStreamable{Rows: []string{"a", "b"}}, Next: nextFixture}

	var buf bytes.Buffer
	require.NoError(t, writeJSONL(&buf, v))
	assert.Equal(t, []string{`"a"`, `"b"`}, strings.Fields(strings.TrimSpace(buf.String())))
}
