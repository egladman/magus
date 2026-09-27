package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeFragment writes one fragment file into dir for tests.
func writeFragment(t *testing.T, dir, name, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644))
}

// TestParseFragment runs the cases tools/changelog.buzz's tests run, so cut's
// parser and every other reader's agree on what a fragment is.
func TestParseFragment(t *testing.T) {
	archive, err := txtar.ParseFile(filepath.Join("..", "..", "changes", "testdata", "fragments.txtar"))
	require.NoError(t, err)
	files := map[string]string{}
	for _, f := range archive.Files {
		files[f.Name] = string(f.Data)
	}

	var valid, invalid int
	for name, body := range files {
		switch {
		case strings.HasPrefix(name, "valid/"):
			valid++
			t.Run(name, func(t *testing.T) {
				_, err := parseFragment("f.md", []byte(body))
				require.NoError(t, err)
			})
		case strings.HasPrefix(name, "invalid/") && strings.HasSuffix(name, ".md"):
			invalid++
			want := strings.TrimSpace(files[strings.TrimSuffix(name, ".md")+".want"])
			require.NotEmpty(t, want, "%s has a .want beside it", name)
			t.Run(name, func(t *testing.T) {
				_, err := parseFragment("f.md", []byte(body))
				require.ErrorContains(t, err, want)
			})
		}
	}
	require.GreaterOrEqual(t, valid, 3)
	require.GreaterOrEqual(t, invalid, 10)

	got, err := parseFragment("f.md", []byte(files["valid/removed-breaking.md"]))
	require.NoError(t, err)
	assert.Equal(t, fragment{
		path:    "f.md",
		section: "Removed",
		entry:   "- **Breaking: the `exclusive` option.** Delete\n  the key.",
	}, got)
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
