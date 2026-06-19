package cache

import (
	"compress/gzip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/codec"
)

// syntheticManifest builds a Manifest with n output records — representative
// of a mid-size project cache entry.
func syntheticManifest(n int) *Manifest {
	outputs := make([]OutputRecord, n)
	for i := range outputs {
		outputs[i] = OutputRecord{
			Path: fmt.Sprintf("dist/lib/component-%d.js", i),
			Blob: fmt.Sprintf("%064x", i),
			Mode: 0o644,
			Size: int64(i * 1024),
		}
	}
	return &Manifest{
		ProjectPath: "apps/my-service",
		Hash:        fmt.Sprintf("%064x", 42),
		Outputs:     outputs,
		CreatedAt:   time.Now().UTC(),
	}
}

// BenchmarkManifestRead measures readManifest: one os.ReadFile + json.Unmarshal.
func BenchmarkManifestRead(b *testing.B) {
	dir := b.TempDir()
	c := &Cache{dir: dir}
	m := syntheticManifest(20)

	data, err := codec.MarshalIndent(m, "", "  ")
	if err != nil {
		b.Fatal(err)
	}
	p := c.manifestPath(m.ProjectPath, m.Hash)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for range b.N {
		if _, err := c.readManifest(m.ProjectPath, m.Hash); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkManifestWrite measures util.MarshalIndent + writeAtomic for a
// typical manifest.
func BenchmarkManifestWrite(b *testing.B) {
	dir := b.TempDir()
	c := &Cache{dir: dir}
	m := syntheticManifest(20)

	b.ResetTimer()
	for range b.N {
		data, err := codec.MarshalIndent(m, "", "  ")
		if err != nil {
			b.Fatal(err)
		}
		if err := writeAtomic(c.manifestPath(m.ProjectPath, m.Hash), data); err != nil {
			b.Fatal(err)
		}
	}
}

// syntheticMtimeStore returns a fresh store pre-populated with n entries.
func syntheticMtimeStore(b *testing.B, n int) *mtimeStore {
	b.Helper()
	dir := b.TempDir()
	s := newMtimeStore(dir, nil)
	s.loaded = true
	// Pre-initialise the shard maps so set() works without a preceding load().
	for i := range s.shards {
		s.shards[i] = make(map[string]mtimeEntry)
	}
	for i := range n {
		s.set(
			fmt.Sprintf("/workspace/project/src/component-%04d.ts", i),
			fmt.Sprintf("%064x", i),
			int64(1_700_000_000+i),
			int64(i*100),
		)
	}
	// Clear dirty bits so each sub-benchmark controls them explicitly.
	for i := range s.dirty {
		s.dirty[i] = false
	}
	return s
}

// BenchmarkMtimeStoreLoad measures loading a gzipped JSON mtime store
// with 1 000 entries. This is the startup cost paid once per magus run.
func BenchmarkMtimeStoreLoad(b *testing.B) {
	// Write a 1000-entry store to disk first.
	s := syntheticMtimeStore(b, 1000)
	for i := range s.dirty {
		s.dirty[i] = true
	}
	s.flush(context.Background())
	dir := s.dir

	b.ResetTimer()
	for range b.N {
		fresh := newMtimeStore(filepath.Dir(dir), nil)
		fresh.load(context.Background())
	}
}

// BenchmarkMtimeStoreFlushOneDirty measures flushing when exactly one entry
// (one shard) has changed — the common case during an incremental build.
func BenchmarkMtimeStoreFlushOneDirty(b *testing.B) {
	s := syntheticMtimeStore(b, 1000)

	b.ResetTimer()
	for range b.N {
		// Dirty exactly one entry on each iteration.
		s.set(
			"/workspace/project/src/component-0000.ts",
			fmt.Sprintf("%064x", b.N),
			int64(1_700_000_000),
			100,
		)
		s.flush(context.Background())
	}
}

// BenchmarkMtimeStoreFlushAllDirty measures the worst case: all 1 000 entries
// must be re-encoded (e.g., first run after a full rebuild).
func BenchmarkMtimeStoreFlushAllDirty(b *testing.B) {
	s := syntheticMtimeStore(b, 1000)

	b.ResetTimer()
	for range b.N {
		for i := range s.dirty {
			s.dirty[i] = true
		}
		s.flush(context.Background())
	}
}

// BenchmarkMtimeStoreConcurrentFlush measures contention when N goroutines
// concurrently set + flush entries that land on distinct shards. Mirrors
// the fan-out hot path where each completed target calls flush() at the end
// of its hashStep.
func BenchmarkMtimeStoreConcurrentFlush(b *testing.B) {
	for _, workers := range []int{1, 4, 8} {
		workers := workers
		b.Run(fmt.Sprintf("workers=%d", workers), func(b *testing.B) {
			s := syntheticMtimeStore(b, 1000)

			b.ReportAllocs()
			b.ResetTimer()

			perWorker := b.N / workers
			if perWorker == 0 {
				perWorker = 1
			}

			done := make(chan struct{}, workers)
			for w := range workers {
				w := w
				go func() {
					for i := range perWorker {
						s.set(
							fmt.Sprintf("/workspace/w-%d/file-%d.ts", w, i),
							fmt.Sprintf("%064x", i),
							int64(1_700_000_000+i),
							int64(100),
						)
						s.flush(context.Background())
					}
					done <- struct{}{}
				}()
			}
			for range workers {
				<-done
			}
		})
	}
}

// BenchmarkMtimeStoreGzipRoundTrip is a micro-benchmark of the gzip+JSON
// encode/decode cycle for a single shard (~250 entries at uniform distribution).
func BenchmarkMtimeStoreGzipRoundTrip(b *testing.B) {
	shard := make(map[string]mtimeEntry, 250)
	for i := range 250 {
		shard[fmt.Sprintf("/workspace/project/src/file-%04d.ts", i)] = mtimeEntry{
			Mtime: int64(1_700_000_000 + i),
			Size:  int64(i * 100),
			Hash:  fmt.Sprintf("%064x", i),
		}
	}

	tmp := b.TempDir()
	b.ResetTimer()
	for range b.N {
		p := filepath.Join(tmp, "shard.json.gz")
		f, err := os.Create(p)
		if err != nil {
			b.Fatal(err)
		}
		gz := gzip.NewWriter(f)
		if err := codec.NewEncoder(gz).Encode(shard); err != nil {
			b.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			b.Fatal(err)
		}
		f.Close()

		f, err = os.Open(p)
		if err != nil {
			b.Fatal(err)
		}
		gz2, err := gzip.NewReader(f)
		if err != nil {
			b.Fatal(err)
		}
		var out map[string]mtimeEntry
		_ = codec.NewDecoder(gz2).Decode(&out)
		gz2.Close()
		f.Close()
	}
}
