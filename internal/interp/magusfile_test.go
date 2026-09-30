package interp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportProbesIsFileMatchesStat(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	j := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	for _, rel := range []string{"a.buzz", "d/b.buzz", "d/e/spell.buzz", "f", "café.buzz"} {
		require.NoError(t, os.MkdirAll(filepath.Dir(j(rel)), 0o755))
		require.NoError(t, os.WriteFile(j(rel), []byte("x"), 0o644))
	}
	require.NoError(t, os.Symlink(j("a.buzz"), j("link.buzz")))
	require.NoError(t, os.Symlink(j("d"), j("ld")))
	require.NoError(t, os.Symlink(j("gone.buzz"), j("dangling.buzz")))

	paths := []string{
		"a.buzz", "d/b.buzz", "d/e/spell.buzz", "d", "d/e", "f", "café.buzz",
		"link.buzz", "ld/b.buzz", "ld", "dangling.buzz",
		"missing.buzz", "m/x.buzz", "m/n/o/spell.buzz", "f/x.buzz", "d/missing/spell.buzz",
		"A.buzz", "D/B.buzz", "CAFÉ.buzz", "../outside.buzz",
	}
	ctx, seal := WithImportProbes(t.Context())
	defer seal()
	probes := ImportProbesFromContext(ctx)
	for _, rel := range paths {
		p := j(rel)
		assert.Equal(t, statIsFile(p), probes.IsFile(p), rel)
		assert.Equal(t, statIsFile(p), probes.IsFile(p), rel+" (cached)")
	}
	rel := filepath.Join("d", "b.buzz")
	assert.Equal(t, statIsFile(rel), probes.IsFile(rel), "relative path")
}

func TestImportProbesSealedStatsAgain(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "late.buzz")
	ctx, seal := WithImportProbes(t.Context())
	probes := ImportProbesFromContext(ctx)
	probes.MarkNotSpell(path)
	require.False(t, probes.IsFile(path))

	seal()
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
	assert.True(t, probes.IsFile(path))
	assert.False(t, probes.NotSpell(path))
}

func TestImportProbesNotSpellIsPerLoad(t *testing.T) {
	var none *ImportProbes
	none.MarkNotSpell("/x.buzz")
	assert.False(t, none.NotSpell("/x.buzz"))
	assert.Nil(t, ImportProbesFromContext(t.Context()))

	ctx, seal := WithImportProbes(t.Context())
	defer seal()
	probes := ImportProbesFromContext(ctx)
	probes.MarkNotSpell("/x.buzz")
	assert.True(t, probes.NotSpell("/x.buzz"))
	assert.False(t, probes.NotSpell("/y.buzz"))
}

func TestImportProbesSpellAtIsPerLoadAndAbsolute(t *testing.T) {
	var none *ImportProbes
	none.RecordSpellAt("/s/spell.buzz", &spells.Descriptor{Name: "s"})
	_, known := none.SpellAt("/s/spell.buzz")
	assert.False(t, known)

	ctx, seal := WithImportProbes(t.Context())
	probes := ImportProbesFromContext(ctx)
	spec := &spells.Descriptor{Name: "s"}
	probes.RecordSpellAt("/s/spell.buzz", spec)
	probes.RecordSpellAt("/none.buzz", nil)
	probes.RecordSpellAt(filepath.Join("rel", "spell.buzz"), spec)

	got, known := probes.SpellAt("/s/spell.buzz")
	assert.True(t, known)
	assert.Same(t, spec, got)
	got, known = probes.SpellAt("/none.buzz")
	assert.True(t, known)
	assert.Nil(t, got)
	_, known = probes.SpellAt(filepath.Join("rel", "spell.buzz"))
	assert.False(t, known, "a relative candidate names a different file per project")

	seal()
	_, known = probes.SpellAt("/s/spell.buzz")
	assert.False(t, known)
}
