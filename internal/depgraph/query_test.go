package depgraph

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGraph_Reachable(t *testing.T) {
	g := build(t, [][]string{
		{"A"},
		{"B", "A"},
		{"C", "B"},
	})
	a, _ := g.ID("A")
	b, _ := g.ID("B")
	c, _ := g.ID("C")

	assert.True(t, g.Reachable(c, a), "C should reach A via C→B→A")
	assert.False(t, g.Reachable(a, c), "A should not reach C (no such edge)")
	assert.True(t, g.Reachable(b, a), "B should reach A via B→A")
}

func TestGraph_ReverseClosure(t *testing.T) {
	g := build(t, [][]string{
		{"A"},
		{"B", "A"},
		{"C", "A"},
	})
	a, _ := g.ID("A")

	// Reverse closure of A: which nodes depend on A? → B and C
	ids := g.ReverseClosure(nil, []ID{a})
	assert.GreaterOrEqual(t, len(ids), 2, "expected ≥2 dependents (B,C)")
}

func TestGraph_BlastRadius_NonEmpty(t *testing.T) {
	g := build(t, [][]string{
		{"A"},
		{"B", "A"},
		{"C", "B"},
	})
	br := g.BlastRadius()
	assert.NotEmpty(t, br, "BlastRadius: expected non-empty result")
}
