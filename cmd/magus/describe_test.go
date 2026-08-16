package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
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
	require.Len(t, out, 2)
	assert.NotEqual(t, out[1].Root, out[0].Root, "expected two distinct workspace roots")
}

// TestClaimLabel pins the one spelling `describe file` prints a claim's declarer
// as: the path:target ref every other magus command accepts, so a reader can run
// the target that wrote the file without translating anything.
func TestClaimLabel(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		claim types.FileClaim
		want  string
	}{
		{name: "per-target glob", claim: types.FileClaim{Project: "docs", Target: "generate"}, want: "docs:generate"},
		{name: "project-wide glob", claim: types.FileClaim{Project: "docs"}, want: "docs"},
		{name: "root project", claim: types.FileClaim{Project: ".", Target: "generate"}, want: ".:generate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, claimLabel(tc.claim))
		})
	}
}
