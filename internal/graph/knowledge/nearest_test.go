package knowledge

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// nearestGraph is the shape the suggestion has to work over: kind-prefixed ids
// whose distinguishing part is the trailing segment, plus a symbol whose id is
// dominated by a module path.
func nearestGraph() *Graph {
	g := NewGraph()
	for _, n := range []types.KnowledgeNode{
		{ID: "file:cmd/magus/guard_shell.go", Kind: types.KindFile, Label: "cmd/magus/guard_shell.go"},
		{ID: "file:cmd/magus/guard_write.go", Kind: types.KindFile, Label: "cmd/magus/guard_write.go"},
		{ID: "target:.:build", Kind: types.KindTarget, Label: "build"},
		{ID: "spell:go", Kind: types.KindSpell, Label: "go"},
		{ID: "docsection:docs/reference/buzz/url.md#build", Kind: types.KindDocSection, Label: "build"},
		{ID: "symbol:gomod github.com/egladman/magus `github.com/egladman/magus/cmd/magus`/adoptionRun().", Kind: types.KindSymbol, Label: "adoptionRun"},
	} {
		g.AddNode(n)
	}
	return g
}

func TestNearestNodeCorrectsATypo(t *testing.T) {
	g := nearestGraph()

	// The typo is in the LEAF, which is what a reader types. Distance over the raw
	// id would be dominated by "file:cmd/magus/".
	assert.Equal(t, "file:cmd/magus/guard_shell.go", g.NearestNode("kind=file guard_shel.go"))
	// The full workspace-relative path works too, so a pasted path with one slip lands.
	assert.Equal(t, "file:cmd/magus/guard_shell.go", g.NearestNode("cmd/magus/guard_shel.go"))
	// A qualifier-separated id: the leaf of "target:.:build" is "build". A doc
	// heading of the same name is exactly as close, and loses on kindRank - the
	// reader asked about a thing, not about prose describing one.
	assert.Equal(t, "target:.:build", g.NearestNode("buld"))
}

func TestNearestNodeRespectsTheQueryKindFilter(t *testing.T) {
	g := nearestGraph()

	// Without a filter "gu" reaches nothing; with one the suggestion must still be
	// a node the query itself would have accepted.
	assert.Equal(t, "spell:go", g.NearestNode("kind=spell goo"))
	assert.Empty(t, g.NearestNode("kind=target goo"), "no target is close to goo")
	assert.Empty(t, g.NearestNode("kind!=spell goo"), "the excluded kind holds the only near name")
}

func TestNearestNodeAbstains(t *testing.T) {
	g := nearestGraph()

	assert.Empty(t, g.NearestNode("zzzzzzzzzz"), "nothing close enough")
	assert.Empty(t, g.NearestNode("buld guard_shel.go"), "two terms name no single misspelling")
	assert.Empty(t, g.NearestNode("guard_*.go"), "a wildcard is a pattern to widen, not a name to correct")
	assert.Empty(t, g.NearestNode("kind=file"), "a field-only query has no term to correct")
	assert.Empty(t, g.NearestNode(""))
}

func TestNearestSymbolStaysInTheSymbolLayer(t *testing.T) {
	g := nearestGraph()

	assert.Equal(t, "symbol:gomod github.com/egladman/magus `github.com/egladman/magus/cmd/magus`/adoptionRun().",
		g.NearestSymbol("adoptionRunn"))
	// refs resolves symbols and nothing else, so a near-miss on a target would name
	// something refs would fail to find a second time.
	assert.Empty(t, g.NearestSymbol("buld"))
}

func TestNearestNodeTieBreaksOnLowerID(t *testing.T) {
	g := NewGraph()
	g.AddNode(types.KnowledgeNode{ID: "target:.:aab", Kind: types.KindTarget, Label: "aab"})
	g.AddNode(types.KnowledgeNode{ID: "target:.:aaa", Kind: types.KindTarget, Label: "aaa"})

	// Both are distance 1 from "aac"; map iteration order must not decide.
	for range 20 {
		assert.Equal(t, "target:.:aaa", g.NearestNode("aac"))
	}
}

// TestNearestNodeNeverEntersResults is the invariant the whole file rests on: a
// suggestion is offered only after matching has already reported nothing, and
// matching itself must not have moved.
func TestNearestNodeNeverEntersResults(t *testing.T) {
	g := nearestGraph()

	out := g.Query("kind=file guard_shel.go", DefaultBudget)
	require.Zero(t, out.MatchCount, "a near miss must not become a match")
	assert.Empty(t, out.Matches)
	assert.NotEmpty(t, g.NearestNode("kind=file guard_shel.go"), "the suggestion lives outside the result set")
}

// BenchmarkNearestNode measures the cost of the zero-result scan at real
// workspace scale: this repo carries ~5.2k default nodes and ~41k with the
// symbol layer merged.
func BenchmarkNearestNode(b *testing.B) {
	for _, size := range []int{5000, 41000} {
		g := NewGraph()
		for i := range size {
			g.AddNode(types.KnowledgeNode{
				ID:    fmt.Sprintf("symbol:gomod github.com/egladman/magus 0.0.1 internal/pkg%04d/someSymbolName%05d().", i%200, i),
				Kind:  types.KindSymbol,
				Label: fmt.Sprintf("someSymbolName%05d", i),
			})
		}
		g.AddNode(types.KnowledgeNode{ID: "file:cmd/magus/guard_shell.go", Kind: types.KindFile, Label: "cmd/magus/guard_shell.go"})
		b.Run(fmt.Sprintf("nodes=%d", size), func(b *testing.B) {
			for b.Loop() {
				g.NearestNode("guard_shel.go")
			}
		})
	}
}
