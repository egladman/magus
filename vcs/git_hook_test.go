package vcs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteManagedHookNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-checkout")
	changed, err := writeManagedHook(path, gitHookBody("post-checkout", "magus server sync"))
	require.NoError(t, err)
	assert.True(t, changed)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(body)
	assert.True(t, strings.HasPrefix(s, "#!/bin/sh"), "a new hook gets a shebang")
	assert.Contains(t, s, gitHookBegin)
	assert.Contains(t, s, "magus server sync")
	assert.Contains(t, s, `[ "$3" = "1" ]`, "post-checkout guards on the branch-checkout flag")

	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&0o100, "the hook is executable")
}

func TestWriteManagedHookIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-merge")
	_, err := writeManagedHook(path, gitHookBody("post-merge", "magus server sync"))
	require.NoError(t, err)

	changed, err := writeManagedHook(path, gitHookBody("post-merge", "magus server sync"))
	require.NoError(t, err)
	assert.False(t, changed, "re-installing an unchanged section is a no-op")
}

func TestWriteManagedHookPreservesUserContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "post-rewrite")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\necho 'my own hook'\n"), 0o755))

	changed, err := writeManagedHook(path, gitHookBody("post-rewrite", "magus server sync"))
	require.NoError(t, err)
	assert.True(t, changed)

	body, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(body)
	assert.Contains(t, s, "echo 'my own hook'", "the user's hook body is preserved")
	assert.Contains(t, s, gitHookBegin, "the managed section is appended")
}
