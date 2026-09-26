// Narrows nothing, but a benchmark always measures some file, so it is reported.
package strict // want `sweep_bench_test.go keeps benchmarks apart from the tests of the file they measure`

import "testing"

func BenchmarkSweep(b *testing.B) {
	for b.Loop() {
		_ = Widget{}
	}
}
