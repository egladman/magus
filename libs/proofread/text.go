package proofread

import (
	"regexp"
	"slices"
	"strings"
)

// JudgeText runs the rules that apply to kind over text, a Markdown file or
// a pull request with its title on the first line and its description after
// it. Front matter, code (fenced, indented and in backticks), tables, HTML
// comments and tags, link targets and URLs are not prose and are never judged.
// Neither is what a platform wrote into a Markdown text: a coding agent's or
// a review bot's footer and summary, a reply the bot wrote whole, the prompt
// a coding agent quotes, and a checkbox a template left for its author to
// tick.
// Findings come in [Rules] order, then in the order their text appears, each
// with its line in text.
//
// KindDocComment reads text as a doc comment, by the rules [Judge] applies to
// one. KindStylesheet reads text as a stylesheet and judges its comments.
// KindDiagnostic judges text as [JudgeMessage] does at its default cap.
// KindChangeDescription, KindCommitMessage and KindIssue read the first line
// as a title, a paragraph of its own. KindCommitMessage reads no headings.
// KindCLIHelp and KindToolDescription read plain text: no Markdown is
// stripped, only backticks mark a literal. KindReleaseNotes and KindChangelog read Markdown pages.
// KindSkillTemplate renders text in both of its forms first, and
// each finding's line is its line in the source.
func JudgeText(text string, kind Kind, opts ...Option) []Finding {
	return judgeText(text, kind, collect(opts))
}

func judgeText(text string, kind Kind, o options) []Finding {
	if k, ok := ParseKind(string(kind)); ok {
		kind = k // compat: see kindAliases
	}

	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")

	switch kind {
	case KindDocComment:
		return run(input{
			symbol: Symbol{Doc: text}, kind: kind, prose: readProse(raw, false), lines: raw, source: raw, opts: o,
		})
	case KindSkillTemplate:
		return judgeSkillSource(strings.Join(raw, "\n"), o)
	case KindStylesheet:
		return judgeStylesheet(strings.Join(raw, "\n"), o)
	case KindDiagnostic:
		return judgeMessage(text, 0, o)
	}

	switch kind {
	case KindCLIHelp:
		return judgeHelp(raw, o)
	case KindToolDescription:
		return judgeTool(raw, o)
	}

	raw = blankBoilerplate(raw)
	lines, prose := readMarkdown(raw, kind)

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

var (
	// agentTip is a setup tip, survey or setup offer a coding agent appends to
	// a pull request. It keys on the wording, not the product that wrote it.
	agentTip = regexp.MustCompile(`^\s*(?:\x{1F4A1}|\x{1F4AC}|\x{2728})?\s*(?:You can make [\w ]{1,30} smarter by setting up custom instructions|` +
		`Share your feedback on (?:(?:[\w-]+\s+){0,2})coding agent |Let (?:(?:[\w-]+\s+){0,2})coding agent \[set things up for you\]|` +
		`We'd love your input! Share your thoughts on (?:(?:[\w-]+\s+){0,2})coding agent )`)
	// editorFooter is the generated footer an editor's pull-request extension appends.
	editorFooter = regexp.MustCompile(`^\s*Created from [^[]+ via the \[[^]]+\]\(`)
	// templateTask is an open task-list item a template writes for its author
	// to tick: an attestation or a question to the author ("- [ ] I have added
	// tests", "- [ ] Have you done a self-review?"). An open item in the
	// author's own words is a plan or a to-do, and stays prose.
	templateTask = regexp.MustCompile(`^\s*(?:[-*+]\s+)?\[ \]\s*(?:\*\*[^*]+\*\*:?\s*|[^\w\s*]+\s*)?` +
		`(?i:I have|I've|I haven't|I am|I'm|I confirm|I agree|I certify|I understand|I read|Have you|Did you|` +
		`My code|My changes|This PR|This pull request|PR was|Any dependent|New and existing)\b`)
)

// agentFirewall is the summary of the warning a coding agent quotes into a
// pull request when its firewall blocked a host.
const agentFirewall = "Firewall rules blocked me from connecting to one or more addresses"

// botBlocks are paired HTML comments that explicitly mark auto-generated text.
var botBlocks = []struct{ open, close *regexp.Regexp }{
	{
		regexp.MustCompile(`(?i)<!--\s*This is an auto-generated (?:comment|description)\b`),
		regexp.MustCompile(`(?i)<!--\s*end of auto-generated (?:comment|description)\b`),
	},
}

// botReply is the comment an automated reviewer opens when it wrote the whole
// reply; no comment closes it.
var botReply = regexp.MustCompile(`(?i)^\s*<!--\s*This is an auto-generated reply\b`)

var (
	// agentSuffix is a coding agent's suffix marker before the prompt it quotes.
	agentSuffix = regexp.MustCompile(`<!--\s*START (?:(?:[A-Z0-9_-]+\s+){0,3})CODING AGENT SUFFIX\s*-->`)
	// promptSummary opens the collapsed block holding that prompt.
	promptSummary = regexp.MustCompile(`^\s*(?:<details>\s*)?<summary>\s*Original prompt\s*</summary>\s*$`)
	// chatPrompt is the italic line above the quoted prompt of a pull request
	// opened from a coding-agent chat.
	chatPrompt = regexp.MustCompile(`^\s*[*_]This pull request was created as a result of the following prompt ` +
		`from (?:(?:[\w-]+\s+){0,2})chat\.?[*_]\s*$`)
	// summaryLabel opens a summary pasted from a coding agent's pull request,
	// whose sentences cite the diff as [[1]](diffhunk://...).
	summaryLabel = regexp.MustCompile(`^\s*\[(?:(?:[\w-]+\s+){0,2})summary\]\s*$`)
	diffhunkLink = regexp.MustCompile(`\[\[\d+\]\]\(diffhunk://`)
	// templateCategory is an open task item a template lists as one choice
	// of a category ("- [ ] Public API: Breaking change (...)"): a capitalized
	// label, a capitalized option and a parenthetical ending the line. An
	// author's own to-do reads as a sentence after its label, in lower case.
	templateCategory = regexp.MustCompile(`^\s*[-*+]\s+\[ \]\s*[A-Z][\w-]*(?: [\w-]+){0,3}: ` +
		`[A-Z][\w-]*(?: [\w-]+){0,3} \([^()]*\)\s*$`)
)

// blankBoilerplate returns raw with what a platform wrote blanked, one entry
// per line so a finding keeps its line: the author did not write it and
// cannot be held to it. Each shape is the platform's own, matched whole, so
// an author's paragraph that mentions a tool stays prose.
func blankBoilerplate(raw []string) []string {
	lines := slices.Clone(raw)

	if at := nextFilled(lines, 0); at < len(lines) && botReply.MatchString(lines[at]) {
		clear(lines)

		return lines
	}

	for _, b := range botBlocks {
		for i := 0; i < len(lines); i++ {
			loc := b.open.FindStringIndex(lines[i])
			if loc == nil {
				continue
			}

			for j := i; j < len(lines); j++ {
				// A pair whose open and close are one comment closes after it.
				after := lines[j]
				if j == i {
					after = after[loc[1]:]
				}

				if b.close.MatchString(after) {
					clear(lines[i : j+1])
					i = j

					break
				}
			}
		}
	}

	for i := 0; i < len(lines); i++ {
		switch {
		case agentTip.MatchString(lines[i]), editorFooter.MatchString(lines[i]):
			lines[i] = ""
		case strings.Contains(lines[i], agentFirewall) && inQuote(lines[i]):
			i = blankQuote(lines, i)
		case templateTask.MatchString(lines[i]), templateCategory.MatchString(lines[i]):
			i = blankItem(lines, i)
		case agentSuffix.MatchString(lines[i]):
			i = blankOriginalPrompt(lines, i)
		case chatPrompt.MatchString(lines[i]):
			lines[i] = ""
			if j := nextFilled(lines, i+1); j < len(lines) && inQuote(lines[j]) {
				i = blankQuote(lines, j)
			}
		case summaryLabel.MatchString(lines[i]):
			i = blankPastedSummary(lines, i)
		}
	}

	return lines
}

// nextFilled is the first line from at on that is not blank, or len(lines).
func nextFilled(lines []string, at int) int {
	for at < len(lines) && strings.TrimSpace(lines[at]) == "" {
		at++
	}

	return at
}

// blankOriginalPrompt blanks the coding agent's suffix comment at line at
// and, when the collapsed original prompt follows it, every line through the
// </details> that closes the block. It returns the last line it blanked.
func blankOriginalPrompt(lines []string, at int) int {
	lines[at] = ""

	open := nextFilled(lines, at+1)
	if open < len(lines) && strings.TrimSpace(lines[open]) == "<details>" {
		open = nextFilled(lines, open+1)
	}

	if open >= len(lines) || !promptSummary.MatchString(lines[open]) {
		return at
	}

	// The prompt quotes an issue, which may hold a <details> block of its
	// own; only an unquoted tag belongs to the agent's block.
	depth := 1

	for j := open + 1; j < len(lines); j++ {
		if inQuote(lines[j]) {
			continue
		}

		depth += strings.Count(lines[j], "<details>") - strings.Count(lines[j], "</details>")
		if depth <= 0 {
			clear(lines[at : j+1])

			return j
		}
	}

	return at
}

// blankPastedSummary blanks a pasted agent summary its label at line at
// opens: every block through the paragraph after the last one citing a diff
// hunk, stopping at a heading, a thematic break or a comment. A label with no
// citation under it is blanked alone. It returns the last line it blanked.
func blankPastedSummary(lines []string, at int) int {
	last := -1

	for j := at + 1; j < len(lines); j++ {
		body := strings.TrimLeft(lines[j], " \t")
		if headingMarker(body) > 0 || thematicBreak.MatchString(lines[j]) || strings.HasPrefix(body, "<!--") {
			break
		}

		if diffhunkLink.MatchString(lines[j]) {
			last = j
		}
	}

	if last < 0 {
		lines[at] = ""

		return at
	}

	end := endOfBlock(lines, last)
	if next := nextFilled(lines, end+1); next < len(lines) && plainParagraph(lines[next]) {
		end = endOfBlock(lines, next)
	}

	clear(lines[at : end+1])

	return end
}

// plainParagraph reports whether line opens a paragraph: no heading, list
// item, quote, comment or break.
func plainParagraph(line string) bool {
	body := strings.TrimLeft(line, " \t")

	return headingMarker(body) == 0 && listMarker(body) == 0 && !inQuote(body) &&
		!strings.HasPrefix(body, "<") && !thematicBreak.MatchString(line)
}

// endOfBlock is the last line of the paragraph or list item holding line at.
func endOfBlock(lines []string, at int) int {
	for at+1 < len(lines) {
		body := strings.TrimLeft(lines[at+1], " \t")
		if body == "" || listMarker(body) > 0 || headingMarker(body) > 0 {
			break
		}

		at++
	}

	return at
}

func inQuote(line string) bool { return strings.HasPrefix(strings.TrimLeft(line, " "), ">") }

// blankQuote blanks the block quote holding line at and returns its last line.
func blankQuote(lines []string, at int) int {
	start, end := at, at
	for start > 0 && inQuote(lines[start-1]) {
		start--
	}

	for end+1 < len(lines) && inQuote(lines[end+1]) {
		end++
	}

	clear(lines[start : end+1])

	return end
}

// blankItem blanks the list item opening at line at, with the lines that
// continue it, and returns its last line.
func blankItem(lines []string, at int) int {
	end := at

	for end+1 < len(lines) {
		body := strings.TrimLeft(lines[end+1], " \t")
		if body == "" || listMarker(body) > 0 || headingMarker(body) > 0 {
			break
		}

		end++
	}

	clear(lines[at : end+1])

	return end
}

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
	if kind == KindDocComment {
		return blankBackticks
	}

	return mentionsMasked
}

// titled reports whether kind opens with a title line, a paragraph of its own
// however close the body starts below it.
func titled(kind Kind) bool {
	return kind == KindChangeDescription || kind == KindCommitMessage || kind == KindIssue
}
