package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "man1")
	require.NoError(t, manCmd([]string{"install", "--dir", dir}))
	for _, name := range []string{"magus.1", "magus-man.1", "magus-run.1"} {
		content, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		assert.NotEmpty(t, content)
	}
}

func TestManInstallRejectsUnknownInput(t *testing.T) {
	assert.ErrorContains(t, manCmd([]string{"remove"}), `unknown subcommand "remove"`)
	assert.ErrorContains(t, manCmd([]string{"install", "extra"}), `unexpected argument "extra"`)
}

func TestDefaultManDir(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/magus-data")
	assert.Equal(t, "/tmp/magus-data/man/man1", defaultManDir())
}

func TestManUsage(t *testing.T) {
	previous := os.Stderr
	read, write, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = write
	t.Cleanup(func() { os.Stderr = previous })

	assert.Error(t, manCmd(nil))
	require.NoError(t, write.Close())
	content, err := io.ReadAll(read)
	require.NoError(t, err)
	assert.Contains(t, string(content), "Usage: magus man install")
}
