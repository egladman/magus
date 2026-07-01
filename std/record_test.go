package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRecordModeSkipsFilesystemWrites is the safety gate for deep dry-run: under a
// recording context, effectful filesystem ops must record-and-skip (return nil
// without touching disk), while a normal context still performs the write.
func TestRecordModeSkipsFilesystemWrites(t *testing.T) {
	dir := t.TempDir()
	rec := types.WithRecord(context.Background())
	plain := context.Background()

	t.Run("write_file", func(t *testing.T) {
		p := filepath.Join(dir, "w.txt")
		require.NoError(t, FsWriteFile(rec, p, "data"))
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "record mode must not write the file")

		require.NoError(t, FsWriteFile(plain, p, "data"))
		_, err = os.Stat(p)
		assert.NoError(t, err, "normal mode must write the file")
	})

	t.Run("mkdir_all", func(t *testing.T) {
		p := filepath.Join(dir, "sub", "deep")
		require.NoError(t, FsMkdirAll(rec, p, 0o755))
		_, err := os.Stat(p)
		assert.True(t, os.IsNotExist(err), "record mode must not create the directory")
	})

	t.Run("remove_all", func(t *testing.T) {
		p := filepath.Join(dir, "keep.txt")
		require.NoError(t, os.WriteFile(p, []byte("x"), 0o644))
		require.NoError(t, FsRemoveAll(rec, p))
		_, err := os.Stat(p)
		assert.NoError(t, err, "record mode must not delete the file")
	})

	t.Run("temp_dir_returns_stub_without_creating", func(t *testing.T) {
		got, err := FsTempDir(rec, "pre-")
		require.NoError(t, err)
		assert.NotEmpty(t, got, "record mode temp dir must return a non-empty path")
		_, statErr := os.Stat(got)
		assert.True(t, os.IsNotExist(statErr), "record mode must not create the temp dir")
	})
}
