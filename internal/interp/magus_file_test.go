package interp

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportProbesIsFileMatchesStat(t *testing.T) {
	w := testkit.NewWorkspace(t)
	root := w.Root()
	j := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	for _, rel := range []string{"a.buzz", "d/b.buzz", "d/e/spell.buzz", "f", "café.buzz"} {
		w.Write(rel, "x")
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

// mark records path as a plain module the way a read does: from a stat of the file.
func mark(t *testing.T, probes *ImportProbes, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	require.NoError(t, err)
	probes.MarkNotSpell(path, fi.Size(), fi.ModTime())
}

func TestImportProbesSealedStatsAgain(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "late.buzz")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
	ctx, seal := WithImportProbes(t.Context())
	probes := ImportProbesFromContext(ctx)
	mark(t, probes, path)
	require.True(t, probes.NotSpell(path))

	seal()
	assert.False(t, probes.NotSpell(path))
}

func TestImportProbesNotSpellIsPerLoadOrRun(t *testing.T) {
	root := t.TempDir()
	x, y := filepath.Join(root, "x.buzz"), filepath.Join(root, "y.buzz")
	require.NoError(t, os.WriteFile(x, []byte("x"), 0o644))
	require.NoError(t, os.WriteFile(y, []byte("y"), 0o644))
	var none *ImportProbes
	none.MarkNotSpell(x, 1, time.Time{})
	assert.False(t, none.NotSpell(x))
	assert.Nil(t, ImportProbesFromContext(t.Context()))

	ctx, seal := WithImportProbes(t.Context())
	defer seal()
	probes := ImportProbesFromContext(ctx)
	mark(t, probes, x)
	assert.True(t, probes.NotSpell(x))
	assert.False(t, probes.NotSpell(y))
}

func TestImportProbesNotSpellMissesOnAFileChangedSinceTheMark(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.buzz")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
	ctx, seal := WithImportProbes(t.Context())
	defer seal()
	probes := ImportProbesFromContext(ctx)
	mark(t, probes, path)
	require.True(t, probes.NotSpell(path))

	require.NoError(t, os.WriteFile(path, []byte("xy"), 0o644))
	assert.False(t, probes.NotSpell(path), "a file of another size stayed marked")

	mark(t, probes, path)
	require.True(t, probes.NotSpell(path))
	later := time.Now().Add(time.Hour)
	require.NoError(t, os.Chtimes(path, later, later))
	assert.False(t, probes.NotSpell(path), "a file of another modification time stayed marked")

	mark(t, probes, path)
	require.NoError(t, os.Remove(path))
	assert.False(t, probes.NotSpell(path), "a deleted file stayed marked")
}

func TestImportProbesSpellAtIsPerLoadOrRunAndAbsolute(t *testing.T) {
	var none *ImportProbes
	none.RecordSpellAt("/s/spell.buzz", "/w", "src", spells.Descriptor{Name: "s"})
	_, _, known := none.SpellAt("/s/spell.buzz", "/w", "src")
	assert.False(t, known)

	ctx, seal := WithImportProbes(t.Context())
	probes := ImportProbesFromContext(ctx)
	spec := spells.Descriptor{Name: "s"}
	probes.RecordSpellAt("/s/spell.buzz", "/w", "src", spec)
	probes.RecordSpellAt(filepath.Join("rel", "spell.buzz"), "/w", "src", spec)

	got, notSpell, known := probes.SpellAt("/s/spell.buzz", "/w", "src")
	assert.True(t, known)
	assert.False(t, notSpell)
	assert.Equal(t, spec, got)
	_, _, known = probes.SpellAt(filepath.Join("rel", "spell.buzz"), "/w", "src")
	assert.False(t, known, "a relative candidate names a different file per project")

	seal()
	_, _, known = probes.SpellAt("/s/spell.buzz", "/w", "src")
	assert.False(t, known)
}

func TestImportProbesSpellAtMissesOnOtherBytesOrDirectory(t *testing.T) {
	ctx, seal := WithImportProbes(t.Context())
	defer seal()
	probes := ImportProbesFromContext(ctx)
	probes.RecordSpellAt("/s/spell.buzz", "/a", "v1", spells.Descriptor{Name: "s"})

	_, _, sameBytes := probes.SpellAt("/s/spell.buzz", "/a", "v1")
	_, _, otherBytes := probes.SpellAt("/s/spell.buzz", "/a", "v2")
	_, _, otherDir := probes.SpellAt("/s/spell.buzz", "/b", "v1")

	assert.True(t, sameBytes)
	assert.False(t, otherBytes)
	assert.False(t, otherDir)
}

func TestImportProbesSpellAtHandsBackANotSpellRecord(t *testing.T) {
	ctx, seal := WithImportProbes(t.Context())
	defer seal()
	probes := ImportProbesFromContext(ctx)
	probes.RecordNotSpellAt("/s/mention.buzz", "/a", "v1")

	_, notSpell, known := probes.SpellAt("/s/mention.buzz", "/a", "v1")
	assert.True(t, known)
	assert.True(t, notSpell)
}
