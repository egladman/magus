package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A workspace with no magus.yaml is a workspace that never asked for anything, so
// defaults are the right answer and no error.
func TestLoadWorkspaceConfigFallsBackWhenTheFileIsAbsent(t *testing.T) {
	t.Parallel()

	cfg, err := loadWorkspaceConfig(t.TempDir())
	require.NoError(t, err)
	assert.False(t, cfg.Sandbox.Enabled, "defaults do not enable the sandbox")
}

// The failure this exists to prevent: a magus.yaml that asked for sandboxing but does not
// parse used to collapse into Defaults(), which disables it - so the workspace joined the
// daemon's union unsandboxed and nothing said so.
func TestLoadWorkspaceConfigRefusesAMalformedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("sandbox:\n  enabled: [true\n"), 0o644))

	_, err := loadWorkspaceConfig(root)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "magus.yaml")
}

func TestApplyUnionSandboxReportsAMalformedWorkspaceConfig(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte("sandbox:\n  enabled: [true\n"), 0o644))

	assert.Error(t, ApplyUnionSandbox(context.Background(), []string{root}))
}
