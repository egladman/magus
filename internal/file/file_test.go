package file

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteFileAtomic_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")
	data := []byte("hello atomic")

	require.NoError(t, WriteFileAtomic(path, data, 0o644))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(data), string(got))
}

func TestWriteFileAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "output.txt")

	require.NoError(t, WriteFileAtomic(path, []byte("old"), 0o644))
	require.NoError(t, WriteFileAtomic(path, []byte("new"), 0o644))

	got, _ := os.ReadFile(path)
	assert.Equal(t, "new", string(got))
}
