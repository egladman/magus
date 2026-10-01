package dependency

import (
	"fmt"
	"testing"
)

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
