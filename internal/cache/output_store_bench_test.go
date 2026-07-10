package cache

import (
	"fmt"
	"strings"
	"testing"
)

// These benchmarks baseline the output-store paths that `magus query ref` rides. Before the
// verbatim-blob refactor the store JSON-encoded per-line events on write and RECONSTRUCTED raw
// text on read; after, persist writes the raw blob + one descriptor and OutputByRef is a
// straight file read. Same benchmark names bracket both, so `benchstat old new` quantifies the
// win (go test -bench=OutputStore -benchmem -count=10).

// benchRaw builds a realistic target log: n lines (~80 bytes each) as verbatim bytes.
func benchRaw(n int) []byte {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "[%04d] go: downloading example.com/some/module v1.%d.0 (cached, verified)\n", i, i%9)
	}
	return []byte(b.String())
}

const benchLines = 200

func benchMeta() OutputDescriptor {
	return OutputDescriptor{Project: "cmd/magus", Target: "build", DurationMs: 1234}
}

// BenchmarkOutputStorePersist measures the write path run for every cached target execution.
func BenchmarkOutputStorePersist(b *testing.B) {
	raw := benchRaw(benchLines)
	meta := benchMeta()
	s := newOutputStore(b.TempDir())
	b.ReportAllocs()
	b.ResetTimer()
	for i := range b.N {
		if _, err := s.persist(fmt.Sprintf("deadbeefcafef%03d", i%1000), raw, meta); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOutputStoreLookupOutput measures the full `magus query ref` read.
func BenchmarkOutputStoreLookupOutput(b *testing.B) {
	raw := benchRaw(benchLines)
	dir := b.TempDir()
	s := newOutputStore(dir)
	ref, err := s.persist("deadbeefcafef00d", raw, benchMeta())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, _, err := OutputByRef(dir, ref); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkOutputStoreSplitOutputLines measures the display projection (blob -> per-line events)
// the viewer path (OutputEventsByRef) performs. Same name straddles the refactor (it benched
// outputEventsFromRaw before) so benchstat still lines up.
func BenchmarkOutputStoreSplitOutputLines(b *testing.B) {
	raw := benchRaw(benchLines)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = splitOutputLines(raw)
	}
}
