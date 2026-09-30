package settle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkspaceLoadWritesNoHook: the load-time refresh registers the driver and leaves
// the hooks dir byte for byte as it was. The hooks dir is shared by every worktree of
// the repository, and only `magus init --vcs git` writes into it.
func TestWorkspaceLoadWritesNoHook(t *testing.T) {
	ctx := context.Background()
	root := initGitRepo(t)
	magusfile := `import "magus";
import "fs";

magus.project({})

export fun generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles("gen/**");
    fs\writeFile("gen/catalog.md", "regenerated\n");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gen"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "gen", "catalog.md"), []byte("kept\n"), 0o644))
	runGit(t, root, "add", "-A")
	runGit(t, root, "commit", "-m", "initial")
	m, err := magus.Open(ctx, root)
	require.NoError(t, err)

	hooks := filepath.Join(root, ".git", "hooks")
	before := snapshotTree(t, hooks)
	EnsureDriver(ctx, m)

	registered, err := resolveGitDriver(t, root).MergeDriverCommand(ctx, root)
	require.NoError(t, err)
	assert.Contains(t, registered, "vcs merge-driver", "the refresh did register the driver")
	assert.Equal(t, before, snapshotTree(t, hooks))
	missing, err := vcs.SettleHooksMissing(ctx, root)
	require.NoError(t, err)
	assert.Equal(t, vcs.SettleHooks, missing)
}

func resolveGitDriver(t *testing.T, dir string) types.VCSDriver {
	t.Helper()
	res, err := vcs.Resolve(context.Background(), dir, "", types.VCSOptions{})
	require.NoError(t, err)
	require.NotNil(t, res.VCS)
	require.Equal(t, "git", res.Name)
	return res.VCS
}
