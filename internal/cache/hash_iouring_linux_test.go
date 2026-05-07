package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// TestIoUringHashBatch verifies that iouringHashBatch produces correct
// sha256 hashes matching what sha256.Sum256 produces directly.
func TestIoUringHashBatch(t *testing.T) {
	dir := t.TempDir()
	contents := []string{
		"hello, world",
		"the quick brown fox",
		"magus/cache io_uring fast path",
	}
	var files []relAbs
	for i, c := range contents {
		path := filepath.Join(dir, "f"+string(rune('a'+i)))
		if err := os.WriteFile(path, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
		files = append(files, relAbs{rel: filepath.Base(path), abs: path})
	}

	hashes, err := iouringHashBatch(files)
	if err != nil {
		t.Skipf("io_uring not available: %v", err)
	}

	for i, c := range contents {
		want := sha256.Sum256([]byte(c))
		wantHex := hex.EncodeToString(want[:])
		if hashes[i] != wantHex {
			t.Errorf("file %d: got %q, want %q", i, hashes[i], wantHex)
		}
	}
}

// TestIoUringFallbackLargeFile verifies that files larger than
// maxSingleRead are handled gracefully (skipped, not errored).
func TestIoUringFallbackLargeFile(t *testing.T) {
	dir := t.TempDir()
	// Write a file that exceeds maxSingleRead.
	large := make([]byte, maxSingleRead+1)
	path := filepath.Join(dir, "large")
	if err := os.WriteFile(path, large, 0o644); err != nil {
		t.Fatal(err)
	}
	files := []relAbs{{rel: "large", abs: path}}
	hashes, err := iouringHashBatch(files)
	if err != nil {
		t.Skipf("io_uring not available: %v", err)
	}
	// Large file must be skipped (empty hash), not errored.
	if hashes[0] != "" {
		t.Errorf("large file should have been skipped, got hash %q", hashes[0])
	}
}
