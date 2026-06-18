package depgraph

import (
	"context"
	"fmt"
	"testing"
)

// linear builds a chain: 0→1→2→…→n-1 where each node depends on the next.
func linear(b *testing.B, n int) *Graph {
	b.Helper()
	bl := New()
	ids := make([]ID, n)
	for i := range n {
		ids[i] = bl.AddNode(fmt.Sprintf("svc%04d", i))
	}
	for i := 0; i < n-1; i++ {
		_ = bl.AddEdge(ids[i], ids[i+1])
	}
	g, err := bl.Build()
	if err != nil {
		b.Fatal(err)
	}
	return g
}

// binTree builds a balanced binary tree with depth d (n = 2^d - 1 nodes).
// Node i depends on its children 2i+1 and 2i+2.
func binTree(b *testing.B, nodes int) *Graph {
	b.Helper()
	bl := New()
	ids := make([]ID, nodes)
	for i := range nodes {
		ids[i] = bl.AddNode(fmt.Sprintf("node%04d", i))
	}
	for i := 0; i < nodes; i++ {
		l, r := 2*i+1, 2*i+2
		if l < nodes {
			_ = bl.AddEdge(ids[i], ids[l])
		}
		if r < nodes {
			_ = bl.AddEdge(ids[i], ids[r])
		}
	}
	g, err := bl.Build()
	if err != nil {
		b.Fatal(err)
	}
	return g
}

// diamond builds n/2 "diamond" units chained together.
// Each diamond: A→{L,R}, L→B, R→B, then B is the A of the next unit.
func diamond(b *testing.B, n int) *Graph {
	b.Helper()
	bl := New()
	addN := func(s string) ID { return bl.AddNode(s) }
	prev := addN("d0000-bot")
	for i := 0; i < n/4; i++ {
		top := addN(fmt.Sprintf("d%04d-top", i+1))
		left := addN(fmt.Sprintf("d%04d-L", i+1))
		right := addN(fmt.Sprintf("d%04d-R", i+1))
		bot := addN(fmt.Sprintf("d%04d-bot", i+1))
		_ = bl.AddEdge(top, left)
		_ = bl.AddEdge(top, right)
		_ = bl.AddEdge(left, bot)
		_ = bl.AddEdge(right, bot)
		_ = bl.AddEdge(bot, prev)
		prev = top
	}
	g, err := bl.Build()
	if err != nil {
		b.Fatal(err)
	}
	return g
}

// layered builds a dense DAG with `layers` layers of `width` nodes each.
// Every node in layer i depends on every node in layer i+1.
func layered(b *testing.B, layers, width int) *Graph {
	b.Helper()
	bl := New()
	grid := make([][]ID, layers)
	for l := range layers {
		grid[l] = make([]ID, width)
		for w := range width {
			grid[l][w] = bl.AddNode(fmt.Sprintf("l%03d-w%03d", l, w))
		}
	}
	for l := 0; l < layers-1; l++ {
		for _, from := range grid[l] {
			for _, to := range grid[l+1] {
				_ = bl.AddEdge(from, to)
			}
		}
	}
	g, err := bl.Build()
	if err != nil {
		b.Fatal(err)
	}
	return g
}

func BenchmarkBuild_linear(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for b.Loop() {
				bl := New()
				ids := make([]ID, n)
				for i := range n {
					ids[i] = bl.AddNode(fmt.Sprintf("svc%04d", i))
				}
				for i := 0; i < n-1; i++ {
					_ = bl.AddEdge(ids[i], ids[i+1])
				}
				if _, err := bl.Build(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkBuild_layered(b *testing.B) {
	for _, wl := range [][2]int{{10, 10}, {50, 50}, {100, 100}} {
		layers, width := wl[0], wl[1]
		b.Run(fmt.Sprintf("layers=%d_width=%d", layers, width), func(b *testing.B) {
			for b.Loop() {
				_ = layered(b, layers, width)
			}
		})
	}
}

func BenchmarkTopoOrder_linear(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			g := linear(b, n)
			b.ResetTimer()
			for b.Loop() {
				_ = g.TopoOrder()
			}
		})
	}
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

func BenchmarkClosureBuild_linear(b *testing.B) {
	for _, n := range []int{100, 1_000, 10_000} {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for b.Loop() {
				g := linear(b, n)
				// BlastRadius forces closure build.
				_ = g.BlastRadius()
			}
		})
	}
}

func BenchmarkClosureBuild_layered(b *testing.B) {
	for _, wl := range [][2]int{{10, 10}, {50, 50}, {100, 100}} {
		layers, width := wl[0], wl[1]
		b.Run(fmt.Sprintf("layers=%d_width=%d", layers, width), func(b *testing.B) {
			for b.Loop() {
				g := layered(b, layers, width)
				_ = g.BlastRadius()
			}
		})
	}
}
