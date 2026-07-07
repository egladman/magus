package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeSpell(t *testing.T, root, rel string) {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "spell.buzz"), []byte("export fun mgs_getName() > str { return \"x\"; }\n"), 0o644))
}

func TestSpellShadows(t *testing.T) {
	t.Run("ancestor and descendant define the same name: shadow", func(t *testing.T) {
		root := t.TempDir()
		writeSpell(t, root, "spells/hello")            // workspace root
		writeSpell(t, root, "web/studio/spells/hello") // nested project
		got, err := SpellShadows(root)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "spells/hello", got[0].Import)
		assert.Contains(t, got[0].Winner, filepath.Join(root, "spells", "hello"))
		assert.Contains(t, got[0].Shadowed, filepath.Join(root, "web", "studio", "spells", "hello"))
	})

	t.Run("sibling subtrees reusing a name are NOT a shadow", func(t *testing.T) {
		root := t.TempDir()
		writeSpell(t, root, "web/spells/hello")
		writeSpell(t, root, "api/spells/hello")
		got, err := SpellShadows(root)
		require.NoError(t, err)
		assert.Empty(t, got, "no ancestor-descendant relation, so no shadow")
	})

	t.Run("three levels: both deeper levels are shadowed by the root-most", func(t *testing.T) {
		root := t.TempDir()
		writeSpell(t, root, "spells/hello")
		writeSpell(t, root, "web/spells/hello")
		writeSpell(t, root, "web/studio/spells/hello")
		got, err := SpellShadows(root)
		require.NoError(t, err)
		require.Len(t, got, 2)
		for _, c := range got {
			assert.Contains(t, c.Winner, filepath.Join(root, "spells", "hello"), "root-most always wins")
		}
	})

	t.Run("distinct names never shadow", func(t *testing.T) {
		root := t.TempDir()
		writeSpell(t, root, "spells/alpha")
		writeSpell(t, root, "web/spells/beta")
		got, err := SpellShadows(root)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
