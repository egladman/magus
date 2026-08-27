package changeset

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// rlo is U+202E, written as an escape rather than as itself: a test file carrying the raw
// character is a file where the deception is invisible to whoever reviews the test, which is the
// exact failure under test. bidichk enforces this, and caught it here first.
const rlo = "\u202e"

// zwsp is U+200B, the invisible half of the same family.
const zwsp = "\u200b"

// The attack, end to end: a right-to-left override inside a comment makes the line RENDER as
// though the comment ended, while the bytes say it never did.
func TestSanitizeBidiRevealsAnOverride(t *testing.T) {
	line := "\tif user.Admin { // " + rlo + " } yletamitigel nrret"

	got, changed := SanitizeBidi(line)

	assert.True(t, changed)
	assert.Contains(t, got, "<U+202E>", "the reader is shown exactly where the reordering was")
	assert.NotContains(t, got, rlo, "and no renderer downstream can still obey it")
}

// Escaped, never stripped. Stripping renders honestly and silently misreports the file, which
// would have a reviewer approve bytes magus never showed them.
func TestSanitizeBidiEscapesRatherThanStrips(t *testing.T) {
	got, _ := SanitizeBidi("a" + zwsp + "b")

	assert.Equal(t, "a<U+200B>b", got)
	assert.NotEqual(t, "ab", got, "a stripped line lies about the file's contents")
}

func TestSanitizeBidiCoversBothFamilies(t *testing.T) {
	for name, r := range map[string]rune{
		"left-to-right embedding": 0x202A,
		"right-to-left override":  0x202E,
		"first strong isolate":    0x2068,
		"pop directional isolate": 0x2069,
		"zero width space":        0x200B,
		"right-to-left mark":      0x200F,
		"word joiner":             0x2060,
		"byte order mark":         0xFEFF,
	} {
		_, changed := SanitizeBidi("x" + string(r) + "y")
		assert.True(t, changed, name)
	}
}

// The control: an ordinary line is returned untouched and reports no change, so nothing downstream
// treats a normal diff as suspicious and no allocation is spent on one. A sanitizer that rewrote
// every line would make a diff full of escaped tabs, which is a diff nobody reads.
func TestSanitizeBidiLeavesOrdinaryCodeAlone(t *testing.T) {
	for _, line := range []string{
		"\tif user.Admin { // trusted",
		"func F(a, b int) error { return nil }",
		"    // a comment with unicode: café, naïve, 日本語",
		"",
	} {
		got, changed := SanitizeBidi(line)

		assert.False(t, changed, line)
		assert.Equal(t, line, got)
	}
}

// TestParseSanitizesBothRenderedForms is the one that matters: the terminal draws Lines and the
// browser draws Rows, so covering one surface and not the other leaves a reader looking at the
// deception with no way to tell which surface they are in.
func TestParseSanitizesBothRenderedForms(t *testing.T) {
	patch := "diff --git a/a.go b/a.go\n" +
		"--- a/a.go\n+++ b/a.go\n" +
		"@@ -1,2 +1,2 @@ func F()\n" +
		" ok\n" +
		"+\tif admin { // " + rlo + " } trap\n"

	files := Parse(patch)
	if assert.Len(t, files, 1) && assert.Len(t, files[0].Hunks, 1) {
		h := files[0].Hunks[0]

		assert.NotNil(t, h.Display, "the terminal's form is escaped")
		assert.Contains(t, h.Display[1], "<U+202E>")
		assert.NotContains(t, h.Display[1], rlo)

		var found bool
		for _, r := range h.Rows {
			if strings.Contains(r.Text, "<U+202E>") {
				found = true
				assert.Nil(t, r.Emph, "an escaped row's offsets no longer address its text")
			}
			assert.NotContains(t, r.Text, rlo, "the browser's form is escaped too")
		}
		assert.True(t, found, "the row carrying the override is the one that was escaped")

		// The identity is untouched: a line rewritten for safe display must not become a
		// different hunk, or every existing read mark for it would silently detach.
		assert.Contains(t, h.Lines[1], rlo, "Lines keeps the real bytes the digest addresses")
	}
}

// The control for the whole feature: an honest patch allocates no second copy and carries no
// Display at all, so this costs nothing to ship and nothing to hold.
func TestParseLeavesAnHonestPatchAlone(t *testing.T) {
	patch := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -1 +1 @@\n-old\n+new\n"

	files := Parse(patch)
	if assert.Len(t, files, 1) && assert.Len(t, files[0].Hunks, 1) {
		assert.Nil(t, files[0].Hunks[0].Display)
	}
}
