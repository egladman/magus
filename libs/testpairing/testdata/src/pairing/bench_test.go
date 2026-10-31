package pairing // want `bench_test.go keeps benchmarks apart from the tests of the file they measure; move them into that file's _test.go`

import "testing"

func BenchmarkSweep(b *testing.B) {
	for b.Loop() {
		Resolve("a")
	}
}
