package textembed

import (
	"os"
	"path/filepath"
	"testing"
)

// fileIndex writes ix to a file and reads it back, so Search reads rows with pread.
func fileIndex(b *testing.B, c docSet, ix *Index) *Index {
	b.Helper()
	path := filepath.Join(b.TempDir(), "vectors.idx")
	if err := os.WriteFile(path, writeIndex(b, ix), 0o600); err != nil {
		b.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { f.Close() })
	info, err := f.Stat()
	if err != nil {
		b.Fatal(err)
	}
	back, err := ReadIndex(f, info.Size(), c.table)
	if err != nil {
		b.Fatal(err)
	}
	return back
}

// BenchmarkSearch runs the big doc set's queries in turn: about 96k docs of 256 dims,
// rows in memory and in a file in the page cache.
func BenchmarkSearch(b *testing.B) {
	c, mem := bigDocSet()
	file := fileIndex(b, c, mem)
	for _, src := range []struct {
		name string
		ix   *Index
	}{{"mem", mem}, {"file", file}} {
		for _, tt := range []struct {
			name string
			opts SearchOptions
		}{
			{"depth10", SearchOptions{Depth: 10}},
			{"depth100/rescore2048", SearchOptions{Depth: 100, Rescore: 2048}},
			{"exact", SearchOptions{Depth: 10, Rescore: mem.Len()}},
		} {
			b.Run(src.name+"/"+tt.name, func(b *testing.B) {
				b.ReportAllocs()
				i := 0
				for b.Loop() {
					src.ix.Search(c.queries[i%len(c.queries)], tt.opts)
					i++
				}
			})
		}
	}
	b.Run("scan", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			mem.candidates(c.queries[i%len(c.queries)].Q, nil, 512)
			i++
		}
	})
}

func BenchmarkReadIndex(b *testing.B) {
	c, mem := bigDocSet()
	path := filepath.Join(b.TempDir(), "vectors.idx")
	if err := os.WriteFile(path, writeIndex(b, mem), 0o600); err != nil {
		b.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := ReadIndex(f, info.Size(), c.table); err != nil {
			b.Fatal(err)
		}
	}
}
