package guard

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/trail"
)

// A verdict a hook stores from a linked worktree is stored in the primary checkout too,
// which `magus query output` searches from every checkout of the repository.
func TestStoreVerdictReachesThePrimaryCheckout(t *testing.T) {
	r := newWorktreeRepo(t)
	session := r.add("session")
	cacheDir := filepath.Join(session, ".magus")

	primary, ok := primaryCacheDir(cacheDir)
	require.True(t, ok)
	assert.Equal(t, filepath.Join(realPath(r.main), ".magus"), primary)

	ref := storeVerdict(t.Context(), cacheDir, "denied: a verdict")
	require.NotEmpty(t, ref)
	for _, dir := range []string{cacheDir, primary} {
		got, err := trail.ReadBlob(dir, ref)
		require.NoError(t, err, dir)
		assert.Equal(t, "denied: a verdict", string(got))
	}

	_, ok = primaryCacheDir(filepath.Join(r.main, ".magus"))
	assert.False(t, ok, "the primary checkout is its own")
	_, ok = primaryCacheDir(t.TempDir())
	assert.False(t, ok, "a cache dir in no checkout")
}
