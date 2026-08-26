package diff

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// hunkAt builds a hunk whose new side starts at start and holds body, with every line a context
// line so newSideBody returns exactly what was passed.
func hunkAt(start int, body ...string) Hunk {
	lines := make([]string, 0, len(body))
	for _, l := range body {
		lines = append(lines, " "+l)
	}
	return Hunk{Lines: lines, Digest: "d", NewStart: start, NewCount: len(body)}
}

func TestCaptureAnchorRemembersTheLineAndItsContext(t *testing.T) {
	h := hunkAt(10, "func F() {", "\tx := 1", "\treturn x", "}")

	got := CaptureAnchor([]Hunk{h}, 12)

	assert.Equal(t, "\treturn x", got.Quote)
	assert.Equal(t, []string{"func F() {", "\tx := 1"}, got.Before)
	assert.Equal(t, []string{"}"}, got.After)
	assert.Equal(t, "d", got.Digest)
}

// A remark with no line under it - a file heading, or a line no hunk covers - has no text to
// remember. Zero rather than a guess, which is what makes the AnchorUnknown rung honest.
func TestCaptureAnchorIsEmptyWhereThereIsNothingToRemember(t *testing.T) {
	h := hunkAt(10, "a", "b")

	assert.Zero(t, CaptureAnchor([]Hunk{h}, 99))
	assert.Zero(t, CaptureAnchor(nil, 10))
}

func TestLocateAnchor(t *testing.T) {
	a := CaptureAnchor([]Hunk{hunkAt(10, "func F() {", "\tx := 1", "\treturn x", "}")}, 12)

	t.Run("unmoved code resolves to the remembered line", func(t *testing.T) {
		same := []Hunk{hunkAt(10, "func F() {", "\tx := 1", "\treturn x", "}")}

		line, rung := LocateAnchor(a, same, 12)

		assert.Equal(t, 12, line)
		assert.Equal(t, types.AnchorExact, rung)
	})

	t.Run("code that moved is re-found and says so", func(t *testing.T) {
		// Twenty lines inserted above: the remembered line now holds something else entirely,
		// which is exactly the case a line number cannot survive and a quote can.
		moved := []Hunk{hunkAt(30, "func F() {", "\tx := 1", "\treturn x", "}")}

		line, rung := LocateAnchor(a, moved, 12)

		assert.Equal(t, 32, line)
		assert.Equal(t, types.AnchorMoved, rung,
			"the reader must be told it moved; presenting this as exact reads as the original position")
	})

	t.Run("code that is gone keeps the path and loses the line", func(t *testing.T) {
		gone := []Hunk{hunkAt(10, "func F() {", "\tsomething else", "}")}

		line, rung := LocateAnchor(a, gone, 12)

		assert.Zero(t, line, "a line that would land on the wrong code is worse than no line")
		assert.Equal(t, types.AnchorLost, rung)
	})

	t.Run("a remark with no quote is unknown, not exact", func(t *testing.T) {
		line, rung := LocateAnchor(types.CommentAnchor{}, []Hunk{hunkAt(10, "a")}, 10)

		assert.Equal(t, 10, line)
		assert.Equal(t, types.AnchorUnknown, rung,
			"still in place and nobody checked are different facts")
	})
}

// The context is what makes a non-unique line usable. A bare closing brace appears four times
// here, and the quote alone would resolve to whichever the scan reached first.
func TestLocateAnchorUsesContextToPickAmongIdenticalLines(t *testing.T) {
	original := []Hunk{hunkAt(1,
		"func A() {", "\ta()", "}",
		"func B() {", "\tb()", "}",
		"func C() {", "\tc()", "}",
	)}
	// The brace closing B, at line 6.
	a := CaptureAnchor(original, 6)
	require.Equal(t, "}", a.Quote)

	// The same file with a function inserted above, so every line moved by three.
	moved := []Hunk{hunkAt(1,
		"func Z() {", "\tz()", "}",
		"func A() {", "\ta()", "}",
		"func B() {", "\tb()", "}",
		"func C() {", "\tc()", "}",
	)}

	line, rung := LocateAnchor(a, moved, 6)

	assert.Equal(t, types.AnchorMoved, rung)
	assert.Equal(t, 9, line, "B's closing brace, not A's or C's")
}

// Without the tie-break an equally-contexted match resolves by hunk order, which is a fact about
// the patch rather than about the remark.
func TestLocateAnchorPrefersTheNearerOfEquallyGoodMatches(t *testing.T) {
	a := types.CommentAnchor{Quote: "\treturn nil"}
	hunks := []Hunk{hunkAt(5, "\treturn nil"), hunkAt(80, "\treturn nil")}

	near, rung := LocateAnchor(a, hunks, 78)
	assert.Equal(t, 80, near)
	assert.Equal(t, types.AnchorMoved, rung)

	far, _ := LocateAnchor(a, hunks, 7)
	assert.Equal(t, 5, far)
}
