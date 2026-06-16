package targetgraph

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkExtract measures the static parse of the real magusfile.buzz — the
// largest realistic input (every target the project ships). Extract runs once
// per `magus describe graph` / `magus run generate` invocation, so this benchmark
// exists to confirm it stays negligible against CLI-invocation cost, not because
// it sits on a hot loop. Re-run: go test -bench=BenchmarkExtract -benchmem -count=10
func BenchmarkExtract(b *testing.B) {
	src, err := os.ReadFile(filepath.Join("..", "..", "magusfile.buzz"))
	if err != nil {
		b.Fatalf("read magusfile.buzz: %v", err)
	}
	s := string(src)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		nodes := Extract(s)
		if len(nodes) == 0 {
			b.Fatal("no nodes extracted")
		}
	}
}
