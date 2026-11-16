package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
)

func TestWriteSpell(t *testing.T) {
	t.Run("scaffolds a parseable, contract-complete spell", func(t *testing.T) {
		dir := t.TempDir()
		pkgDir, path, err := WriteSpell(t.Context(), dir, "acme", false)
		require.NoError(t, err)
		assert.Equal(t, filepath.Join(dir, "acme"), pkgDir)

		data, err := os.ReadFile(path)
		require.NoError(t, err, "expected spells/acme/spell.buzz")
		body := string(data)

		// The generated spell must parse (embedded mode, as the engine loads it).
		_, perr := buzz.ParseEmbedded(body)
		require.NoError(t, perr, "scaffolded spell must parse")

		for _, want := range []string{
			`export fun mgs_getName() > str { return "acme"; }`,
			"mgs_listRequiredGlobs",
			"mgs_listTargets",
			`import "magus/spell"`,
			`import "magus/charm"`,
			`test "build op forks the expected command"`,
		} {
			assert.Contains(t, body, want, "scaffold missing %q", want)
		}
	})

	t.Run("rejects an invalid handle", func(t *testing.T) {
		_, _, err := WriteSpell(t.Context(), t.TempDir(), "1bad name", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not a valid handle")
	})

	t.Run("refuses to overwrite without force", func(t *testing.T) {
		dir := t.TempDir()
		_, _, err := WriteSpell(t.Context(), dir, "acme", false)
		require.NoError(t, err)
		_, _, err = WriteSpell(t.Context(), dir, "acme", false)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
		_, _, err = WriteSpell(t.Context(), dir, "acme", true)
		require.NoError(t, err)
	})
}
