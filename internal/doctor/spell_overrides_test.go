package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	remotespell "github.com/egladman/magus/internal/spell/remote"
)

func TestSpellOverrideFindings(t *testing.T) {
	root := t.TempDir()
	eject := func(rel string) string {
		dir := filepath.Join(root, filepath.FromSlash(rel))
		_, err := remotespell.Eject("go", "golang", dir)
		require.NoError(t, err)
		return dir
	}
	shipped, _, err := remotespell.Shipped("go", "golang")
	require.NoError(t, err)
	stamp := "// magus:origin " + shipped.String() + "\n"
	cfg := func(path string) config.SpellsConfig {
		return config.SpellsConfig{Imports: map[string]config.SpellImport{
			"magus/spell/go":         {Path: path},
			"magus/spell/nope":       {Path: "spells/nope"}, // MGS1044 at load, not doctor's to report
			"ghcr.io/team/spells/x":  {Tag: "v1"},
			"ghcr.io/team/spells/y":  {Path: "vendor/y"}, // replaces a remote spell, not a built-in
			"magus/spell/typescript": {Tag: "v1"},        // not an override
		}}
	}

	t.Run("edited", func(t *testing.T) {
		dir := eject("edited/go")
		require.NoError(t, os.WriteFile(filepath.Join(dir, "extra.buzz"), nil, 0o644))
		details, advice := spellOverrideFindings(root, cfg("edited/go"))
		assert.Equal(t, []string{"edited/go replaces magus/spell/go (pulled from this binary's copy)"}, details)
		assert.Zero(t, advice)
	})

	t.Run("unedited", func(t *testing.T) {
		eject("same/go")
		details, advice := spellOverrideFindings(root, cfg("same/go"))
		assert.Equal(t, []string{"same/go is identical to the built-in magus/spell/go; drop the override"}, details)
		assert.Equal(t, 1, advice)
	})

	t.Run("built-in changed since", func(t *testing.T) {
		dir := eject("old/go")
		p := filepath.Join(dir, "spell.buzz")
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		older := digest.FromString("an older release").String()
		require.NoError(t, os.WriteFile(p, append([]byte("// magus:origin ghcr.io/egladman/magus/spells/go@"+older+"\n"), body[len(stamp):]...), 0o644))
		details, advice := spellOverrideFindings(root, cfg("old/go"))
		assert.Equal(t, []string{"magus/spell/go changed since old/go was pulled (" + older[:len("sha256:")+12] + " -> " + shipped.Digest.String()[:len("sha256:")+12] +
			"); `magus spell pull magus/spell/go <empty dir>` writes the current copy to diff against"}, details)
		assert.Equal(t, 1, advice)
	})

	t.Run("no stamp", func(t *testing.T) {
		dir := eject("bare/go")
		p := filepath.Join(dir, "spell.buzz")
		body, err := os.ReadFile(p)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, body[len(stamp):], 0o644))
		details, advice := spellOverrideFindings(root, cfg("bare/go"))
		assert.Equal(t, []string{"bare/go replaces magus/spell/go with no origin stamp, so what it started from is unknown; " +
			"`magus spell pull magus/spell/go <empty dir>` writes the current copy to diff against"}, details)
		assert.Equal(t, 1, advice)
	})

	t.Run("no override", func(t *testing.T) {
		details, advice := spellOverrideFindings(root, config.SpellsConfig{})
		assert.Empty(t, details)
		assert.Zero(t, advice)
	})
}
