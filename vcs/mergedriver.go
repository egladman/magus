package vcs

import "strings"

// replaceManagedSection replaces the begin..end marker section with newSection, or
// appends it when the text holds none.
//
// begin matches a PREFIX of the opening line, not the whole line, so a section written by
// an older magus with different banner wording is still found and rewritten in place.
//
// It collapses every managed section, not just the first. The end marker used to be
// located by a plain Index over the whole text, which finds the FIRST section's end even
// when the matched begin sits further down; that computed endIdx < startIdx, failed the
// ordering test, and appended instead - once per invocation. A real .gitattributes
// reached four stacked sections this way, each still applying merge=magus for globs the
// workspace had dropped. Searching for end after begin stops the accumulation; sweeping
// the rest heals a file that already accumulated.
func replaceManagedSection(text, newSection, begin, end string) string {
	spans := managedSpans(text, begin, end)

	// head is everything before the section's position, body everything after it with
	// any later duplicates removed. With no section present the whole text is head, so
	// the section lands at the end.
	head, body := text, ""
	if len(spans) > 0 {
		head = text[:spans[0].start]
		var rest strings.Builder
		prev := spans[0].end
		for _, s := range spans[1:] {
			rest.WriteString(text[prev:s.start])
			prev = s.end
		}
		rest.WriteString(text[prev:])
		body = rest.String()
	}

	// One blank line separates the section from its surroundings on both paths.
	// Normalizing both the same way makes a repeated write converge: they disagreed
	// before, so the first write appended with a blank line and later ones replaced
	// without it, rewriting the file forever.
	head = strings.TrimRight(head, "\n")
	if head != "" {
		head += "\n\n"
	}
	body = strings.TrimLeft(body, "\n")
	if body != "" {
		body = "\n" + body
	}
	return head + newSection + body
}

// span is one managed section's half-open byte range, from the start of its begin line
// through the newline ending its end line.
type span struct{ start, end int }

// managedSpans locates every begin..end section in order. An unterminated begin (a
// truncated or hand-edited file) is skipped rather than swallowing the remainder.
func managedSpans(text, begin, end string) []span {
	var spans []span
	for offset := 0; offset < len(text); {
		rel := strings.Index(text[offset:], begin)
		if rel < 0 {
			break
		}
		start := offset + rel
		relEnd := strings.Index(text[start:], end)
		if relEnd < 0 {
			break
		}
		stop := start + relEnd + len(end)
		if nl := strings.Index(text[stop:], "\n"); nl >= 0 {
			stop += nl + 1
		} else {
			stop = len(text)
		}
		spans = append(spans, span{start: start, end: stop})
		offset = stop
	}
	return spans
}
