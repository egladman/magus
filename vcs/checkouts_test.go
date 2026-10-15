package vcs

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// headRev names the commit init made, in each backend's own spelling: jj's init leaves an
// empty working-copy commit on top of it.
var headRev = map[string]string{"git": "HEAD", "hg": ".", "sl": ".", "jj": "@-"}

// commitIn records a new file in the checkout at dir on no branch or bookmark. jj needs no
// command: its working copy is the commit.
func commitIn(t *testing.T, b parityBackend, dir string) {
	t.Helper()
	writeRepoFile(t, dir, "b.txt", "two\n")
	switch b.name {
	case "git":
		vcsTestRun(t, dir, "git", "add", "b.txt")
		vcsTestRun(t, dir, "git", "-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "b")
	case "hg":
		vcsTestRun(t, dir, "hg", "add", "b.txt")
		vcsTestRun(t, dir, "hg", "commit", "-m", "b", "-u", "test")
	}
}

func TestCheckoutLifecycleOnEveryBackend(t *testing.T) {
	eachBackend(t, func(t *testing.T, b parityBackend) {
		ctx := t.Context()
		root := t.TempDir()
		b.init(t, root, map[string]string{"a.txt": "one\n"})
		base, err := b.drv.FindCommit(ctx, root, headRev[b.name])
		require.NoError(t, err)
		dir := filepath.Join(t.TempDir(), "job")

		err = b.drv.CreateCheckout(ctx, root, dir, base.ID)
		if b.name == "sl" {
			var declined *types.VCSUnsupportedError
			require.ErrorAs(t, err, &declined, "Sapling has no secondary checkout, so it must decline in its own name")
			assert.Equal(t, "sl", declined.VCS)
			return
		}
		require.NoError(t, err)
		require.FileExists(t, filepath.Join(dir, "a.txt"), "the checkout holds the revision it was made at")
		listed, err := b.drv.Checkouts(ctx, root)
		require.NoError(t, err)
		assert.Equal(t, []string{realPath(dir)}, listed)

		stranded, err := b.drv.StrandedRevisions(ctx, dir, base.ID)
		require.NoError(t, err)
		assert.Empty(t, stranded, "a fresh checkout at the base strands nothing")

		commitIn(t, b, dir)
		stranded, err = b.drv.StrandedRevisions(ctx, dir, base.ID)
		require.NoError(t, err)
		assert.Len(t, stranded, 1, "a commit on no branch or bookmark is stranded by removing its checkout")

		require.Error(t, b.drv.RemoveCheckout(ctx, root, filepath.Join(t.TempDir(), "foreign")), "a checkout it did not make is refused")
		require.NoError(t, b.drv.RemoveCheckout(ctx, root, dir))
		assert.NoDirExists(t, dir)
		listed, err = b.drv.Checkouts(ctx, root)
		require.NoError(t, err)
		assert.Empty(t, listed)
	})
}
