package vcs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The three hg-family sections share one config file and never touch the user's own
// lines or each other; each rewrite of a current section is a no-op.
func TestWriteHgFamilySections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hgrc")
	require.NoError(t, os.WriteFile(path, []byte("[ui]\nusername = me\n"), 0o644))

	for _, write := range []func() (bool, error){
		func() (bool, error) { return writeHgFamilyMergeDriverSection(path, []string{"gen/**", "dist/**"}) },
		func() (bool, error) { return writeHgFamilyRefreshSection(path, "magus job run sync-graph") },
		func() (bool, error) { return writeHgFamilyDriftSection(path, "magus job run check-drift") },
	} {
		changed, err := write()
		require.NoError(t, err)
		assert.True(t, changed, "the first write of each section changes the file")
	}

	want := "[ui]\nusername = me\n\n" +
		generatedMarkers.section("[merge-patterns]\n"+
			"glob:gen/** = magus\n"+
			"glob:dist/** = magus\n"+
			"\n[merge-tools]\n"+
			"magus.executable = magus\n"+
			"magus.args = vcs merge-driver $base $local $other 0 $local\n"+
			"magus.premerge = False\n"+
			"magus.gui = False\n") + "\n" +
		refreshMarkers.section("[hooks]\n"+
			"update.magus-refresh = magus job run sync-graph >/dev/null 2>&1 || true\n") + "\n" +
		driftMarkers.section("[hooks]\n"+
			"commit.magus-drift-notice = magus job run check-drift >/dev/null 2>&1 || true\n"+
			"outgoing.magus-drift-notice = magus job run check-drift >/dev/null 2>&1 || true\n")
	assertFile(t, path, want, 0o644)

	changed, err := writeHgFamilyRefreshSection(path, "magus job run sync-graph")
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = writeHgFamilyMergeDriverSection(path, []string{"gen/**"})
	require.NoError(t, err)
	assert.True(t, changed, "a dropped glob rewrites the section in place")
	present, err := managedSectionPresent(path, generatedMarkers)
	require.NoError(t, err)
	assert.True(t, present)
}
