package cache

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkReplayBlob measures the replayBlob hot path — the function that
// materialises a CAS blob into the workspace on every cache hit. On CoW
// filesystems (btrfs, XFS reflink=1, APFS) the reflink path is O(1);
// on others it falls through to hard-link then io.Copy.
//
// Run: go test -bench=BenchmarkReplayBlob -benchtime=5s ./magus/cache/
func BenchmarkReplayBlob(b *testing.B) {
	const size = 64 << 20 // 64 MiB
	b.SetBytes(size)

	src := filepath.Join(b.TempDir(), "blob")
	data := make([]byte, size)
	if err := os.WriteFile(src, data, 0o644); err != nil {
		b.Fatal(err)
	}

	dstDir := b.TempDir()
	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		dst := filepath.Join(dstDir, "out")
		_ = os.Remove(dst)
		if err := replayBlob(src, dst); err != nil {
			b.Fatal(err)
		}
	}
}
