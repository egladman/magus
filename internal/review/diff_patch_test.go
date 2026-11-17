package review

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChangedPathsFromPatchReadsTheHeaders(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/cmd/magus/diff.go b/cmd/magus/diff.go",
		"index 111..222 100644",
		"--- a/cmd/magus/diff.go",
		"+++ b/cmd/magus/diff.go",
		"+// a change",
		"diff --git a/docs/graph.json b/docs/graph.json",
		"diff --git a/cmd/magus/diff.go b/cmd/magus/diff.go",
		"",
	}, "\n")

	assert.Equal(t, []string{"cmd/magus/diff.go", "docs/graph.json"}, ChangedPathsFromPatch(patch))
	assert.Empty(t, ChangedPathsFromPatch(""))
}

// A header with no a//b prefixes is not malformed; it is what `git diff --no-prefix` emits,
// and diff.noPrefix is a setting plenty of people turn on. This used to be pinned the other
// way, as a line to skip, so such a patch reported ZERO changed files at exit 0. The reader
// falls back to a whitespace split there, which cannot handle a path containing spaces but
// names the file rather than dropping it.
func TestNoPrefixHeadersStillNameTheirFiles(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git f.txt renamed.txt",
		"similarity index 100%",
		"rename from f.txt",
		"rename to renamed.txt",
		"",
	}, "\n")
	assert.Equal(t, []string{"renamed.txt"}, ChangedPathsFromPatch(patch))
}

// A VCS colorizes when it believes it is writing to a terminal, which is precisely the case
// when magus is its pager, so the first patch a reader ever hands over through the wiring in
// docs/guides/integrations/git.md is a colorized one. Its headers sit behind an escape
// sequence and no longer begin a line, so nothing parses, and "no headers magus can read" is
// true but sends them looking in the wrong place.
func TestAColorizedPatchNamesColorAsTheCause(t *testing.T) {
	colorized := "\x1b[0;1mdiff -r abc123 f.txt\x1b[0m\n" +
		"\x1b[0;31;1m--- a/f.txt\x1b[0m\n" +
		"\x1b[0;32;1m+++ b/f.txt\x1b[0m\n"
	assert.Empty(t, ChangedPathsFromPatch(colorized), "escape sequences hide the headers")
	assert.Contains(t, colorized, "\x1b[", "the detection this refusal keys on")
}
