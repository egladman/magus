package eval

import (
	"slices"
	"strings"

	"github.com/egladman/magus/libs/train/completion/fim"
)

// Scrubbed is a training text with the lines that repeat an eval text taken out.
type Scrubbed struct {
	Text    string
	Removed int
	// Reparsed is false when the original had FIM-eligible constructs and the scrubbed
	// text has none: the deletion broke the file's syntax, and its samples fall back.
	Reparsed bool
}

// maxScrubPasses bounds the rescans: a deletion can join two lines into a new window.
const maxScrubPasses = 4

// Scrub removes from text every line that overlaps a gram of an eval text, widened to the
// smallest FIM-eligible construct (statement, block, argument list) that holds the lines.
// The rest of the file stays. A text that equals an eval text is removed whole.
func Scrub(d *Decontaminator, language, text string) Scrubbed {
	out := Scrubbed{Text: text, Reparsed: true}
	had := len(fim.Constructs(language, text)) > 0
	for range maxScrubPasses {
		hits := d.Hits(out.Text)
		if len(hits) == 0 {
			break
		}
		constructs := fim.Constructs(language, out.Text)
		var cut []fim.Range
		for _, h := range hits {
			r := lineRange(out.Text, h)
			if c, ok := smallestHolding(constructs, r); ok {
				r = lineRange(out.Text, c)
			}
			cut = append(cut, r)
		}
		out.Text = remove(out.Text, cut)
	}
	out.Removed = len(text) - len(out.Text)
	if had && out.Text != "" && len(fim.Constructs(language, out.Text)) == 0 {
		out.Reparsed = false
	}
	return out
}

// lineRange widens r to whole lines, newline included.
func lineRange(text string, r fim.Range) fim.Range {
	start := strings.LastIndexByte(text[:r.Start], '\n') + 1
	end := r.End
	if i := strings.IndexByte(text[end:], '\n'); i >= 0 {
		end += i + 1
	} else {
		end = len(text)
	}
	return fim.Range{Start: start, End: end}
}

// smallestHolding is the shortest construct that contains r.
func smallestHolding(cs []fim.Range, r fim.Range) (fim.Range, bool) {
	var best fim.Range
	found := false
	for _, c := range cs {
		if c.Start <= r.Start && c.End >= r.End-1 && (!found || c.End-c.Start < best.End-best.Start) {
			best, found = c, true
		}
	}
	return best, found
}

// remove deletes the ranges from text.
func remove(text string, cut []fim.Range) string {
	slices.SortFunc(cut, func(a, b fim.Range) int { return a.Start - b.Start })
	var b strings.Builder
	at := 0
	for _, r := range cut {
		if r.Start > at {
			b.WriteString(text[at:r.Start])
		}
		at = max(at, r.End)
	}
	if at < len(text) {
		b.WriteString(text[at:])
	}
	return b.String()
}
