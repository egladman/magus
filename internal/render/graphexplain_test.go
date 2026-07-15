package render

import (
	"strings"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestExplainText: the readable card folds edge direction into a natural-language
// verb (out=active, in=passive), groups by that verb with a count when a list has
// more than one, and lists FULL node IDs (the token to pass to the next explain).
func TestExplainText(t *testing.T) {
	out := types.KnowledgeExplainOutput{
		Node:        types.KnowledgeNode{ID: "target:.:test", Kind: "target", Doc: "Run the tests.", Source: ".", Attrs: map[string]string{"engine": "buzz"}},
		BlastRadius: 2,
		Out: []types.KnowledgeEdgeRef{
			{Relation: types.RelationUses, Other: "op:go:go-test", OtherKind: "op"},
			{Relation: types.RelationDependsOn, Other: "target:.:format", OtherKind: "target"},
		},
		In: []types.KnowledgeEdgeRef{
			{Relation: types.RelationContains, Other: "project:.", OtherKind: "project"},
			{Relation: types.RelationDependsOn, Other: "target:.:ci", OtherKind: "target"},
		},
	}
	got := ExplainText(out)

	assert.Contains(t, got, "target:.:test   target", "header carries id and kind")
	assert.Contains(t, got, "Run the tests.")
	assert.Contains(t, got, "engine: buzz")
	assert.Contains(t, got, "2 nodes reach this")
	// Out edges render active; in edges render passive - direction is in the verb.
	assert.Contains(t, got, "uses         op:go:go-test")
	assert.Contains(t, got, "depends on   target:.:format", "out depends_on is active")
	assert.Contains(t, got, "part of      project:.", "in contains is passive")
	assert.Contains(t, got, "required by  target:.:ci", "in depends_on is passive")
	// The old arrow notation is gone.
	assert.NotContains(t, got, "<--")
	assert.NotContains(t, got, "-->")
	assert.NotContains(t, got, "out edges")
}

// TestExplainTextCountsAndFullIDs: a multi-edge group states its count before the
// list and keeps full IDs, so an agent never miscounts and always has the next-call
// token verbatim.
func TestExplainTextCountsAndFullIDs(t *testing.T) {
	var in []types.KnowledgeEdgeRef
	for _, id := range []string{"op:go:go-build", "op:go:go-test", "op:go:go-vet", "spell:go"} {
		in = append(in, types.KnowledgeEdgeRef{Relation: types.RelationUses, Other: id, OtherKind: "op"})
	}
	got := ExplainText(types.KnowledgeExplainOutput{
		Node: types.KnowledgeNode{ID: "tool:go", Kind: "tool"},
		In:   in,
	})
	assert.Contains(t, got, "used by (4)", "count precedes a multi-item list")
	assert.Contains(t, got, "op:go:go-build", "full IDs, not prettified labels")
	assert.Contains(t, got, "spell:go")
}

// TestPathText renders the chain as natural-language steps with the direction in
// the verb, and reports the step count.
func TestPathText(t *testing.T) {
	got := PathText(types.KnowledgePathOutput{
		From: "target:.:test", To: "tool:go", Found: true,
		Steps: []types.KnowledgePathStep{
			{Relation: types.RelationUses, To: "op:go:go-test", Forward: true},
			{Relation: types.RelationUses, To: "tool:go", Forward: true},
		},
	})
	assert.Contains(t, got, "target:.:test -> tool:go  (2 steps)")
	assert.Contains(t, got, "uses  op:go:go-test")
	assert.NotContains(t, got, "-->")

	none := PathText(types.KnowledgePathOutput{From: "a", To: "b", Found: false})
	assert.Contains(t, none, "no path connects these nodes")
	assert.Contains(t, none, "(no path)")
}

// TestPhraseForFallback: an unknown relation still conveys direction rather than
// dropping it.
func TestPhraseForFallback(t *testing.T) {
	assert.Equal(t, "uses", phraseFor(types.RelationUses, true))
	assert.Equal(t, "used by", phraseFor(types.RelationUses, false))
	assert.True(t, strings.Contains(phraseFor("mystery", true), "mystery"))
	assert.True(t, strings.Contains(phraseFor("mystery", false), "mystery"))
}
