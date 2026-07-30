package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	// Importing the host package triggers all host/*.go init() calls, which
	// register every module (Sh, Fs, Vcs, …) with std.Register.
	"github.com/egladman/magus/std"
)

func TestWriteFileIfChanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "generated.go")
	stamp := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.WriteFile(path, []byte("current"), 0o644))
	require.NoError(t, os.Chtimes(path, stamp, stamp))

	require.NoError(t, writeFileIfChanged(path, []byte("current"), 0o644))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.True(t, stamp.Equal(info.ModTime()))

	require.NoError(t, writeFileIfChanged(path, []byte("updated"), 0o644))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "updated", string(got))
}

// buzzModules lists modules with generated Buzz host bindings.
var buzzModules = []string{
	"os", "vcs", "fs", "archive", "env", "json", "http", "charm",
}

// TestBuzzFilesUpToDate verifies that the checked-in host/gen/<module>.go files
// are byte-for-byte identical to what `magus-utils bindings -lang buzz` would emit today.
func TestBuzzFilesUpToDate(t *testing.T) {
	genBuzzDir := filepath.Join("..", "..", "host", "gen")

	for _, name := range buzzModules {
		t.Run(name, func(t *testing.T) {
			m, ok := std.Get(name)
			require.True(t, ok, "std.Get(%q): module not registered", name)
			want, err := emitBuzz(m)
			require.NoError(t, err, "emitBuzz(%q)", name)
			outPath := filepath.Join(genBuzzDir, name+".go")
			got, err := os.ReadFile(outPath)
			require.NoError(t, err, "read %s", outPath)
			assert.Equal(t, string(want), string(got),
				"%s.go is out of date; re-run:\n  go generate ./std/", name)
		})
	}
}
