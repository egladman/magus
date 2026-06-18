package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteMagusfileStub(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, writeMagusfileStub(dir))
	data, err := os.ReadFile(filepath.Join(dir, "magusfile.buzz"))
	require.NoError(t, err, "expected magusfile.buzz")
	body := string(data)
	for _, want := range []string{
		`import "magus"`,
		"magus.project.register(",
		`export fun preflight`,
		`export fun test`,
	} {
		assert.Contains(t, body, want, "magusfile.buzz missing %q", want)
	}
}

func TestMagusfilePresent(t *testing.T) {
	for _, name := range []string{"magusfile.buzz"} {
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644))
		assert.True(t, magusfilePresent(dir), "magusfilePresent should detect %s", name)
	}
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "magusfiles"), 0o755))
	assert.True(t, magusfilePresent(dir), "magusfilePresent should detect magusfiles/ directory")
	assert.False(t, magusfilePresent(t.TempDir()), "magusfilePresent should be false for an empty directory")
}

// An existing magusfile must not be clobbered by a stub write.
func TestWriteMagusfileStubSkipsExisting(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(existing, []byte("// mine\n"), 0o644))
	require.NoError(t, writeMagusfileStub(dir))
	data, _ := os.ReadFile(existing)
	assert.Equal(t, "// mine\n", string(data), "existing magusfile.buzz was modified")
}
