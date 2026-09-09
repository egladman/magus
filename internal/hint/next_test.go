package hint

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runs projects a breadcrumb set onto (id, run) pairs. The Why is prose and moves;
// the id is what a measurement counts and the run is what a reader pastes, so those
// two are what the assertions pin.
func runs(next []Next) [][2]string {
	out := make([][2]string, len(next))
	for i, n := range next {
		out[i] = [2]string{n.ID, n.Run}
	}
	return out
}

func TestNextForQuery(t *testing.T) {
	next := NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
		{ID: "spell:go", Kind: "spell", Label: "go"},
		{ID: "symbol:RunAll", Kind: types.KindSymbol, Label: "RunAll"},
		{ID: "spell:gomod", Kind: "spell", Label: "gomod"},
	}})

	assert.Equal(t, [][2]string{
		{"query-explain", "magus explain spell:go"},
		{"query-path", "magus path spell:go spell:gomod"},
		{"query-refs", "magus refs RunAll"},
	}, runs(next))
}

// The path breadcrumb needs two matches of ONE kind; a set with no repeated kind has
// no pair to connect and must not invent one.
func TestNextForQueryWithoutASharedKind(t *testing.T) {
	next := NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
		{ID: "spell:go", Kind: "spell", Label: "go"},
		{ID: "target:.:build", Kind: "target", Label: "build"},
	}})

	assert.Equal(t, [][2]string{{"query-explain", "magus explain spell:go"}}, runs(next))
}

// Absence has to be measurable: nothing matched means no field at all, never an empty
// list a consumer would count as a suggestion nobody took.
func TestNextIsNilWhenThereIsNothingToSuggest(t *testing.T) {
	assert.Nil(t, NextForQuery(types.KnowledgeQueryOutput{}))
	assert.Nil(t, NextForExplain(types.KnowledgeExplainOutput{}))
	assert.Nil(t, NextForFiles(nil))
	assert.Nil(t, NextForAffected("ci", nil))
	assert.Nil(t, NextForFailure("", "", ""))
}

func TestNextForExplain(t *testing.T) {
	next := NextForExplain(types.KnowledgeExplainOutput{
		Node: types.KnowledgeNode{ID: "symbol:RunAll", Kind: types.KindSymbol, Label: "RunAll", Source: "internal/cache/run.go:42"},
		Out:  []types.KnowledgeEdgeRef{{Other: "spell:go"}, {Other: "spell:gomod"}},
		In:   []types.KnowledgeEdgeRef{{Other: "spell:gomod"}},
	})

	assert.Equal(t, [][2]string{
		{"explain-path", "magus path symbol:RunAll spell:gomod"},
		{"explain-refs", "magus refs RunAll"},
		{"explain-describe-file", "magus describe file internal/cache/run.go"},
	}, runs(next))
}

// A node reached by one edge each is a tie, and a tie goes to the card's own order
// rather than to whatever the map iterated first.
func TestNextForExplainBreaksNeighborTiesByCardOrder(t *testing.T) {
	card := types.KnowledgeExplainOutput{
		Node: types.KnowledgeNode{ID: "spell:go", Kind: "spell", Label: "go"},
		Out:  []types.KnowledgeEdgeRef{{Other: "op:go:go-build"}, {Other: "op:go:go-test"}},
	}
	for range 20 {
		require.Equal(t, "magus path spell:go op:go:go-build", NextForExplain(card)[0].Run)
	}
}

func TestNextForFiles(t *testing.T) {
	next := NextForFiles([]types.FileEntry{
		{Path: "MAGUS.md", OutputOf: []string{"."}, SourceOf: []string{"docs"}},
		{Path: "internal/hint/hint.go", SourceOf: []string{"."}},
	})

	assert.Equal(t, [][2]string{
		{"file-impact", "magus affected --impact"},
		{"file-regenerate", "magus run generate:rw ."},
	}, runs(next))
}

// A cross-project output lands in the owner's tree but only the declaring project's
// target regenerates it, so the breadcrumb names the declarer.
func TestNextForFilesRegeneratesFromTheDeclarer(t *testing.T) {
	next := NextForFiles([]types.FileEntry{{
		Path:     "docs/gen/index.html",
		OutputOf: []string{"docs"},
		Claims:   []types.FileClaim{{Project: "tools/site", Target: "render", Role: "output", Glob: "docs/gen/**"}},
	}})

	assert.Equal(t, [][2]string{{"file-regenerate", "magus run generate:rw tools/site"}}, runs(next))
}

func TestNextForAffected(t *testing.T) {
	next := NextForAffected("ci", []string{"libs/gopherbuzz", "docs"})

	assert.Equal(t, [][2]string{
		{"affected-plan", "magus affected ci --plan"},
		{"affected-explain", "magus affected --explain libs/gopherbuzz"},
	}, runs(next))
}

func TestNextForFailure(t *testing.T) {
	next := NextForFailure("libs/gopherbuzz", "test", "out1a2b3c")

	assert.Equal(t, [][2]string{
		{"run-output", "magus query output out1a2b3c"},
		{"run-explain-target", "magus explain target:libs/gopherbuzz:test"},
	}, runs(next))
}

// A failure that captured nothing still has a target to explain, and a ref line
// pointing at "" would be a command that cannot run.
func TestNextForFailureWithoutARef(t *testing.T) {
	next := NextForFailure("libs/gopherbuzz", "test", "")

	assert.Equal(t, [][2]string{{"run-explain-target", "magus explain target:libs/gopherbuzz:test"}}, runs(next))
}

// The cap is the context budget. No builder can exceed it today, which is exactly why
// it is asserted on the shared trim rather than through one of them: the next builder
// added is the one that would.
func TestNextCapsAtThree(t *testing.T) {
	assert.Equal(t, 3, NextCap, "three, one line each: a longer list is a wall nobody reads")

	long := []Next{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	assert.Equal(t, []Next{{ID: "a"}, {ID: "b"}, {ID: "c"}}, capNext(long))

	for _, build := range []func() []Next{
		func() []Next {
			return NextForQuery(types.KnowledgeQueryOutput{Matches: []types.KnowledgeMatch{
				{ID: "symbol:A", Kind: types.KindSymbol, Label: "A"},
				{ID: "symbol:B", Kind: types.KindSymbol, Label: "B"},
				{ID: "symbol:C", Kind: types.KindSymbol, Label: "C"},
			}})
		},
		func() []Next {
			return NextForExplain(types.KnowledgeExplainOutput{
				Node: types.KnowledgeNode{ID: "symbol:A", Kind: types.KindSymbol, Label: "A", Source: "a.go:1"},
				Out:  []types.KnowledgeEdgeRef{{Other: "spell:go"}},
			})
		},
	} {
		assert.LessOrEqual(t, len(build()), NextCap)
	}
}

// Provenance is "path" or "path:line", and describe file classifies paths. A target's
// provenance is its project DIRECTORY, which has no classification worth asking for.
func TestNextSourcePath(t *testing.T) {
	assert.Equal(t, "internal/cache/run.go", sourcePath("internal/cache/run.go:42"))
	assert.Equal(t, "internal/cache/run.go", sourcePath("internal/cache/run.go"))
	assert.Equal(t, "spells/go/spell.buzz:main", sourcePath("spells/go/spell.buzz:main"))
	assert.Empty(t, sourcePath(""))
	assert.Empty(t, sourcePath("."), "a project directory is not a file to classify")
	assert.Empty(t, sourcePath("libs/gopherbuzz"))
}
