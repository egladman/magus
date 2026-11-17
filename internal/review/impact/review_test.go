package impact

import (
	"fmt"
	"strings"
	"testing"

	difftypes "github.com/egladman/magus/types/diff"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImpactReviewLines(t *testing.T) {
	// The empty form is the half that matters: a silent section reads as a clean bill of
	// health, and here that would mean "somebody read this".
	t.Run("unmeasured says so rather than saying unread", func(t *testing.T) {
		lines := reviewLines(nil)
		require.Len(t, lines, 1)
		assert.Contains(t, lines[0], "unavailable")
	})

	t.Run("names the unopened files and caps the list", func(t *testing.T) {
		r := &reviewSummary{Files: 30, Read: 0}
		for i := range 30 {
			r.Unread = append(r.Unread, fmt.Sprintf("f%d.go", i))
		}
		lines := reviewLines(r)
		assert.Contains(t, lines[0], "30 file(s) you have not opened")
		assert.Contains(t, lines[unreadShown+1], "and 20 more")
	})

	// No ratio, anywhere. A count with a target is one that gets cleared rather than
	// satisfied, and clearing it takes one keystroke and no reading.
	t.Run("reports no read ratio", func(t *testing.T) {
		lines := reviewLines(&reviewSummary{
			Files: 40, Read: 12,
			Stale:  []string{"internal/cache/key.go"},
			Unread: []string{"a.go", "b.go"},
		})
		for _, l := range lines {
			assert.NotContains(t, l, " of 40")
			assert.NotContains(t, l, "12")
		}
	})

	// Stale is the finding, so it leads whatever else the section holds: it is derived
	// from content rather than from a claim, so no amount of stamping produces it.
	t.Run("stale leads and is named", func(t *testing.T) {
		lines := reviewLines(&reviewSummary{
			Files: 4, Read: 1,
			Stale:  []string{"internal/cache/key.go"},
			Unread: []string{"a.go"},
		})
		assert.Contains(t, lines[0], "1 file(s) changed after you read them")
		assert.Equal(t, "      internal/cache/key.go", lines[1])
	})

	// A small undisturbed change needs no reading plan, and printing one there is how a
	// reader learns to skip the section before meeting a change big enough to need it.
	t.Run("says nothing about a small undisturbed change", func(t *testing.T) {
		assert.Nil(t, reviewLines(&reviewSummary{
			Files: 3, Unread: []string{"a.go", "b.go", "c.go"},
		}))
	})

	// ...but a small change where something moved under the reader is exactly when the
	// section earns its line.
	t.Run("a small change still reports stale", func(t *testing.T) {
		lines := reviewLines(&reviewSummary{Files: 2, Stale: []string{"a.go"}})
		require.NotEmpty(t, lines)
		assert.Contains(t, lines[0], "changed after you read them")
	})

	t.Run("says nothing when everything has been read", func(t *testing.T) {
		assert.Nil(t, reviewLines(&reviewSummary{Files: 30, Read: 30}))
	})
}

// A bulk cover is echoed so a file stamped in one keystroke does not read as one somebody
// sat down with. It is a note the reader left themselves, not a toll they paid.
func TestImpactReviewLinesReportsBulkReasons(t *testing.T) {
	lines := reviewLines(&reviewSummary{
		Files: 8, Read: 4,
		Unread:  []string{"a.go"},
		Reasons: []string{"codemod output, spot-checked 3 of 40"},
	})
	joined := strings.Join(lines, "\n")
	assert.Contains(t, joined, "covered in bulk")
	assert.Contains(t, joined, "codemod output")
}

func TestImpactReviewLinesListsRequiredUncapped(t *testing.T) {
	r := &reviewSummary{Files: 30, Read: 0}
	for i := range unreadShown + 5 {
		r.Required = append(r.Required, fmt.Sprintf("internal/secret/f%d.go", i))
	}
	r.Unread = append([]string{}, r.Required...)
	lines := reviewLines(r)
	assert.Contains(t, lines[0], "15 unopened in review_required paths")
	// Uncapped, unlike the general list: the workspace said these cost something.
	assert.Contains(t, lines[15], "internal/secret/f14.go")
}

// A path the workspace flagged is named once, under review_required, and not again in the
// general list. Saying it twice reads as two findings.
func TestImpactReviewLinesDoesNotRepeatRequiredPaths(t *testing.T) {
	lines := reviewLines(&reviewSummary{
		Files:    8,
		Required: []string{"internal/secret/value.go"},
		Unread:   []string{"internal/secret/value.go", "a.go"},
	})
	joined := strings.Join(lines, "\n")
	assert.Equal(t, 1, strings.Count(joined, "internal/secret/value.go"))
	assert.Contains(t, joined, "1 file(s) you have not opened")
}

// readChangeset is a diff of source files, each carrying the read state attach folded onto it.
func readChangeset(states map[string]string) difftypes.Diff {
	var rev difftypes.Diff
	for path, state := range states {
		rev.Files = append(rev.Files, difftypes.File{Path: path, Role: difftypes.RoleSource, ReadState: state})
	}
	return rev
}

func TestCollectReviewCountsAnUnreadChangeset(t *testing.T) {
	got := collectReview(readChangeset(map[string]string{"a.go": difftypes.ReadUnread, "b.go": difftypes.ReadUnread}), nil, nil)
	require.NotNil(t, got)
	assert.ElementsMatch(t, []string{"a.go", "b.go"}, got.Unread)
	assert.Equal(t, &reviewSummary{Files: 2, Unread: got.Unread}, got)
}

func TestCollectReviewCountsARead(t *testing.T) {
	got := collectReview(readChangeset(map[string]string{"a.go": difftypes.ReadRead}), nil, nil)
	assert.Equal(t, &reviewSummary{Files: 1, Read: 1}, got)
}

// Stale is its own list, not an annotated entry in the unopened one: it is a different
// finding and it leads the section.
func TestCollectReviewListsAStaleFileApart(t *testing.T) {
	got := collectReview(readChangeset(map[string]string{"a.go": difftypes.ReadStale}), nil, nil)
	assert.Equal(t, &reviewSummary{Files: 1, Stale: []string{"a.go"}}, got)
}

// Reading a machine's restatement of an edit made elsewhere is not the review.
func TestCollectReviewLeavesGeneratedOutputOut(t *testing.T) {
	rev := readChangeset(map[string]string{"a.go": difftypes.ReadUnread})
	rev.Files = append(rev.Files, difftypes.File{Path: "gen/x.go", Role: difftypes.RoleOutput, ReadState: difftypes.ReadUnread})

	got := collectReview(rev, nil, nil)
	assert.Equal(t, &reviewSummary{Files: 1, Unread: []string{"a.go"}}, got)
}

// A changeset no file of which carries a state was not measured. Reporting it as unread
// would accuse somebody of skipping a deletion.
func TestCollectReviewIsUnmeasuredWithoutAState(t *testing.T) {
	assert.Nil(t, collectReview(readChangeset(map[string]string{"gone.go": difftypes.ReadUnknown}), nil, nil))
}

func TestCollectReviewSeparatesRequiredPaths(t *testing.T) {
	got := collectReview(readChangeset(map[string]string{
		"internal/secret/value.go": difftypes.ReadUnread,
		"internal/cache/key.go":    difftypes.ReadUnread,
	}), func(p string) bool { return strings.HasPrefix(p, "internal/secret/") }, nil)
	require.NotNil(t, got)
	assert.Equal(t, []string{"internal/secret/value.go"}, got.Required)
	assert.Len(t, got.Unread, 2)
}
