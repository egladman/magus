package risk

import (
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/spells"
)

// CommentLine is one source line of a comment, its markers removed.
type CommentLine struct {
	// Line is 1-based.
	Line int
	// Col is the 1-based column, in runes as Vale counts them, of Text's first rune.
	Col  int
	Text string
}

// CommentBlock is the unit the prose budgets judge: a run of own-line line comments on
// consecutive lines with the same opener, one block comment, or one trailing comment.
type CommentBlock struct {
	Path  string
	Lines []CommentLine
}

// CommentBlocks returns path's comment blocks in source order, read with the declared
// syntax of its language. Directive comments are code and never appear.
func CommentBlocks(path, src string, syn spells.CommentSyntax) []CommentBlock {
	starts := lineStarts(src)
	var blocks []CommentBlock
	var prev commentSpan
	for i, s := range commentSpans(src, syn) {
		lines := spanLines(src, s, starts)
		joins := i > 0 && s.closer == "" && prev.closer == "" && s.ownLine && prev.ownLine &&
			s.opener == prev.opener && len(blocks) > 0 &&
			lines[0].Line == blocks[len(blocks)-1].Lines[len(blocks[len(blocks)-1].Lines)-1].Line+1
		if joins {
			last := &blocks[len(blocks)-1]
			last.Lines = append(last.Lines, lines...)
		} else {
			blocks = append(blocks, CommentBlock{Path: path, Lines: lines})
		}
		prev = s
	}
	return blocks
}

// spanLines splits one span's body into its source lines. A block comment's closer is
// dropped, and so is the `*` that decorates each line of a `/** ... */` block.
func spanLines(src string, s commentSpan, starts []int) []CommentLine {
	bodyStart := s.start + len(s.opener)
	bodyEnd := s.end
	if s.closer != "" && strings.HasSuffix(src[bodyStart:bodyEnd], s.closer) {
		bodyEnd -= len(s.closer)
	}
	line := lineAt(starts, s.start)
	texts := strings.Split(src[bodyStart:bodyEnd], "\n")
	out := make([]CommentLine, 0, len(texts))
	off := bodyStart
	for i, text := range texts {
		col := utf8.RuneCountInString(src[starts[line-1]:off]) + 1
		if s.closer != "" {
			trimmed := strings.TrimLeft(text, " \t")
			if i == 0 {
				trimmed = text
			}
			if strings.HasPrefix(trimmed, "*") {
				skip := len(text) - len(trimmed) + 1
				col += skip
				text = text[skip:]
			}
		}
		out = append(out, CommentLine{Line: line, Col: col, Text: strings.TrimRight(text, "\r")})
		off = nextLine(src, off)
		line++
	}
	return out
}

func nextLine(src string, off int) int {
	if n := strings.IndexByte(src[off:], '\n'); n >= 0 {
		return off + n + 1
	}
	return len(src)
}

// ProseBlocks keeps the prose of each block: a blank comment line and an indented one,
// a code example, are left out, and so is a block left with no line. Each kept line's
// Text is its source text byte for byte from its first word, and Col points there, so a
// prose linter's column on that text maps back to the source by addition.
func ProseBlocks(blocks []CommentBlock) []CommentBlock {
	var kept []CommentBlock
	for _, b := range blocks {
		var lines []CommentLine
		for _, l := range b.Lines {
			prose, lead, ok := proseLine(l.Text)
			if !ok {
				continue
			}
			lines = append(lines, CommentLine{Line: l.Line, Col: l.Col + lead, Text: prose})
		}
		if len(lines) > 0 {
			kept = append(kept, CommentBlock{Path: b.Path, Lines: lines})
		}
	}
	return kept
}

// proseLine is a comment line's prose and the bytes before it. A line indented past the
// one space after its marker is a code example, and an empty one carries no prose.
func proseLine(text string) (prose string, lead int, ok bool) {
	rest := strings.TrimPrefix(text, " ")
	if strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") {
		return "", 0, false
	}
	prose = strings.TrimRight(rest, " \t")
	if prose == "" {
		return "", 0, false
	}
	return prose, len(text) - len(rest), true
}

// lineStarts holds the byte offset each line of src starts at; index 0 is line 1.
func lineStarts(src string) []int {
	starts := []int{0}
	for i := range len(src) {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// lineAt is the 1-based line holding byte offset off.
func lineAt(starts []int, off int) int {
	lo, hi := 0, len(starts)
	for lo+1 < hi {
		mid := (lo + hi) / 2
		if starts[mid] <= off {
			lo = mid
		} else {
			hi = mid
		}
	}
	return lo + 1
}
