package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFragment writes one fragment file into dir for tests.
func writeFragment(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

func TestParseFragment(t *testing.T) {
	got, err := parseFragment("f.md", []byte("### Removed\n\n- **Breaking: the `exclusive` option.** Delete\n  the key.\n\n"))
	require.NoError(t, err)
	assert.Equal(t, fragment{
		path:    "f.md",
		section: "Removed",
		entry:   "- **Breaking: the `exclusive` option.** Delete\n  the key.",
	}, got)

	for _, tc := range []struct{ name, body, want string }{
		{"empty", "", "opens with no `### <group>` heading"},
		{"no heading", "- **A thing.**\n", "sits under a section heading"},
		{"unknown group", "### Fix\n\n- **A thing.**\n", `f.md: line 1: "Fix" is not a Keep a Changelog section`},
		{"heading alone", "### Added\n", "holds 0 entries"},
		{"two entries", "### Added\n\n- **One.**\n- **Two.**\n", "holds 2 entries"},
		{"two sections", "### Added\n\n- **One.**\n\n### Fixed\n\n- **Two.**\n", "more than one section heading"},
		{"no headline", "### Added\n\n- plain.\n", "opens with a **bold headline**"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseFragment("f.md", []byte(tc.body))
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestReadFragments(t *testing.T) {
	t.Run("a missing directory holds none", func(t *testing.T) {
		got, err := readFragments(filepath.Join(t.TempDir(), "absent"))
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("every bad file is reported and dotfiles are skipped", func(t *testing.T) {
		dir := t.TempDir()
		writeFragment(t, dir, ".DS_Store", "binary")
		writeFragment(t, dir, "ok.md", "### Added\n\n- **Fine.**\n")
		writeFragment(t, dir, "bad.md", "### Addded\n\n- **Typo.**\n")
		writeFragment(t, dir, "notes.txt", "### Added\n\n- **Wrong extension.**\n")
		_, err := readFragments(dir)
		require.Error(t, err)
		assert.ErrorContains(t, err, `bad.md: line 1: "Addded" is not a Keep a Changelog section`)
		assert.ErrorContains(t, err, "notes.txt: not a fragment")
	})
}

func TestRenderUnreleased(t *testing.T) {
	frags := []fragment{
		{section: "Fixed", entry: "- **A fix.**"},
		{section: "Added", entry: "- **First.**"},
		{section: "Added", entry: "- **Second.** It\n  wraps."},
	}
	want := "### Added\n\n- **First.**\n- **Second.** It\n  wraps.\n\n### Fixed\n\n- **A fix.**"
	assert.Equal(t, want, renderUnreleased(frags))
	assert.Empty(t, lintUnreleased(want))
	assert.Equal(t, "", renderUnreleased(nil))
}

func TestRunLintFragments(t *testing.T) {
	dir := t.TempDir()
	writeFragment(t, dir, "ok.md", "### Security\n\n- **Patched.**\n")
	writeFragment(t, dir, "bad.md", "### Added\n\n- no headline\n")
	require.NoError(t, runLintFragments([]string{filepath.Join(dir, "ok.md")}))
	err := runLintFragments([]string{filepath.Join(dir, "ok.md"), filepath.Join(dir, "bad.md"), filepath.Join(dir, "x.txt")})
	require.ErrorContains(t, err, "bad.md: line 3: an entry opens with a **bold headline**")
	require.ErrorContains(t, err, "x.txt: not a fragment")
	require.Error(t, runLintFragments(nil))
}
