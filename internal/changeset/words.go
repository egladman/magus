package changeset

// Span is a half-open [Start, End) range of a line to emphasize. The zero Span means there is
// nothing to emphasize on that side; emphasize never returns a span that marks nothing, so the
// zero value is unambiguous.
//
// The UNIT depends on where the span came from, which is why both spellings are named at their
// source: emphasize returns BYTES, because that is what a Go caller slices a string with, and
// Row.Emph carries UTF-16 code units, because that is what the browser it is shipped to indexes
// by. byteSpan converts the second back into the first.
type Span struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Empty reports whether the span marks nothing.
func (s Span) Empty() bool { return s.End <= s.Start }

// byteSpan converts a UTF-16 span over text into the byte offsets a Go caller slices with.
//
// Out-of-range input yields an empty span rather than a panic: this converts a number that
// travelled, and a renderer must not crash on a line it was handed.
func byteSpan(text string, s Span) Span {
	if s.Empty() {
		return Span{}
	}
	start, end := -1, -1
	units := 0
	for i, r := range text {
		if units == s.Start && start < 0 {
			start = i
		}
		if units == s.End && end < 0 {
			end = i
		}
		units++
		if r > 0xFFFF {
			units++
		}
	}
	if units == s.Start && start < 0 {
		start = len(text)
	}
	if units == s.End && end < 0 {
		end = len(text)
	}
	if start < 0 || end < 0 || end <= start {
		return Span{}
	}
	return Span{Start: start, End: end}
}

// emphasize reports which PART of a changed line changed, as a span of the before side and a
// span of the after side.
//
// A line diff only says "this line is different". On a line where one identifier was renamed,
// one argument was added, or one string changed, that leaves the reader to spot the difference
// by eye across two nearly identical rows.
//
// The algorithm is deliberately NOT a full word-level diff. It takes the common prefix and the
// common suffix on TOKEN boundaries and emphasizes the span between them. That is exact when a
// line has one edit region - overwhelmingly the common case - and it degrades to "the whole
// middle changed" otherwise, which is true rather than merely plausible. A real LCS would be
// more precise on multi-region edits and would also, on the lines where it disagrees with the
// eye, produce confident nonsense.
//
// Two empty spans mean the lines are identical, one side is empty, or the two differ so
// completely that emphasizing everything would be noise rather than signal - in each case the
// row color already says all there is to say.
//
// Parse calls this and ships the result, so both surfaces read one answer. Do not add a
// second implementation: nothing checks two against each other, so they diverge in silence and
// the same changed line reads as two different changes depending on where it was opened.
//
// BOUNDARY SEMANTICS: the scan compares RUNES and the returned offsets are BYTES. Byte-wise
// scanning is not the same algorithm - "café" and "cafè" share a lead byte, and a byte-wise
// common prefix would end in the middle of a rune and hand back an offset that slices the line
// into invalid UTF-8. Bytes are what a Go caller slices a string with; the browser is handed
// UTF-16 offsets instead, converted where the row is built.
func emphasize(before, after string) (Span, Span) {
	if before == after || before == "" || after == "" {
		return Span{}, Span{}
	}

	b, bOff := runesOf(before)
	a, aOff := runesOf(after)
	limit := min(len(b), len(a))

	p := 0
	for p < limit && b[p] == a[p] {
		p++
	}
	// A prefix that ends inside a token is not a prefix worth keeping. When the two sides walk
	// back to different token starts there is no honest shared prefix at all, so take none.
	bStart, aStart := backToBoundary(b, p), backToBoundary(a, p)
	p = 0
	if bStart == aStart {
		p = bStart
	}

	s := 0
	for s < limit-p && b[len(b)-1-s] == a[len(a)-1-s] {
		s++
	}
	bEnd := max(p, forwardToBoundary(b, len(b)-s))
	aEnd := max(p, forwardToBoundary(a, len(a)-s))

	// If the changed span is the entire line on both sides, emphasis adds nothing the row
	// color did not already carry, so say so by returning nothing.
	if p == 0 && bEnd == len(b) && aEnd == len(a) {
		return Span{}, Span{}
	}
	return span(bOff, p, bEnd), span(aOff, p, aEnd)
}

// emphasisPair is one deleted line matched to the added line that replaced it, as positions in
// the hunk body both were read from.
type emphasisPair struct {
	Del int
	Add int
}

// pairForEmphasis matches deleted lines to added lines inside one run of changes.
//
// Pairing is strictly positional and only within a run of equal length. An unequal run means
// lines were added or removed rather than rewritten, and pairing across that boundary invents a
// correspondence the patch does not contain - which would emphasize the wrong half of two
// unrelated lines and read as a confident lie.
func pairForEmphasis(dels, adds []int) []emphasisPair {
	if len(dels) == 0 || len(dels) != len(adds) {
		return nil
	}
	out := make([]emphasisPair, 0, len(dels))
	for i := range dels {
		out = append(out, emphasisPair{Del: dels[i], Add: adds[i]})
	}
	return out
}

// isWordRune is ASCII-only on purpose: identifier runs stay
// whole so a rename highlights the whole name rather than the three letters it happens to share
// with the old one. A letter outside the class is therefore a token BOUNDARY, which is why
// "naive" and "naïve" split differently. Deliberate, not an oversight to correct with
// unicode.IsLetter: widening the class merges tokens a reader sees as separate words.
func isWordRune(r rune) bool {
	return r >= 'a' && r <= 'z' ||
		r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9' ||
		r == '_' || r == '$'
}

// backToBoundary walks an index back to the start of the token it lands inside, so a common
// prefix never cuts an identifier in half. An index at or past the end is already a boundary.
func backToBoundary(s []rune, i int) int {
	n := i
	for n > 0 && n < len(s) && isWordRune(s[n-1]) && isWordRune(s[n]) {
		n--
	}
	return n
}

// forwardToBoundary is the same for a suffix start.
func forwardToBoundary(s []rune, i int) int {
	n := i
	for n > 0 && n < len(s) && isWordRune(s[n]) && isWordRune(s[n-1]) {
		n++
	}
	return n
}

// runesOf returns s as runes alongside each rune's byte offset, with a trailing entry for
// len(s) so a half-open range ending past the last rune resolves.
func runesOf(s string) ([]rune, []int) {
	rs := make([]rune, 0, len(s))
	off := make([]int, 0, len(s)+1)
	for i, r := range s {
		rs = append(rs, r)
		off = append(off, i)
	}
	return rs, append(off, len(s))
}

// span converts a rune-indexed range into the byte range a caller slices with.
func span(off []int, start, end int) Span {
	if end <= start {
		return Span{}
	}
	return Span{Start: off[start], End: off[end]}
}
