package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
		require.NoError(t, os.WriteFile(path, []byte(c), 0o644))
		files = append(files, relAbs{rel: filepath.Base(path), abs: path})
	}

	hashes, err := iouringHashBatch(files)
	if err != nil {
		t.Skipf("io_uring not available: %v", err)
	}

	for i, c := range contents {
		want := sha256.Sum256([]byte(c))
		wantHex := hex.EncodeToString(want[:])
		assert.Equalf(t, wantHex, hashes[i], "file %d", i)
	}
}

// TestIoUringFallbackLargeFile verifies that files larger than
// maxSingleRead are handled gracefully (skipped, not errored).
func TestIoUringFallbackLargeFile(t *testing.T) {
	dir := t.TempDir()
	// Write a file that exceeds maxSingleRead.
	large := make([]byte, maxSingleRead+1)
	path := filepath.Join(dir, "large")
	require.NoError(t, os.WriteFile(path, large, 0o644))
	files := []relAbs{{rel: "large", abs: path}}
	hashes, err := iouringHashBatch(files)
	if err != nil {
		t.Skipf("io_uring not available: %v", err)
	}
	// Large file must be skipped (empty hash), not errored.
	assert.Empty(t, hashes[0], "large file should have been skipped")
}
