package graph_test

import (
	"testing"

	"github.com/egladman/magus/internal/graph"
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

	if !g.Reachable(c, a) {
		t.Error("C should reach A via C→B→A")
	}
	if g.Reachable(a, c) {
		t.Error("A should not reach C (no such edge)")
	}
	if !g.Reachable(b, a) {
		t.Error("B should reach A via B→A")
	}
}

func TestGraph_ReverseClosure(t *testing.T) {
	g := build(t, [][]string{
		{"A"},
		{"B", "A"},
		{"C", "A"},
	})
	a, _ := g.ID("A")

	// Reverse closure of A: which nodes depend on A? → B and C
	ids := g.ReverseClosure(nil, []graph.ID{a})
	if len(ids) < 2 {
		t.Errorf("ReverseClosure: expected ≥2 dependents (B,C), got %d", len(ids))
	}
}

func TestGraph_BlastRadius_NonEmpty(t *testing.T) {
	g := build(t, [][]string{
		{"A"},
		{"B", "A"},
		{"C", "B"},
	})
	br := g.BlastRadius()
	if len(br) == 0 {
		t.Error("BlastRadius: expected non-empty result")
	}
}
