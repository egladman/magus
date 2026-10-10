package prose

import (
	"regexp"
	"slices"
	"strings"
)

// JudgeText runs the rules that apply to kind over text, a Markdown file or
// a pull request with its title on the first line and its description after
// it. Front matter, code (fenced, indented and in backticks), tables, HTML
// comments and tags, link targets and URLs are not prose and are never judged.
// Findings come in [Rules] order, then in the order their text appears, each
// with its line in text.
//
// KindDoc reads text as a doc comment, by the rules [Judge] applies to one.
// KindSkillSource renders text in both of a skill's forms first, and each
// finding's line is its line in the source.
func JudgeText(text string, kind Kind, opts ...Option) []Finding {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	o := collect(opts)

	switch kind {
	case KindDoc:
		return run(input{
			symbol: Symbol{Doc: text}, kind: kind, prose: readProse(raw, false), lines: raw, source: raw, opts: o,
		})
	case KindSkillSource:
		// The two forms are judged without the options, so the rules they name
		// are applied to what comes back.
		return slices.DeleteFunc(judgeSkillSource(strings.Join(raw, "\n")), func(f Finding) bool {
			return !o.keeps(f.Rule)
		})
	}

	lines := markdownProse(raw, kind != KindPullRequest && kind != KindReply)
	wrapCodeSpans(lines)
	prose := readProse(lines, true)

	if kind == KindPullRequest {
		// The title is a paragraph of its own, however close the description
		// starts below it.
		for i := range prose {
			if prose[i].line > 1 {
				prose[i].opens, prose[i].paragraph = true, true

				break
			}
		}
	}

	return run(input{kind: kind, prose: prose, lines: lines, source: raw, opts: o})
}

var (
	linkDefinition = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:\s`)
	thematicBreak  = regexp.MustCompile(`^ {0,3}(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	tableDivider   = regexp.MustCompile(`^\s*\|?\s*:?-+:?\s*(?:\|\s*:?-+:?\s*)+\|?\s*$`)
	quoteMarker    = regexp.MustCompile(`^ {0,3}(?:>[ \t]?)+`)
	linkTarget     = regexp.MustCompile(`\]\((?:[^()\s]|\([^()\s]*\))*(?:\s+"[^"]*")?\)`)
	autolink       = regexp.MustCompile(`<(?:https?|mailto):[^>\s]*>`)
	bareURL        = regexp.MustCompile(`\bhttps?://[^\s)>\]]+`)
	htmlTag        = regexp.MustCompile(`</?[A-Za-z][A-Za-z0-9-]*(?:\s[^<>]*)?/?>`)
)

// markdownProse returns raw with every line that is not prose blanked and the
// markup that is not prose cut from the rest, one entry per line so a finding
// keeps its line. Code stays for readProse to drop, since only it knows a list
// item's indented continuation from an indented block.
func markdownProse(raw []string, frontMatter bool) []string {
	lines := slices.Clone(raw)
	if frontMatter {
		blankFrontMatter(lines)
	}

	blankComments(lines)
	blankTables(lines)

	for i, ln := range lines {
		if linkDefinition.MatchString(ln) || thematicBreak.MatchString(ln) {
			lines[i] = ""

			continue
		}

		ln = quoteMarker.ReplaceAllString(ln, "")
		ln = linkTarget.ReplaceAllString(ln, "]")
		ln = autolink.ReplaceAllString(ln, "")
		ln = bareURL.ReplaceAllString(ln, "")
		lines[i] = htmlTag.ReplaceAllString(ln, "")
	}

	return lines
}

// wrapCodeSpans carries a code span that wraps across a line ending onto the
// lines it continues on, as CommonMark reads one, so the per-line mask that
// blanks a span blanks all of it. Each continued line opens with a backtick in
// place of its first character, which is code, and a line that only closes the
// span loses that backtick, so its tail reads as prose. A span still open at
// the end of its paragraph is left to the per-line mask.
func wrapCodeSpans(lines []string) {
	fenced := unfenced(lines)

	for i := 0; i < len(fenced); i++ {
		if !endsInSpan(fenced[i], false) {
			continue
		}

		end := spanEnd(fenced, i)
		if end < 0 {
			continue
		}

		for j := i + 1; j <= end; j++ {
			body := strings.TrimLeft(lines[j], " \t")
			at := len(lines[j]) - len(body)

			mark := "`"
			if body[0] == '`' {
				mark = "#"
			}

			lines[j] = lines[j][:at] + mark + lines[j][at+1:]
		}

		i = end
	}
}

// spanEnd returns the line after open that closes the span open leaves open,
// or -1 when its paragraph, list item or heading ends first.
func spanEnd(lines []string, open int) int {
	for j := open + 1; j < len(lines); j++ {
		body := strings.TrimLeft(lines[j], " \t")
		if body == "" || listMarker(body) > 0 || headingMarker(body) > 0 {
			return -1
		}

		if !endsInSpan(body, true) {
			return j
		}
	}

	return -1
}

// endsInSpan reports whether line, read from inside a span or not, ends
// inside one.
func endsInSpan(line string, inside bool) bool {
	return inside != (strings.Count(line, "`")%2 == 1)
}

// blankFrontMatter blanks a YAML block opening the file, all but the value of
// its description: search results and link previews show that sentence before
// anything on the page, so it is held to the same rules.
func blankFrontMatter(lines []string) {
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return
	}

	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			for j := 0; j <= i; j++ {
				lines[j] = descriptionValue(lines[j])
			}

			return
		}
	}
}

// descriptionValue is the value of a one-line `description:` key without its
// YAML quotes, which would otherwise read as a quoted mention, or "" for any
// other front matter line.
func descriptionValue(line string) string {
	v, ok := strings.CutPrefix(line, "description:")
	if !ok {
		return ""
	}

	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
		v = v[1 : len(v)-1]
	}

	return v
}

// blankComments blanks every HTML comment, across lines, and keeps the text
// either side of one.
func blankComments(lines []string) {
	open := false

	for i, ln := range lines {
		var kept strings.Builder

		for ln != "" {
			if open {
				end := strings.Index(ln, "-->")
				if end < 0 {
					ln = ""

					continue
				}

				ln, open = ln[end+len("-->"):], false

				continue
			}

			start := strings.Index(ln, "<!--")
			if start < 0 {
				kept.WriteString(ln)

				break
			}

			kept.WriteString(ln[:start])
			ln, open = ln[start+len("<!--"):], true
		}

		lines[i] = kept.String()
	}
}

// blankTables blanks a table: every line opening with a pipe, and a table
// written without leading pipes from its header to the blank line after it.
// A cell is a label or a value, not a sentence, and the rules read sentences.
func blankTables(lines []string) {
	for i, ln := range lines {
		if strings.HasPrefix(strings.TrimSpace(ln), "|") {
			lines[i] = ""
		}

		if !tableDivider.MatchString(ln) || !strings.Contains(ln, "|") {
			continue
		}

		if i > 0 && strings.Contains(lines[i-1], "|") {
			lines[i-1] = ""
		}

		for j := i; j < len(lines) && strings.Contains(lines[j], "|"); j++ {
			lines[j] = ""
		}
	}
}

// blankQuoted overwrites each double-quoted span of s, quotes included, the
// way blankBackticks overwrites a code span: quoting a phrase mentions it
// rather than uses it, which is how a page documents the words a rule refuses.
func blankQuoted(s string) string {
	if !strings.Contains(s, `"`) {
		return s
	}

	b := []byte(s)

	for i := 0; i < len(b); i++ {
		if b[i] != '"' {
			continue
		}

		end := strings.IndexByte(s[i+1:], '"')
		if end < 0 {
			break
		}

		for j := i; j <= i+1+end; j++ {
			b[j] = '#'
		}

		i += 1 + end
	}

	return string(b)
}

// mentionsMasked blanks code spans and quoted spans: what a written rule must
// never read as the author's own words.
func mentionsMasked(s string) string { return blankQuoted(blankBackticks(s)) }

// mentions returns the mask filler and terms read kind through. Written
// text reads a quoted word as a mention; a doc comment keeps reading it as used,
// as it always has.
func mentions(kind Kind) func(string) string {
	if kind == KindDoc {
		return blankBackticks
	}

	return mentionsMasked
}
