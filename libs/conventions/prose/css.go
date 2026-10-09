package prose

import (
	"slices"
	"strings"
)

// cssPlace says where a comment sits, which decides its line budget.
type cssPlace string

const (
	// placeFile is the header above the first rule, set apart from it by a
	// blank line.
	placeFile cssPlace = "file"
	// placeRule sits directly above a rule, an at-rule or a nested rule.
	placeRule cssPlace = "rule"
	// placeSection stands alone between top-level rules.
	placeSection cssPlace = "section"
	// placeDeclaration sits directly above a declaration in a block.
	placeDeclaration cssPlace = "declaration"
	// placeInline stands alone inside a block, or inside a statement.
	placeInline cssPlace = "inline"
	// placeTrailing follows code on the line the code ends on.
	placeTrailing cssPlace = "trailing"
)

// cssComment is one comment of a stylesheet with the code it speaks of.
type cssComment struct {
	place cssPlace
	// start is the 1-based line the comment opens on; lines is how many source
	// lines it spans.
	start, lines int
	// text holds one entry per source line, joined by newlines, with the
	// delimiters, the leading asterisks and the rule lines of a banner gone.
	text string
	// target is the rule header or declaration the comment precedes, or the
	// declaration a trailing comment follows. owner is the header of the block
	// holding that code, "" at the top level.
	target, owner string
	// inside holds the declarations of a small block the comment sits above,
	// which the comment may repeat as well as the header.
	inside string
	// block is the index in the file's statements of the block this comment's
	// lines are charged to, or -1 at the top level.
	block int
}

// cssStmt is one statement of a stylesheet: a rule header, which opens a block,
// or a declaration or at-rule statement.
type cssStmt struct {
	text       string
	start, end int
	block      bool
	parent     int
	// decls counts the statements directly inside a block.
	decls int
}

// cssItem is a comment or a statement in file order.
type cssItem struct {
	comment    bool
	idx        int
	start, end int
}

// rawComment is a comment as written, before it is placed.
type rawComment struct {
	body       string
	start, end int
	// code is set when code opened earlier on the line the comment opens on.
	code bool
	// inStmt is set when the comment sits inside a statement not yet ended.
	inStmt bool
	parent int
}

type cssFile struct {
	stmts    []cssStmt
	comments []cssComment
}

// directivePrefixes open a comment a tool reads: it is configuration, not
// prose, and nothing here judges it.
var directivePrefixes = []string{"stylelint-", "biome-", "prettier-ignore", "eslint-", "# sourceMappingURL", "!"}

// readCSS splits src into statements and comments and places each comment.
func readCSS(src string) cssFile {
	var (
		f        cssFile
		items    []cssItem
		raws     []rawComment
		rawAt    = map[int]int{}
		stack    []int
		pend     strings.Builder
		pendFrom int
		line     = 1
		lastCode int
	)

	parent := func() int {
		if len(stack) == 0 {
			return -1
		}

		return stack[len(stack)-1]
	}

	emit := func(block bool, end int) {
		text := strings.TrimSpace(pend.String())
		pend.Reset()

		if text == "" {
			return
		}

		p := parent()
		f.stmts = append(f.stmts, cssStmt{text: text, start: pendFrom, end: end, block: block, parent: p})

		if p >= 0 {
			f.stmts[p].decls++
		}

		items = append(items, cssItem{idx: len(f.stmts) - 1, start: pendFrom, end: end})

		if block {
			stack = append(stack, len(f.stmts)-1)
		}
	}

	code := func(b byte) {
		if pend.Len() == 0 {
			pendFrom = line
		}

		pend.WriteByte(b)

		lastCode = line
	}

	for i := 0; i < len(src); i++ {
		c := src[i]

		switch {
		case c == '\n':
			line++

			if pend.Len() > 0 && !strings.HasSuffix(pend.String(), " ") {
				pend.WriteByte(' ')
			}
		case c == ' ' || c == '\t' || c == '\r':
			if pend.Len() > 0 && !strings.HasSuffix(pend.String(), " ") {
				pend.WriteByte(' ')
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			stop := len(src)

			if end >= 0 {
				stop = i + 2 + end + 2
			}

			body := src[i+2 : max(i+2, stop-2)]
			if end < 0 {
				body = src[i+2:]
			}

			span := strings.Count(src[i:stop], "\n")
			rawAt[len(items)] = len(raws)
			raws = append(raws, rawComment{
				body: body, start: line, end: line + span, code: lastCode == line,
				inStmt: pend.Len() > 0, parent: parent(),
			})
			items = append(items, cssItem{comment: true, idx: len(raws) - 1, start: line, end: line + span})

			line += span
			i = stop - 1
		case c == '"' || c == '\'':
			code(c)

			for i++; i < len(src) && src[i] != c; i++ {
				if src[i] == '\\' && i+1 < len(src) {
					code(src[i])
					i++
				}

				if src[i] == '\n' {
					line++
				}

				code(src[i])
			}

			if i < len(src) {
				code(src[i])
			}
		case c == '(' && strings.HasSuffix(strings.ToLower(pend.String()), "url"):
			// An unquoted url() runs to its closing parenthesis, whatever it holds.
			j := strings.IndexByte(src[i:], ')')
			if j < 0 {
				j = len(src) - i - 1
			}

			for _, b := range []byte(src[i : i+j+1]) {
				if b == '\n' {
					line++
				}

				code(b)
			}

			i += j
		case c == '{':
			emit(true, line)

			lastCode = line
		case c == '}':
			emit(false, line)

			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}

			lastCode = line
		case c == ';':
			emit(false, line)

			lastCode = line
		default:
			code(c)
		}
	}

	emit(false, line)

	f.comments = placeComments(&f, items, raws, rawAt)

	return f
}

// placeComments turns each judged comment into a [cssComment] with its place
// and the code it speaks of.
func placeComments(f *cssFile, items []cssItem, raws []rawComment, rawAt map[int]int) []cssComment {
	var out []cssComment

	seenCode := false
	last := -1

	for n, it := range items {
		if !it.comment {
			seenCode = true
			last = it.idx

			continue
		}

		raw := raws[rawAt[n]]

		text := normalizeComment(raw.body)
		if strings.TrimSpace(text) == "" || isDirective(text) {
			continue
		}

		c := cssComment{start: raw.start, lines: spokenLines(text), text: text, block: raw.parent}

		switch {
		case raw.code && !raw.inStmt && last >= 0 && f.stmts[last].end == raw.start:
			c.place = placeTrailing
			c.target = f.stmts[last].text
			c.owner = headerOf(f, f.stmts[last].parent)

			if f.stmts[last].block {
				c.block = last
			} else {
				c.block = f.stmts[last].parent
			}
		case raw.inStmt || raw.code:
			c.place = placeInline
			c.owner = headerOf(f, raw.parent)
		default:
			c.place = standalonePlace(f, items, n, raw, seenCode)

			if next, ok := adjacentStmt(f, items, n); ok {
				s := f.stmts[next]
				c.target = s.text
				c.owner = headerOf(f, s.parent)

				if s.block {
					c.block = next
					c.inside = declarationsOf(f, next)
				}
			} else {
				c.owner = headerOf(f, raw.parent)
			}
		}

		out = append(out, c)
	}

	return out
}

// standalonePlace places a comment on a line of its own.
func standalonePlace(f *cssFile, items []cssItem, n int, raw rawComment, seenCode bool) cssPlace {
	next, attached := adjacentStmt(f, items, n)

	switch {
	case attached && f.stmts[next].block:
		return placeRule
	case attached && raw.parent >= 0:
		return placeDeclaration
	case attached:
		return placeRule
	case !seenCode && raw.parent < 0:
		return placeFile
	case raw.parent < 0:
		return placeSection
	}

	return placeInline
}

// adjacentStmt returns the statement that starts on the line after item n, or
// on its last line's next line break, with no blank line between.
func adjacentStmt(f *cssFile, items []cssItem, n int) (int, bool) {
	if n+1 >= len(items) || items[n+1].comment {
		return 0, false
	}

	if items[n+1].start > items[n].end+1 {
		return 0, false
	}

	return items[n+1].idx, true
}

// maxRepeatable is the most declarations a block can hold and still be read
// whole by a one-line comment above it.
const maxRepeatable = 4

// declarationsOf joins the declarations directly inside block, or returns "" when
// the block is too large for one line to repeat.
func declarationsOf(f *cssFile, block int) string {
	var decls []string

	for _, s := range f.stmts {
		if s.parent == block && !s.block {
			decls = append(decls, s.text)
		}
	}

	if len(decls) > maxRepeatable {
		return ""
	}

	return strings.Join(decls, " ")
}

func headerOf(f *cssFile, block int) string {
	if block < 0 {
		return ""
	}

	return f.stmts[block].text
}

func isDirective(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range directivePrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}

	return false
}

// normalizeComment returns the comment body as prose: one entry per source
// line, each trimmed of its indent and leading asterisk, a rule line or a
// banner's dashes gone. An item's wrapped lines keep a two-space indent so the
// shared reader sees one list item, not a paragraph break.
func normalizeComment(body string) string {
	raw := strings.Split(body, "\n")
	out := make([]string, len(raw))

	listIndent, inList := 0, false

	for i, ln := range raw {
		indent := len(ln) - len(strings.TrimLeft(ln, " \t"))
		text := strings.TrimSpace(ln)

		if rest := strings.TrimLeft(text, "*"); rest != text && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			shifted := strings.TrimLeft(rest, " \t")
			indent += len(text) - len(shifted)
			text = shifted
		}

		text = trimBanner(text)

		switch {
		case text == "":
			out[i] = ""
		case listMarker(text) > 0:
			inList, listIndent = true, indent
			out[i] = text
		case inList && indent > listIndent:
			out[i] = "  " + text
		default:
			inList = false
			out[i] = text
		}
	}

	return strings.Join(out, "\n")
}

// spokenLines counts the lines of normalized comment text between its first and
// last line of words: the delimiters and the rule lines of a banner cost the
// reader nothing, a blank line inside it does.
func spokenLines(text string) int {
	return len(strings.Split(strings.Trim(text, "\n"), "\n"))
}

// trimBanner drops the runs of dashes, equals signs and asterisks that frame a
// heading, or all of a line that is nothing else.
func trimBanner(text string) string {
	isRule := func(r rune) bool { return r == '-' || r == '=' || r == '*' || r == '_' || r == '~' }

	if strings.TrimFunc(text, isRule) == "" && len(text) >= 3 {
		return ""
	}

	for _, run := range []string{"---", "===", "***", "___", "~~~"} {
		for strings.HasPrefix(text, run) {
			text = strings.TrimSpace(strings.TrimLeftFunc(text, isRule))
		}

		for strings.HasSuffix(text, run) {
			text = strings.TrimSpace(strings.TrimRightFunc(text, isRule))
		}
	}

	return text
}

// cssFindings judges one comment with the rules for [KindCSS], and moves each
// finding to the source line it came from. A finding with no line of its own
// sits on the line the comment opens.
func cssFindings(c cssComment) []Finding {
	lines := strings.Split(c.text, "\n")

	in := input{
		symbol: Symbol{Name: c.target, Owner: c.owner + " " + c.inside, Doc: c.text},
		kind:   KindCSS,
		prose:  readProse(lines, false),
		lines:  lines,
		css:    &c,
	}

	out := run(in)
	for i := range out {
		if out[i].Line == 0 {
			out[i].Line = c.start
		} else {
			out[i].Line += c.start - 1
		}
	}

	return out
}

// blockFindings charges each comment line to the block it speaks of and reports
// a block whose comments outweigh what it earns.
func blockFindings(f cssFile) []Finding {
	charged := map[int]int{}
	from := map[int]int{}

	for _, c := range f.comments {
		if c.block < 0 || c.place == placeSection || c.place == placeFile {
			continue
		}

		charged[c.block] += c.lines

		if _, ok := from[c.block]; !ok || c.start < from[c.block] {
			from[c.block] = c.start
		}
	}

	var out []Finding

	for block, n := range charged {
		earned := blockBudget(f.stmts[block].decls)
		if n <= earned {
			continue
		}

		out = append(out, Finding{
			Message: blockMessage(f.stmts[block].text, n, earned, f.stmts[block].decls),
			Match:   f.stmts[block].text, Line: f.stmts[block].start,
		})
	}

	slices.SortFunc(out, func(a, b Finding) int { return a.Line - b.Line })

	return out
}

// JudgeCSS judges the comments of a stylesheet by the rules for [KindCSS]. The
// reader attributes each comment to the rule block or declaration it precedes
// (or a trailing comment follows) and the rules read that code beside the
// prose: the comment budget by where the comment sits, the budget of a whole
// block, and a comment that only repeats the code it sits over. Findings come
// in [Rules] order, then in line order, each with its line in src.
func JudgeCSS(src string) []Finding {
	f := readCSS(strings.ReplaceAll(src, "\r\n", "\n"))

	var out []Finding

	for _, c := range f.comments {
		out = append(out, cssFindings(c)...)
	}

	for _, b := range blockFindings(f) {
		b.Rule = RuleBlockBudget
		out = append(out, b)
	}

	order := Rules()
	slices.SortStableFunc(out, func(a, b Finding) int {
		if d := slices.Index(order, a.Rule) - slices.Index(order, b.Rule); d != 0 {
			return d
		}

		return a.Line - b.Line
	})

	return out
}
