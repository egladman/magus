package ward

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func spell(t *testing.T, root, rel string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spell.buzz"), []byte("export fun mgs_getName() > str { return \"x\"; }\n"), 0o644))
}

func TestSpellShadowsWard(t *testing.T) {
	t.Run("unacknowledged shadow yields a coded MGS1002 diagnostic", func(t *testing.T) {
		root := t.TempDir()
		spell(t, root, "spells/hello")
		spell(t, root, "web/spells/hello")
		diags, err := SpellShadows(root, nil)
		require.NoError(t, err)
		require.Len(t, diags, 1)
		assert.ErrorIs(t, diags[0], types.ErrDiag)
		assert.Equal(t, types.SpellShadowed, diags[0].Code)
		assert.Contains(t, diags[0].Msg, "spells/hello")
	})

	t.Run("acknowledged shadow is silent", func(t *testing.T) {
		root := t.TempDir()
		spell(t, root, "spells/hello")
		spell(t, root, "web/spells/hello")
		diags, err := SpellShadows(root, func(imp string) bool { return imp == "spells/hello" })
		require.NoError(t, err)
		assert.Empty(t, diags)
	})

	t.Run("no shadow, no diagnostic", func(t *testing.T) {
		root := t.TempDir()
		spell(t, root, "spells/hello")
		spell(t, root, "web/spells/world")
		diags, err := SpellShadows(root, nil)
		require.NoError(t, err)
		assert.Empty(t, diags)
	})
}
