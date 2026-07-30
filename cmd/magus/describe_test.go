package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
)

// TestDescribeWorkspacesOutput_MultiDeclared verifies that `describe workspaces`
// enumerates every declared daemon workspace, not just the active one.
func TestDescribeWorkspacesOutput_MultiDeclared(t *testing.T) {
	base := t.TempDir()
	mkWorkspace := func(name string) string {
		dir := filepath.Join(base, name)
		require.NoError(t, os.MkdirAll(dir, 0o755))
		// An empty magusfile.buzz marks the directory as a workspace root.
		require.NoError(t, os.WriteFile(filepath.Join(dir, "magusfile.buzz"), nil, 0o644))
		return dir
	}
	wsA, wsB := mkWorkspace("a"), mkWorkspace("b")

	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })
	globalCfg = config.Config{}
	globalCfg.Daemon.Workspaces = []string{wsA, wsB}

	out, err := describeWorkspacesOutput(context.Background(), "")
	require.NoError(t, err)
	require.Equal(t, 2, out.Count)
	require.Len(t, out.Workspaces, 2)
	assert.NotEqual(t, out.Workspaces[1].Root, out.Workspaces[0].Root, "expected two distinct workspace roots")
}
