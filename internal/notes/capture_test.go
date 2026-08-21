package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func capturedAt() time.Time {
	return time.Date(2026, 8, 19, 14, 2, 11, 0, time.UTC)
}

func validCapture() Capture {
	return Capture{
		Title:  "Why the cache key dropped the tree hash",
		Tags:   []string{"review"},
		Source: Source{Kind: SourceReviewThread, Ref: "sess_1a2b", AsOf: "9cc8be0", Captured: capturedAt()},
		Entries: []CaptureEntry{
			{Subject: "internal/cache/key.go", Locator: HunkLocator(3), Author: "Eli", Body: "Why not hash the tree?"},
			{
				Subject:  "internal/cache/key.go",
				Locator:  HunkLocator(3),
				Author:   "claude",
				Body:     "Every target would miss on every commit.",
				Resolved: true,
			},
			{Subject: "internal/cache/store.go", Author: "Eli", Body: "Fine, leave it."},
		},
	}
}

func TestCaptureNoteAnchorsEveryFileItTouches(t *testing.T) {
	n, err := validCapture().Note("review-cache-key")
	require.NoError(t, err)

	assert.Equal(t, []Anchor{
		{Kind: AnchorFile, Target: "internal/cache/key.go"},
		{Kind: AnchorFile, Target: "internal/cache/store.go"},
	}, n.Anchors, "one file anchor per distinct subject, deduplicated and ordered")
}

// The provenance is the whole reason a capture may live in a store that otherwise holds only
// authored prose, so it has to survive the round trip rather than being decoration on the
// in-memory value.
func TestCaptureSourceSurvivesSaveAndRead(t *testing.T) {
	dir := t.TempDir()
	n, err := validCapture().Note("review-cache-key")
	require.NoError(t, err)
	require.NoError(t, Save(dir, n))

	got, err := Get(dir, "review-cache-key")
	require.NoError(t, err)
	require.NotNil(t, got.Source, "a capture read back is still marked as one")
	assert.Equal(t, Source{
		Kind:     SourceReviewThread,
		Ref:      "sess_1a2b",
		AsOf:     "9cc8be0",
		Captured: capturedAt(),
	}, *got.Source)
}

// The one key magus owns is `magus`; a note store may be an Obsidian vault whose frontmatter
// already uses `source` for something of its own.
func TestCaptureSourceIsNestedUnderTheMagusKey(t *testing.T) {
	dir := t.TempDir()
	n, err := validCapture().Note("review-cache-key")
	require.NoError(t, err)
	require.NoError(t, Save(dir, n))

	raw, err := os.ReadFile(filepath.Join(dir, "review-cache-key.md"))
	require.NoError(t, err)
	text := string(raw)

	// Asserted by indentation rather than by an exact prefix: the width is yaml.v3's business
	// and the invariant is only that the key is nested, never at column zero.
	assert.Regexp(t, `(?m)^ +source:`, text, "source is nested inside the magus block")
	assert.NotContains(t, text, "\nsource:", "and never claims a second top-level key")
}

// A written note must not gain the marker just by being saved, or the distinction the marker
// exists to draw stops meaning anything.
func TestAuthoredNoteCarriesNoSource(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, Save(dir, validNote()))

	got, err := Get(dir, "cache-invalidation-pairing")
	require.NoError(t, err)
	assert.Nil(t, got.Source)

	raw, err := os.ReadFile(filepath.Join(dir, "cache-invalidation-pairing.md"))
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "source:", "no empty block in a hand-written note's vault")
}

func TestCaptureBodyGroupsBySubjectAndAttributes(t *testing.T) {
	n, err := validCapture().Note("review-cache-key")
	require.NoError(t, err)

	// Setext, so a heading is a heading to a markdown renderer AND an underline to the reader
	// of a surface that prints the body as text. Asserted by deriving the rule from the
	// heading: a hand-counted row of dashes tests the count, which is not the invariant and
	// goes red for a one-character rename.
	setext := func(head string) string { return head + "\n" + strings.Repeat("-", len(head)) + "\n" }
	assert.Contains(t, n.Body, setext("internal/cache/key.go hunk 3"))
	assert.Contains(t, n.Body, setext("internal/cache/store.go"))
	assert.Contains(t, n.Body, "\nEli:\n")
	assert.Contains(t, n.Body, "\nclaude (resolved):\n")
	assert.Contains(t, n.Body, "Session sess_1a2b, patch 9cc8be0.")

	// One heading per RUN of a subject, not one per entry: two comments on the same file
	// are one section.
	assert.Equal(t, 1, strings.Count(n.Body, "internal/cache/key.go hunk 3\n---"))

	// Nothing in a generated body may need a markdown renderer to be legible: every magus
	// surface prints a note body as text, because the body is untrusted by contract.
	assert.NotContains(t, n.Body, "#")
}

// A reader who meets this file in six months has to learn it is quoted before they quote it
// at somebody. The frontmatter says so to tools; the body has to say so to a person.
func TestCaptureBodySaysItIsATranscript(t *testing.T) {
	n, err := validCapture().Note("review-cache-key")
	require.NoError(t, err)
	assert.Contains(t, n.Body, "A transcript, not written prose")
}

func TestCaptureRefusesWhatItCannotRecord(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Capture)
		want   string
	}{
		{"no entries", func(c *Capture) { c.Entries = nil }, "no messages"},
		{"no title", func(c *Capture) { c.Title = "" }, "needs a title"},
		{"no source kind", func(c *Capture) { c.Source.Kind = "" }, "needs a source kind"},
		{"no capture time", func(c *Capture) { c.Source.Captured = time.Time{} }, "the time it was taken"},
		{
			"no subject on any entry",
			func(c *Capture) {
				for i := range c.Entries {
					c.Entries[i].Subject = ""
				}
			},
			"names no file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := validCapture()
			tt.mutate(&c)
			_, err := c.Note("review-cache-key")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

// An unnamed speaker is recorded as unattributed rather than guessed at: a capture's value is
// that its provenance is checkable, and inventing a name is the one thing that breaks it.
func TestCaptureNamesAnUnattributedSpeaker(t *testing.T) {
	c := validCapture()
	c.Entries[0].Author = ""
	n, err := c.Note("review-cache-key")
	require.NoError(t, err)
	assert.Contains(t, n.Body, "\nunattributed:\n")
}

func TestHunkLocatorDropsAnAbsentHunk(t *testing.T) {
	assert.Equal(t, "hunk 3", HunkLocator(3))
	assert.Equal(t, "", HunkLocator(0))
}

// Captured is the moment the transcript was taken and must not drift with the file's mtime,
// so it is stored rather than observed - and stored normalized, or the same capture taken in
// two timezones reads as two different times.
func TestCaptureTimeIsNormalizedToUTC(t *testing.T) {
	c := validCapture()
	c.Source.Captured = capturedAt().In(time.FixedZone("somewhere", 5*3600))
	n, err := c.Note("review-cache-key")
	require.NoError(t, err)
	require.NotNil(t, n.Source)
	assert.Equal(t, time.UTC, n.Source.Captured.Location())
	assert.True(t, n.Source.Captured.Equal(capturedAt()))
}
