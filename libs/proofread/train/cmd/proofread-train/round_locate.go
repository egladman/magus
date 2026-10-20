package main

import (
	"regexp"
	"strings"

	"github.com/egladman/magus/libs/proofread"
)

// The context a labeler reads: this many bytes each side of the match, or
// of a whole text from its start.
const (
	contextBytes  = 300
	wholeBytes    = 600
	sentenceBytes = 600
)

// locateFiring places f's match in t's text and cuts its sentence and
// context; a finding with no match, or one not found as written, is about
// the paragraph its line opens or the whole text.
func locateFiring(t *sampleText, f proofread.Finding) firing {
	out := firing{t: t, f: f}
	text := t.text

	start, end, ok := locate(text, f)
	if ok {
		s, e := sentenceAround(text, start, end)
		out.scope = "sentence"
		out.sent = clip(text[s:e], sentenceBytes)
		out.key = normalize(text[s:e])

		lo, hi := runeFloor(text, max(0, start-contextBytes)), runeCeil(text, min(len(text), end+contextBytes))
		out.context = ellipsis(lo > 0) + text[lo:start] + "[[" + text[start:end] + "]]" + text[end:hi] + ellipsis(hi < len(text))

		return out
	}

	if f.Line > 0 {
		if s, e, found := blockAtLine(text, f.Line); found {
			out.scope = "paragraph"
			out.sent = clip(text[s:e], sentenceBytes)
			out.key = normalize(text[s:e])
			out.context = "[[" + clip(text[s:e], wholeBytes) + "]]"

			return out
		}
	}

	out.scope = "text"
	out.sent = clip(text, sentenceBytes)
	out.key = normalize(text)
	out.context = "[[" + clip(text, wholeBytes) + "]]"

	return out
}

// locate finds f.Match in text: at the line and column the finding gives,
// else first on its line, else first anywhere.
func locate(text string, f proofread.Finding) (int, int, bool) {
	if f.Match == "" {
		return 0, 0, false
	}

	if f.Line > 0 {
		if ls, le, found := lineSpan(text, f.Line); found {
			if f.Column > 0 {
				s := ls + f.Column - 1
				if s+len(f.Match) <= len(text) && text[s:s+len(f.Match)] == f.Match {
					return s, s + len(f.Match), true
				}
			}

			if i := strings.Index(text[ls:le], f.Match); i >= 0 {
				return ls + i, ls + i + len(f.Match), true
			}
		}
	}

	if i := strings.Index(text, f.Match); i >= 0 {
		return i, i + len(f.Match), true
	}

	return 0, 0, false
}

// lineSpan is the byte span of the 1-based line n of text, its newline
// excluded.
func lineSpan(text string, n int) (int, int, bool) {
	start := 0

	for range n - 1 {
		i := strings.IndexByte(text[start:], '\n')
		if i < 0 {
			return 0, 0, false
		}

		start += i + 1
	}

	end := len(text)
	if i := strings.IndexByte(text[start:], '\n'); i >= 0 {
		end = start + i
	}

	return start, end, true
}

// sentenceAround is the span of the sentences [start, end) overlaps, the
// same boundaries sentenceSpans cuts, so a firing's sentence compares equal
// to the same sentence split out of an exclude file.
func sentenceAround(text string, start, end int) (int, int) {
	lo, hi := -1, -1

	for _, sp := range sentenceSpans(text) {
		if sp[1] <= start || sp[0] >= end {
			continue
		}

		if lo < 0 {
			lo = sp[0]
		}

		hi = sp[1]
	}

	if lo < 0 {
		return start, end
	}

	return min(lo, start), max(hi, end)
}

// blockSpans cuts text into blocks: runs of non-blank lines, where a list
// item or a heading starts a new block and a heading is a block of its own.
func blockSpans(text string) [][2]int {
	var out [][2]int

	open := -1
	closeAt := func(end int) {
		if open >= 0 {
			out = append(out, [2]int{open, end})
			open = -1
		}
	}

	pos := 0
	for pos <= len(text) {
		end := len(text)
		if i := strings.IndexByte(text[pos:], '\n'); i >= 0 {
			end = pos + i
		}

		line := strings.TrimSpace(text[pos:end])

		switch {
		case line == "":
			closeAt(pos)
		case heading.MatchString(line):
			closeAt(pos)
			out = append(out, [2]int{pos, end})
		case startsListItem(line):
			closeAt(pos)

			open = pos
		case open < 0:
			open = pos
		}

		if end == len(text) {
			closeAt(end)

			break
		}

		pos = end + 1
	}

	return out
}

var (
	listItem = regexp.MustCompile(`^([-*+]|\d+[.)])\s`)
	heading  = regexp.MustCompile(`^#{1,6}(\s|$)`)
)

func startsListItem(line string) bool { return listItem.MatchString(line) }

// blockAtLine is the block that holds the 1-based line n.
func blockAtLine(text string, n int) (int, int, bool) {
	ls, _, ok := lineSpan(text, n)
	if !ok {
		return 0, 0, false
	}

	for _, b := range blockSpans(text) {
		if ls >= b[0] && ls < b[1] || ls == b[0] {
			return b[0], b[1], true
		}
	}

	return 0, 0, false
}

// sentenceSpans cuts each block at a '.', '!' or '?' that is followed by
// whitespace or ends the block, trimming each sentence's whitespace.
func sentenceSpans(text string) [][2]int {
	var out [][2]int

	add := func(s, e int) {
		for s < e && isSpace(text[s]) {
			s++
		}

		for e > s && isSpace(text[e-1]) {
			e--
		}

		if s < e {
			out = append(out, [2]int{s, e})
		}
	}

	for _, b := range blockSpans(text) {
		start := b[0]

		for i := b[0]; i < b[1]; i++ {
			if strings.IndexByte(".!?", text[i]) >= 0 && (i+1 == b[1] || isSpace(text[i+1])) {
				add(start, i+1)
				start = i + 1
			}
		}

		add(start, b[1])
	}

	return out
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func runeFloor(s string, i int) int {
	for i > 0 && i < len(s) && !isRuneStart(s[i]) {
		i--
	}

	return i
}

func runeCeil(s string, i int) int {
	for i < len(s) && !isRuneStart(s[i]) {
		i++
	}

	return i
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

// clip cuts s to at most n bytes on a rune boundary, marking a cut with "...".
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:runeFloor(s, n)] + "..."
}

func ellipsis(cut bool) string {
	if cut {
		return "..."
	}

	return ""
}
