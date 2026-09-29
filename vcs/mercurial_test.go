package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/stamp"
	"github.com/egladman/magus/types"
)

// The three hg-family sections share one config file and never touch the user's own
// lines or each other; each rewrite of a current section is a no-op.
func TestWriteHgSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hgrc")
	require.NoError(t, os.WriteFile(path, []byte("[ui]\nusername = me\n"), 0o644))

	for _, write := range []func() (bool, error){
		func() (bool, error) {
			return writeHgMergeDriverSection(path, types.MergeDriverGlobs{Outputs: []string{"gen/**", "dist/**"}}, stamp.Judge{})
		},
		func() (bool, error) {
			return writeHgRefreshSection(path, "magus job run sync-graph", stamp.Judge{})
		},
		func() (bool, error) {
			return writeHgDriftSection(path, "magus job run check-drift", stamp.Judge{})
		},
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
			"magus.gui = False\n"+
			"magus.disabled = True\n") + "\n" +
		refreshMarkers.section("[hooks]\n"+
			"update.magus-refresh = magus job run sync-graph >/dev/null 2>&1 || true\n") + "\n" +
		driftMarkers.section("[hooks]\n"+
			"commit.magus-drift-notice = magus job run check-drift >/dev/null 2>&1 || true\n"+
			"outgoing.magus-drift-notice = magus job run check-drift >/dev/null 2>&1 || true\n")
	assertFile(t, path, want, 0o644)

	changed, err := writeHgRefreshSection(path, "magus job run sync-graph", stamp.Judge{})
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = writeHgMergeDriverSection(path, types.MergeDriverGlobs{Outputs: []string{"gen/**"}}, stamp.Judge{})
	require.NoError(t, err)
	assert.True(t, changed, "a dropped glob rewrites the section in place")
	present, err := managedSectionPresent(path, generatedMarkers)
	require.NoError(t, err)
	assert.True(t, present)
}

// An auto-resolve glob routes to the same tool as an output, once even when an output
// glob names it too, and a second write of the same globs changes nothing.
func TestWriteHgMergeDriverSectionRoutesAutoResolveGlobs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hgrc")
	globs := types.MergeDriverGlobs{Outputs: []string{"gen/**"}, AutoResolve: []string{"CHANGELOG.md", "gen/**", "docs/**/*.md"}}
	changed, err := writeHgMergeDriverSection(path, globs, stamp.Judge{})
	require.NoError(t, err)
	assert.True(t, changed)
	assertFile(t, path, generatedMarkers.section("[merge-patterns]\n"+
		"glob:CHANGELOG.md = magus\n"+
		"glob:gen/** = magus\n"+
		"glob:docs/**/*.md = magus\n"+
		"\n[merge-tools]\n"+
		"magus.executable = magus\n"+
		"magus.args = vcs merge-driver $base $local $other 0 $local\n"+
		"magus.premerge = False\n"+
		"magus.gui = False\n"+
		"magus.disabled = True\n"), 0o644)

	changed, err = writeHgMergeDriverSection(path, globs, stamp.Judge{})
	require.NoError(t, err)
	assert.False(t, changed, "installing twice is idempotent")
}

// TestHgMergeToolRoutesOnlyWhatOutputsClaim pins, on a real hg, that the tool gets only the
// declared outputs. A hand file among them (carved out) keeps Mercurial's :merge because
// merge-patterns takes its first match. And a source file must not reach magus through
// the fallback: with no ui.merge set, filemerge._picktool ranks every [merge-tools] entry
// by priority, and picked magus for every conflicted file until the tool was disabled.
func TestHgMergeToolRoutesOnlyWhatOutputsClaim(t *testing.T) {
	if _, err := exec.LookPath("hg"); err != nil {
		t.Skip("hg not available")
	}
	dir := t.TempDir()
	hgInitRepo(t, dir, map[string]string{"a.txt": "one\n", "gen/x.go": "x\n", "gen/runtime.go": "hand\n"})
	_, err := writeHgMergeDriverSection(filepath.Join(dir, ".hg", "hgrc"),
		types.MergeDriverGlobs{Outputs: []string{"gen/*.go"}, Carved: []string{"gen/runtime.go"}}, stamp.Judge{})
	require.NoError(t, err)
	// The tool has to be found for a pattern to pick it; the test binary stands in for magus.
	self, err := os.Executable()
	require.NoError(t, err)

	cmd := exec.CommandContext(t.Context(), "hg", "--config", "merge-tools.magus.executable="+self,
		"debugpickmergetool", "a.txt", "gen/runtime.go", "gen/x.go")
	cmd.Dir = dir
	// No user or system config, so no ui.merge a developer set can mask the fallback.
	cmd.Env = append(os.Environ(), "HGRCPATH=", "HGPLAIN=1")
	out, err := cmd.Output()
	require.NoError(t, err)

	assert.Equal(t, "a.txt = :merge\ngen/runtime.go = :merge\ngen/x.go = magus\n", string(out))
}
