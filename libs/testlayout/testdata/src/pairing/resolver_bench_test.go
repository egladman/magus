package pairing // want `resolver_bench_test.go narrows resolver.go; these tests belong in resolver_test.go`

import "testing"

func BenchmarkResolve(b *testing.B) {
	for b.Loop() {
		Resolve("a")
	}
}
