package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/cmdline"
)

// awkwardSubs carries the two characters that have broken a generated script: a colon
// terminates zsh's name:description pair, and an apostrophe closes the single-quoted
// string in zsh, fish and PowerShell alike.
var awkwardSubs = []cmdline.Command{
	{Name: "run", Short: "Run it"},
	{Name: "query", Short: "a node's neighborhood: really"},
}

// completionsDir plants the four scripts with an empty generated region, which is what
// a hand-written script looks like before the list is filled in.
func completionsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for name := range completionScripts {
		body := fmt.Sprintf(`# leading logic
%s
%s
# trailing logic
`, shellMarker.Begin, shellMarker.End)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	return dir
}

func TestWriteCompletionsFillsEveryDialect(t *testing.T) {
	out := completionsDir(t)

	require.NoError(t, writeCompletions(out, awkwardSubs))

	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(out, name))
		require.NoError(t, err)
		return string(body)
	}

	// The hand-written shell around the region is what the marker design exists to
	// leave alone; a whole-file rewrite would take it with it.
	for name := range completionScripts {
		body := read(name)
		assert.Contains(t, body, "# leading logic\n", name)
		assert.Contains(t, body, "# trailing logic\n", name)
		assert.Contains(t, body, shellMarker.Begin, name)
		assert.Contains(t, body, shellMarker.End, name)
	}

	assert.Contains(t, read("magus.bash"), `local subcommands="run query"`)
	assert.Contains(t, read("magus.zsh"), `'query:a node'\''s neighborhood\: really'`)
	assert.Contains(t, read("magus.fish"), `'a node\'s neighborhood: really'`)
	assert.Contains(t, read("magus.ps1"), "$subcommands = 'run', 'query'")
}

// TestWriteCompletionsIsIdempotent covers the "already current" branch, which exists so a
// no-op regenerate does not touch an mtime and dirty every downstream cache key.
func TestWriteCompletionsIsIdempotent(t *testing.T) {
	out := completionsDir(t)

	require.NoError(t, writeCompletions(out, awkwardSubs))
	first, err := os.ReadFile(filepath.Join(out, "magus.bash"))
	require.NoError(t, err)
	stat, err := os.Stat(filepath.Join(out, "magus.bash"))
	require.NoError(t, err)

	require.NoError(t, writeCompletions(out, awkwardSubs))
	second, err := os.ReadFile(filepath.Join(out, "magus.bash"))
	require.NoError(t, err)
	restat, err := os.Stat(filepath.Join(out, "magus.bash"))
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second))
	assert.Equal(t, stat.ModTime(), restat.ModTime(), "an unchanged script must not be rewritten")
}

// TestWriteCompletionsRefusesAnEmptyTable: writing an empty list into four scripts
// would ship shells that complete nothing.
func TestWriteCompletionsRefusesAnEmptyTable(t *testing.T) {
	err := writeCompletions(completionsDir(t), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no subcommands")
}

// TestWriteCompletionsNamesTheScriptMissingItsMarker. A generator cannot know where in a
// hand-written file the region belongs, so it must refuse rather than append, and the
// error has to say which of the four scripts it was reading.
func TestWriteCompletionsNamesTheScriptMissingItsMarker(t *testing.T) {
	out := completionsDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(out, "magus.zsh"), []byte("# no markers here\n"), 0o644))

	err := writeCompletions(out, awkwardSubs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "magus.zsh")
	assert.Contains(t, err.Error(), shellMarker.Begin)
}

func TestWriteCompletionsSurfacesAMissingScript(t *testing.T) {
	assert.Error(t, writeCompletions(filepath.Join(t.TempDir(), "absent"), awkwardSubs))
}
