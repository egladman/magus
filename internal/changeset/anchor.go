package changeset

import (
	"strings"

	"github.com/egladman/magus/types"
)

// CaptureAnchor records what a remark on line of path's hunks should remember about the code it
// was written against.
//
// Taken from the patch the reader was actually shown rather than from the file on disk. Those are
// the same thing for a working-tree review and different for every other kind, and a remark must
// attest to what its author saw.
//
// A line the changeset does not contain yields a zero anchor rather than a guess. That is the
// AnchorUnknown rung, and it is honest: a remark on a file heading, or on a line no hunk covers,
// has no text under it to remember.
func CaptureAnchor(hunks []Hunk, line int) types.CommentAnchor {
	h, idx, ok := newSideLine(hunks, line)
	if !ok {
		return types.CommentAnchor{}
	}
	body := newSideBody(h)
	return types.CommentAnchor{
		Digest:      h.Digest,
		Quote:       body[idx],
		Before:      window(body, idx-types.AnchorContextLines, idx),
		After:       window(body, idx+1, idx+1+types.AnchorContextLines),
		Declaration: h.Declaration,
	}
}

// LocateAnchor re-finds a remark's line in the hunks a reader is being shown now, and says how
// well it managed.
//
// The degradation is the whole design: the quote found where it was remembered, else found
// elsewhere and SAID to have moved, else lost. Each rung down is a weaker claim, and the caller
// renders which one it got instead of presenting all three as the same answer.
//
// remembered is the line the remark was written on. It stops being the answer the moment the file
// moves, and becomes the tie-breaker among equal matches - which is what makes a search for a
// non-unique line land where the reader meant rather than on the file's first closing brace.
func LocateAnchor(a types.CommentAnchor, hunks []Hunk, remembered int) (int, types.CommentAnchorRung) {
	if a.Quote == "" {
		return remembered, types.AnchorUnknown
	}
	// The remembered line is NOT checked first and trusted. It is scored like every other
	// candidate, because the text alone does not identify a position: a closing brace still sits
	// at the remembered line after a function was inserted above, and short-circuiting there
	// reported the wrong one of four as exact. It keeps its advantage through the tie-break,
	// where its distance from itself is zero.
	best, found := 0, false
	bestScore := -1
	for _, h := range hunks {
		body := newSideBody(h)
		for i, got := range body {
			if got != a.Quote {
				continue
			}
			score := contextScore(a, body, i)
			line := newSideNumber(h, i)
			// A strictly better context wins; an equal one goes to whichever line sits nearer
			// the remark's remembered position. Without the tie-break a file with the same line
			// four times would resolve by hunk order, which is not a fact about the remark.
			if !found || score > bestScore ||
				(score == bestScore && abs(line-remembered) < abs(best-remembered)) {
				best, bestScore, found = line, score, true
			}
		}
	}
	if !found {
		// The quote is gone. The declaration it sat in may not be, and "this remark was about
		// AttachChurn, which is here" is worth more to a reader than nothing at all.
		if line, ok := declarationLine(a, hunks); ok {
			return line, types.AnchorDeclaration
		}
		return 0, types.AnchorLost
	}
	if best == remembered {
		return best, types.AnchorExact
	}
	return best, types.AnchorMoved
}

// DeclarationOf is the enclosing declaration git named in a hunk header line: everything after
// the second @@. Empty where git named none, which is ordinary - the top of a file, a language with
// no funcname pattern, or a hunk that spans a declaration boundary.
//
// Exported because it is what a SURFACE renders in place of the raw header. The @@ coordinates are
// wire syntax: the console already prints line numbers in its gutters, so they are redundant there,
// and they are unreadable everywhere. What a reader wants from a hunk heading is where they are and
// what they are inside of.
func DeclarationOf(header string) string {
	_, after, ok := strings.Cut(header, "@@")
	if !ok {
		return ""
	}
	_, decl, ok := strings.Cut(after, "@@")
	if !ok {
		return ""
	}
	return strings.TrimSpace(decl)
}

// declarationLine finds the first hunk still declaring what the remark sat in, and returns the
// line that hunk starts at.
//
// The START of the hunk rather than a line within it, because the remark's own line is gone by the
// time this is reached: pointing at the declaration is a claim magus can support, and pointing at a
// line inside it would be the guess this whole ladder exists to refuse.
func declarationLine(a types.CommentAnchor, hunks []Hunk) (int, bool) {
	if a.Declaration == "" {
		return 0, false
	}
	for _, h := range hunks {
		if h.Declaration == a.Declaration {
			return h.NewStart, true
		}
	}
	return 0, false
}

// contextScore counts how many of the remembered surrounding lines still surround this candidate.
//
// A count rather than a threshold, because the caller is choosing BETWEEN candidates rather than
// deciding whether one is good enough. A candidate with no context match still wins over no
// candidate at all: the quote itself matched, and that is more than the line number offered.
func contextScore(a types.CommentAnchor, body []string, idx int) int {
	score := 0
	for n, want := range a.Before {
		at := idx - len(a.Before) + n
		if at >= 0 && body[at] == want {
			score++
		}
	}
	for n, want := range a.After {
		at := idx + 1 + n
		if at < len(body) && body[at] == want {
			score++
		}
	}
	return score
}

// newSideBody is the hunk's NEW-side lines with their diff markers stripped: what the file looks
// like after the change, which is the only side a remark's line number addresses.
func newSideBody(h Hunk) []string {
	out := make([]string, 0, len(h.Lines))
	for _, l := range h.Lines {
		if strings.HasPrefix(l, "-") || strings.HasPrefix(l, "\\") {
			continue
		}
		out = append(out, strings.TrimPrefix(strings.TrimPrefix(l, "+"), " "))
	}
	return out
}

// newSideNumber maps an index within newSideBody back to the file's new-side line number.
func newSideNumber(h Hunk, idx int) int { return h.NewStart + idx }

// newSideLine finds the hunk covering a new-side line and that line's index within its body.
func newSideLine(hunks []Hunk, line int) (Hunk, int, bool) {
	for _, h := range hunks {
		body := newSideBody(h)
		idx := line - h.NewStart
		if idx >= 0 && idx < len(body) {
			return h, idx, true
		}
	}
	return Hunk{}, 0, false
}

func window(body []string, from, to int) []string {
	from = max(from, 0)
	to = min(to, len(body))
	if from >= to {
		return nil
	}
	return append([]string(nil), body[from:to]...)
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
