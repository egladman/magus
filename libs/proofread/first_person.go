package proofread

import (
	"fmt"
	"regexp"
	"strings"
)

// authorNoteOpen is the line that opens an author's note: a blockquote whose
// first line is the GitHub alert marker [!AUTHOR]. The note runs to the last
// line of that blockquote.
var authorNoteOpen = regexp.MustCompile(`^ {0,3}>[ \t]?\[!AUTHOR\][ \t]*$`)

// authorNoteLines reports, for each line of source, whether it sits in an
// author's note. A note ends at the first line that does not open with ">",
// so a lazy continuation line is outside it.
func authorNoteLines(source []string) []bool {
	out := make([]bool, len(source))
	in := false

	for i, ln := range source {
		switch {
		case authorNoteOpen.MatchString(ln):
			in = true
		case !quoteMarker.MatchString(ln):
			in = false
		}

		out[i] = in
	}

	return out
}

// firstPersonSingular reports the author as I, me or my on a page that speaks
// for the project. Quoted words and code are mentions, a reader's question
// ("how do I build this?") among them, so is a heading, which a FAQ words as
// the reader asking. "I/O", "I-cache" and "works-on-my-machine" are no pronoun.
// Quotes are blanked over the joined paragraph, since a quote wraps lines.
func firstPersonSingular(in input) []Finding {
	var out []Finding

	note := authorNoteLines(in.source)

	for _, para := range paragraphs(in.prose, blankBackticks) {
		if para.head.heading {
			continue
		}

		text := blankQuoted(para.text)
		for _, at := range firstPerson.FindAllStringIndex(text, -1) {
			line := para.lineAt(at[0])
			if (line > 0 && line <= len(note) && note[line-1]) || compounded(text, at) || inLinkText(text, at[0]) {
				continue
			}

			m := text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop '%s', or move the sentence into an author's note (> [!AUTHOR]): "+
					"the rest of the page speaks for magus, not its author.", m),
				Match: m, Line: line,
			})
		}
	}

	return out
}

// compounded reports whether the word at sits inside a compound joined by a
// hyphen or a slash.
func compounded(text string, at []int) bool {
	return strings.HasPrefix(text[at[1]:], "/") || strings.HasPrefix(text[at[1]:], "-") ||
		strings.HasSuffix(text[:at[0]], "-")
}
