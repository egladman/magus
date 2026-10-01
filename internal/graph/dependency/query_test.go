package dependency

import (
	"context"
	"fmt"
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

func BenchmarkReverseClosure_seed1_linear(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := linear(b, n)
			// Seed is the deepest dependency (last node in lex order).
			leaf, _ := g.ID(fmt.Sprintf("svc%04d", n-1))
			dst := make([]ID, 0, n)
			b.ResetTimer()
			for b.Loop() {
				dst = g.ReverseClosure(dst[:0], []ID{leaf})
			}
		})
	}
}

func BenchmarkReverseClosure_seedAll_linear(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := linear(b, n)
			seeds := make([]ID, n)
			for i := range n {
				seeds[i], _ = g.ID(fmt.Sprintf("svc%04d", i))
			}
			dst := make([]ID, 0, n)
			b.ResetTimer()
			for b.Loop() {
				dst = g.ReverseClosure(dst[:0], seeds)
			}
		})
	}
}

func BenchmarkReverseClosure_seed1_diamond(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := diamond(b, n)
			leaf, _ := g.ID("d0000-bot")
			dst := make([]ID, 0, g.Len())
			b.ResetTimer()
			for b.Loop() {
				dst = g.ReverseClosure(dst[:0], []ID{leaf})
			}
		})
	}
}

func BenchmarkBlastRadius_linear(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := linear(b, n)
			b.ResetTimer()
			for b.Loop() {
				_ = g.BlastRadius()
			}
		})
	}
}

func BenchmarkBlastRadius_diamond(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := diamond(b, n)
			b.ResetTimer()
			for b.Loop() {
				_ = g.BlastRadius()
			}
		})
	}
}

func BenchmarkNCCD_linear(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := linear(b, n)
			b.ResetTimer()
			for b.Loop() {
				_ = g.NCCD()
			}
		})
	}
}

func BenchmarkNCCD_layered(b *testing.B) {
	for _, wl := range [][2]int{{10, 10}, {50, 50}} {
		layers, width := wl[0], wl[1]
		b.Run(fmt.Sprintf("layers=%d_width=%d", layers, width), func(b *testing.B) {
			g := layered(b, layers, width)
			b.ResetTimer()
			for b.Loop() {
				_ = g.NCCD()
			}
		})
	}
}

func BenchmarkPathsFromSeeds_linear(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := linear(b, n)
			target, _ := g.ID("svc0000")
			seed, _ := g.ID(fmt.Sprintf("svc%04d", n-1))
			out := make([]AffectedPath, 0, 4)
			b.ResetTimer()
			for b.Loop() {
				out = g.PathsFromSeeds(target, []ID{seed}, out)
			}
		})
	}
}

func BenchmarkNearCycles_depth3_linear(b *testing.B) {
	for _, n := range []int{100, 1_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := linear(b, n)
			b.ResetTimer()
			for b.Loop() {
				_ = g.NearCycles(context.Background(), 3)
			}
		})
	}
}

func BenchmarkNearCycles_depth3_binTree(b *testing.B) {
	for _, n := range []int{127, 1023} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := binTree(b, n)
			b.ResetTimer()
			for b.Loop() {
				_ = g.NearCycles(context.Background(), 3)
			}
		})
	}
}
