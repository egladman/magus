package bindings

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/interp"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestRootFirstLevels pins the walk-up spell-search chain: root-first from the
// workspace root down to the importing file's dir, bounded to the workspace.
func TestRootFirstLevels(t *testing.T) {
	j := filepath.Join
	root := j("/", "w")
	t.Run("nested project walks root down to the file dir", func(t *testing.T) {
		got := rootFirstLevels(root, j(root, "web", "studio"))
		assert.Equal(t, []string{root, j(root, "web"), j(root, "web", "studio")}, got)
	})
	t.Run("file at the root yields just the root", func(t *testing.T) {
		assert.Equal(t, []string{root}, rootFirstLevels(root, root))
	})
	t.Run("no workspace root yields just the file dir (out-of-workspace script)", func(t *testing.T) {
		assert.Equal(t, []string{j(root, "web")}, rootFirstLevels("", j(root, "web")))
	})
	t.Run("file outside the root is not walked above itself (hermetic)", func(t *testing.T) {
		assert.Equal(t, []string{j("/", "other", "x")}, rootFirstLevels(root, j("/", "other", "x")))
	})
}

// A workspace bounds the spell search, so `magus --root` from another checkout
// never falls back to that checkout's spells/.
func TestSpellSearchLevelsFallBackToCwdOnlyWithoutWorkspace(t *testing.T) {
	root := filepath.Join("/", "w")
	src := &interp.Source{Dir: filepath.Join(root, "web")}
	ctx := interp.WithSource(context.Background(), src)

	assert.Equal(t, []string{src.Dir, ""}, spellSearchLevels(ctx))
	assert.Equal(t, []string{root, src.Dir}, spellSearchLevels(types.WithWorkspace(ctx, rootOnlyWS{root: root})))
}
