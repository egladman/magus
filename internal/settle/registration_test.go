package settle

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const driverTestMagusfile = `import "magus";
import "fs";

magus\project({})

export fun generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles("gen/**");
    fs\writeFile("gen/catalog.md", "regenerated\n");
}
`

// TestEnsureDriverNeverFirstInstalls: with no registration, EnsureDriver writes neither
// .gitattributes nor git config nor a hook. First install is `magus init --vcs`'s alone.
func TestEnsureDriverNeverFirstInstalls(t *testing.T) {
	ctx := context.Background()
	root := testkit.NewRepo(t, map[string]string{
		"magusfile.buzz": driverTestMagusfile,
		"gen/catalog.md": "kept\n",
	}).Root()
	m, err := magus.Open(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err)

	hooks := filepath.Join(root, ".git", "hooks")
	before := snapshotTree(t, hooks)
	EnsureDriver(ctx, m)

	registered, err := resolveGitDriver(t, root).MergeDriverCommand(ctx, root)
	require.NoError(t, err)
	assert.Empty(t, registered, "no registration may appear without init")
	assert.NoFileExists(t, filepath.Join(root, ".gitattributes"))
	assert.Equal(t, before, snapshotTree(t, hooks))
}

// TestEnsureDriverRefreshesAnExistingRegistration: a registration whose globs went stale
// follows the declared outputs, and the hooks dir stays byte for byte as it was. The hooks
// dir is shared by every worktree of the repository, and only `magus init --vcs git`
// writes into it.
func TestEnsureDriverRefreshesAnExistingRegistration(t *testing.T) {
	ctx := context.Background()
	root := testkit.NewRepo(t, map[string]string{
		"magusfile.buzz": driverTestMagusfile,
		"gen/catalog.md": "kept\n",
	}).Root()
	m, err := magus.Open(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err)

	installer, ok := vcs.Installer("git")
	require.True(t, ok)
	_, err = installer.EnsureMergeDriver(ctx, root, types.MergeDriverGlobs{Outputs: []string{"stale/**"}})
	require.NoError(t, err)

	hooks := filepath.Join(root, ".git", "hooks")
	before := snapshotTree(t, hooks)
	EnsureDriver(ctx, m)

	attributes, err := os.ReadFile(filepath.Join(root, ".gitattributes"))
	require.NoError(t, err)
	assert.Contains(t, string(attributes), "gen/**")
	assert.NotContains(t, string(attributes), "stale/**")
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
