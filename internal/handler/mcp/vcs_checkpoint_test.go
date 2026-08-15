package mcp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkpointRepo makes a throwaway git repo with one committed file and returns its dir.
// GIT_CONFIG_GLOBAL/SYSTEM are neutered so a developer's own hooks path or hardened
// protocol settings cannot reach into the fixture, matching the vcs package's harness.
func checkpointRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
			"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.CombinedOutput()
		require.NoErrorf(t, err, "git %v: %s", args, out)
	}
	run("init", "-q")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha\n"), 0o644))
	run("add", "-A")
	run("commit", "-q", "-m", "init")
	run("checkout", "-q", "-b", "work")
	return dir
}

func TestVCSCheckpointTool(t *testing.T) {
	// Pinned rather than autodetected: a developer's MAGUS_VCS_* would otherwise decide
	// what this test resolves, and MAGUS_VCS_ENABLED=false would leave a nil driver and
	// turn a real assertion into an error path.
	t.Setenv("MAGUS_VCS_NAME", "git")
	t.Setenv("MAGUS_VCS_ENABLED", "true")

	dir := checkpointRepo(t)
	tool := &vcsCheckpointTool{ws: &fakeWorkspace{root: dir}}

	t.Run("clean tree", func(t *testing.T) {
		resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{})
		require.NoError(t, err)
		got, ok := resp.Data.(types.VCSCheckpoint)
		require.True(t, ok, "Data is the record itself, so the CLI and the tool cannot disagree")
		assert.NotEmpty(t, got.Revision)
		assert.Equal(t, "work", got.Branch)
		assert.False(t, got.Dirty)
		assert.Empty(t, got.PatchDigest)
		assert.Equal(t, "git", got.VCS)
	})

	t.Run("dirty tree carries a digest", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "a.txt"), []byte("alpha changed\n"), 0o644))
		resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{})
		require.NoError(t, err)
		got := resp.Data.(types.VCSCheckpoint)
		assert.True(t, got.Dirty)
		assert.Len(t, got.PatchDigest, 16)
	})

	t.Run("parameters are ignored, not rejected", func(t *testing.T) {
		// The tool declares none, and a client that sends one anyway must still get an
		// answer: a checkpoint has nothing to narrow, so there is no argument to honor
		// and no reason to fail over one.
		resp, err := tool.Invoke(context.Background(), spells.InvokeRequest{Params: map[string]any{"paths": "a.txt"}})
		require.NoError(t, err)
		assert.NotEmpty(t, resp.Data.(types.VCSCheckpoint).Revision)
	})

	t.Run("a directory under no VCS reports the failure", func(t *testing.T) {
		bare := &vcsCheckpointTool{ws: &fakeWorkspace{root: t.TempDir()}}
		_, err := bare.Invoke(context.Background(), spells.InvokeRequest{})
		assert.Error(t, err, "no repository means no revision to record, not an empty one")
	})
}
