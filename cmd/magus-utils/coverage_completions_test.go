package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// awkwardSubs carries the two characters that have broken a generated script: a colon
// terminates zsh's name:description pair, and an apostrophe closes the single-quoted
// string in zsh, fish and PowerShell alike.
var awkwardSubs = []subcommandDoc{
	{Name: "run", Short: "Run it"},
	{Name: "query", Short: "a node's neighborhood: really"},
}

func TestRenderBashList(t *testing.T) {
	assert.Equal(t,
		`    local subcommands="run query"`,
		renderBashList(awkwardSubs))
}

func TestRenderZshListEscapesColonsAndApostrophes(t *testing.T) {
	want := "            subcommands=(\n" +
		"                'run:Run it'\n" +
		"                'query:a node" + `'\''` + "s neighborhood" + `\:` + " really'\n" +
		"            )"
	assert.Equal(t, want, renderZshList(awkwardSubs))
}

// TestRenderZshListWrapsTheWholeAssignment: zsh does not treat "#" as a comment inside
// an array literal, so a marker between the parens parses as an element and the closing
// paren then fails. The renderer therefore has to emit both parens itself.
func TestRenderZshListWrapsTheWholeAssignment(t *testing.T) {
	got := renderZshList(awkwardSubs)
	assert.True(t, strings.HasPrefix(got, "            subcommands=("), got)
	assert.True(t, strings.HasSuffix(got, "            )"), got)
}

func TestRenderFishListPadsToTheWidestNameAndDropsTheLastContinuation(t *testing.T) {
	want := "        run   'Run it' \\" + "\n" +
		`        query 'a node\'s neighborhood: really'`
	assert.Equal(t, want, renderFishList(awkwardSubs))
}

func TestRenderPowerShellListBreaksAtSix(t *testing.T) {
	subs := []subcommandDoc{
		{Name: "a"}, {Name: "b"}, {Name: "c"}, {Name: "d"},
		{Name: "e"}, {Name: "f"}, {Name: "g"},
	}
	want := "    $subcommands = 'a', 'b', 'c', 'd', 'e', 'f',\n" +
		"                   'g'"
	assert.Equal(t, want, renderPowerShellList(subs))
}

// TestRenderPowerShellListSingleRow: the sole row carries the variable name and no
// trailing comma, which is the shape a short list has to produce.
func TestRenderPowerShellListSingleRow(t *testing.T) {
	assert.Equal(t,
		"    $subcommands = 'run', 'query'",
		renderPowerShellList(awkwardSubs))
}

// TestRenderersAreDeterministic guards the property the whole drift gate rests on: the
// same input must render the same bytes, or every generate run reports drift.
func TestRenderersAreDeterministic(t *testing.T) {
	for name, render := range map[string]func([]subcommandDoc) string{
		"bash": renderBashList,
		"zsh":  renderZshList,
		"fish": renderFishList,
		"ps1":  renderPowerShellList,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, render(awkwardSubs), render(awkwardSubs))
		})
	}
}

const surfaceFixture = `package main

type subcommand struct {
	Name  string
	Short string
}

var subcommands = []subcommand{
	{Name: "run", Short: "Run it"},
	{Name: "query", Short: "a node's neighborhood: really"},
	{Short: "an entry with no Name"},
}
`

func writeSurface(t *testing.T, dir, body string) string {
	t.Helper()
	path := filepath.Join(dir, "surface.go")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

func TestReadSurfaceDropsANamelessEntry(t *testing.T) {
	got, err := readSurface(writeSurface(t, t.TempDir(), surfaceFixture))
	require.NoError(t, err)
	assert.Equal(t, awkwardSubs, got)
}

func TestReadSurfaceSurfacesAParseFailure(t *testing.T) {
	_, err := readSurface(writeSurface(t, t.TempDir(), "package main\nvar x = (\n"))
	assert.Error(t, err)
}

// completionsDir plants the four scripts with an empty generated region, which is what
// a hand-written script looks like before the list is filled in.
func completionsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"magus.bash", "magus.zsh", "magus.fish", "magus.ps1"} {
		body := "# leading logic\n" +
			shellMarker.Begin + "\n" +
			shellMarker.End + "\n" +
			"# trailing logic\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644))
	}
	return dir
}

func TestRunCompletionsFillsEveryDialect(t *testing.T) {
	src := t.TempDir()
	out := completionsDir(t)
	surface := writeSurface(t, src, surfaceFixture)

	require.NoError(t, runCompletions([]string{"-surface", surface, "-out", out}))

	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(out, name))
		require.NoError(t, err)
		return string(body)
	}

	// The hand-written shell around the region is what the marker design exists to
	// leave alone; a whole-file rewrite would take it with it.
	for _, name := range []string{"magus.bash", "magus.zsh", "magus.fish", "magus.ps1"} {
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

// TestRunCompletionsIsIdempotent covers the "already current" branch, which exists so a
// no-op regenerate does not touch an mtime and dirty every downstream cache key.
func TestRunCompletionsIsIdempotent(t *testing.T) {
	src := t.TempDir()
	out := completionsDir(t)
	surface := writeSurface(t, src, surfaceFixture)

	require.NoError(t, runCompletions([]string{"-surface", surface, "-out", out}))
	first, err := os.ReadFile(filepath.Join(out, "magus.bash"))
	require.NoError(t, err)
	stat, err := os.Stat(filepath.Join(out, "magus.bash"))
	require.NoError(t, err)

	require.NoError(t, runCompletions([]string{"-surface", surface, "-out", out}))
	second, err := os.ReadFile(filepath.Join(out, "magus.bash"))
	require.NoError(t, err)
	restat, err := os.Stat(filepath.Join(out, "magus.bash"))
	require.NoError(t, err)

	assert.Equal(t, string(first), string(second))
	assert.Equal(t, stat.ModTime(), restat.ModTime(), "an unchanged script must not be rewritten")
}

// TestRunCompletionsRefusesAnEmptySurface: zero subcommands is the parse being wrong,
// not the surface, and writing an empty list into four scripts would ship it.
func TestRunCompletionsRefusesAnEmptySurface(t *testing.T) {
	src := t.TempDir()
	surface := writeSurface(t, src, "package main\n\nvar unrelated = 1\n")

	err := runCompletions([]string{"-surface", surface, "-out", completionsDir(t)})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no subcommands found")
}

// TestRunCompletionsNamesTheScriptMissingItsMarker. A generator cannot know where in a
// hand-written file the region belongs, so it must refuse rather than append - and the
// error has to say which of the four scripts it was reading.
func TestRunCompletionsNamesTheScriptMissingItsMarker(t *testing.T) {
	src := t.TempDir()
	out := completionsDir(t)
	require.NoError(t, os.WriteFile(filepath.Join(out, "magus.zsh"), []byte("# no markers here\n"), 0o644))

	err := runCompletions([]string{"-surface", writeSurface(t, src, surfaceFixture), "-out", out})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "magus.zsh")
	assert.Contains(t, err.Error(), shellMarker.Begin)
}

func TestRunCompletionsSurfacesAMissingScript(t *testing.T) {
	src := t.TempDir()
	err := runCompletions([]string{
		"-surface", writeSurface(t, src, surfaceFixture),
		"-out", filepath.Join(t.TempDir(), "absent"),
	})
	assert.Error(t, err)
}
