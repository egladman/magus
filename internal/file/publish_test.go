package file

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The first publish wins; a second to the same path fails with fs.ErrExist, leaves the
// first file as it was, and leaves no temp behind.
func TestPublishRefusesATakenPath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	require.NoError(t, Publish(path, []byte("first"), 0o600))
	require.ErrorIs(t, Publish(path, []byte("second"), 0o600), fs.ErrExist)

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "first", string(raw))
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	require.Len(t, entries, 1, "no temp left behind")
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
}

// Publish does not create the directory, so a token directory's mode stays its owner's.
func TestPublishNeedsTheDirectory(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "absent", "token")
	require.ErrorIs(t, Publish(path, []byte("x"), 0o600), fs.ErrNotExist)
	assert.NoDirExists(t, filepath.Dir(path))
}

// A reader racing the publish finds no file or all of it, never an empty or partial one.
func TestPublishNeverShowsAnEmptyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	data := []byte("a token long enough that a partial write would show")
	for i := range 200 {
		path := filepath.Join(dir, fmt.Sprintf("token-%d", i))
		published := make(chan struct{})
		var reader sync.WaitGroup
		reader.Go(func() {
			for {
				raw, err := os.ReadFile(path)
				if err == nil {
					assert.Equal(t, string(data), string(raw))
					return
				}
				if !assert.ErrorIs(t, err, fs.ErrNotExist) {
					return
				}
				select {
				case <-published:
					return
				default:
				}
			}
		})
		require.NoError(t, Publish(path, data, 0o600))
		close(published)
		reader.Wait()
	}
}
