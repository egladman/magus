// Package commentdash defines an analyzer that reports a Go comment using a
// spaced hyphen where prose wants an em-dash.
//
// The fix is always a colon, a semicolon, or parentheses. The rule exists
// because the aside reads as an em-dash to a human, and a repo that bans the
// em-dash in prose has banned this too; nothing in the Go toolchain objects, so
// only a reviewer ever catches it.
//
// Three shapes carry a spaced hyphen that is NOT prose punctuation, and each is
// exempt: a doc-list bullet, which gofmt itself formats that way and would fight
// a linter over; an indented preformatted block, which is where command examples
// live; and a span inside backticks, which is a literal. The exemptions are
// measured rather than assumed, and the counts are in readme.md.
//
// Indentation is read the way [go/doc/comment] reads it: the comment marker's
// own leading space is stripped, the group's common indent comes off, and a line
// still indented is preformatted unless it is a list item or the continuation of
// one. That model is what keeps a wrapped list item, which is prose, apart from
// a command example, which is not.
//
// The analyzer depends on no linter runner. The golangci-lint plugin lives in
// the plugin subpackage.
package commentdash

import (
	"fmt"
	"go/ast"
	"go/token"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const doc = `check that a comment does not use a spaced hyphen as an em-dash aside

A comment reading "the run is cached - so nothing executes" spells an em-dash
with a hyphen. Write a colon, a semicolon, or parentheses instead.

Exempt: a doc-list bullet, since gofmt formats list items that way; an indented
preformatted block, where command examples live; a span inside backticks; and a
hyphen with a digit on each side, which is arithmetic or a range rather than
punctuation.`

// asideMessage names the fix rather than the sin, for the reason testlayout's
// external-package message does: the wrong thing compiles, passes, and reads
// fine to everyone except the person who set the rule.
const asideMessage = `comment uses " - " as an em-dash aside; write a colon, a semicolon, or parentheses instead. ` +
	"A spaced hyphen that belongs to a literal goes in backticks or an indented block, both of which are exempt."

const wrappedMessage = `comment line ends in " -", carrying an em-dash aside onto the next line; ` +
	"write a colon, a semicolon, or parentheses instead and rewrap the paragraph."

// Options configures the analyzer returned by [New]. The json tags are
// golangci-lint's settings block: the plugin decodes straight into this struct
// rather than keeping a parallel copy, so adding an option here cannot be
// silently dropped on the way in.
type Options struct {
	// Allow lists globs in [path/filepath.Match] syntax, matched against the base
	// name of a Go file whose comments are exempt.
	Allow []string `json:"allow"`

	// Wrapped extends the rule to a comment line ENDING in a spaced hyphen, which
	// is the same aside with its second half on the next line. Off by default
	// because the fix rewraps a paragraph rather than editing one line, so it is
	// worth sweeping separately. See readme.md for what it adds.
	Wrapped bool `json:"wrapped"`
}

// New returns an analyzer configured by opts, erroring on a malformed Allow glob.
//
// Checked here rather than at the point of use, which runs once per file: a
// config typo would lint clean until it reached a file holding an aside, then
// fail from somewhere unrelated to the mistake.
func New(opts Options) (*analysis.Analyzer, error) {
	for _, pattern := range opts.Allow {
		if _, err := filepath.Match(pattern, "probe"); err != nil {
			return nil, fmt.Errorf("commentdash: allow pattern %q: %w", pattern, err)
		}
	}

	return newAnalyzer(opts), nil
}

// Analyzer is the analyzer with no exempt globs and the wrapped half off. It
// takes its configuration at construction, so this one runs with the defaults;
// use [New] to change them.
var Analyzer = newAnalyzer(Options{})

func newAnalyzer(opts Options) *analysis.Analyzer {
	l := linter{allow: opts.Allow, wrapped: opts.Wrapped}

	return &analysis.Analyzer{Name: "commentdash", Doc: doc, Run: l.run}
}

type linter struct {
	allow   []string
	wrapped bool
}

func (l linter) run(pass *analysis.Pass) (any, error) {
	for _, f := range pass.Files {
		// A file with no position, synthesized or overlay-sourced, has no name to
		// match a glob against. Skipping beats dereferencing nil and panicking the
		// driver.
		tf := pass.Fset.File(f.Pos())
		if tf == nil {
			continue
		}

		if l.exempted(filepath.Base(tf.Name())) {
			continue
		}

		for _, group := range f.Comments {
			l.checkGroup(pass, group)
		}
	}

	return nil, nil
}

// exempted reports whether the file's base name matches a configured glob. The
// patterns were validated by [New], so a match error here would mean a pattern
// that changed after construction, and there is none.
func (l linter) exempted(name string) bool {
	for _, pattern := range l.allow {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}

	return false
}

// checkGroup reports at most one diagnostic per line of the group. One per line
// keeps the finding count comparable to a grep over the same tree, which is how
// the exemptions in readme.md were measured.
func (l linter) checkGroup(pass *analysis.Pass, group *ast.CommentGroup) {
	lines := groupLines(group)
	unindent(lines)

	inList := false

	for _, ln := range lines {
		if strings.TrimSpace(ln.text) == "" {
			// A blank line neither opens nor closes a list: go/doc/comment keeps a
			// list running across the blank line that separates loose items.
			continue
		}

		body := strings.TrimLeft(ln.text, " \t")
		indent := len(ln.text) - len(body)
		marker := listMarker(body)

		switch {
		case marker > 0:
			inList = true
		case indent > 0 && inList:
			// A wrapped list item is prose, not code, however deep it sits.
		case indent > 0:
			continue
		default:
			inList = false
		}

		// Scanning starts past the marker so the bullet's own hyphen is exempt while
		// a second one later in the item is still reported.
		at, message := l.scan(ln.text, indent+marker)
		if at < 0 {
			continue
		}

		pass.Report(analysis.Diagnostic{Pos: ln.pos + token.Pos(at), Message: message})
	}
}

// scan returns the byte offset of the first offending hyphen in text at or after
// start, with the message for it, or -1 when the line is clean.
func (l linter) scan(text string, start int) (int, string) {
	if start >= len(text) {
		return -1, ""
	}

	scan := blankBackticks(text)

	for i := start; i < len(scan); {
		j := strings.Index(scan[i:], " - ")
		if j < 0 {
			break
		}

		at := i + j + 1
		if !numeric(scan, at) {
			return at, asideMessage
		}

		i = at + 1
	}

	if !l.wrapped {
		return -1, ""
	}

	trimmed := strings.TrimRight(scan, " \t")
	if len(trimmed) < start+2 || trimmed[len(trimmed)-1] != '-' {
		return -1, ""
	}

	if b := trimmed[len(trimmed)-2]; b != ' ' && b != '\t' {
		return -1, ""
	}

	return len(trimmed) - 1, wrappedMessage
}

// numeric reports whether the hyphen at index at has a digit on each side, which
// makes it arithmetic or a range. Neither a colon nor a parenthesis is the fix
// for those, so reporting them would be noise a sweep has to hand-clear.
//
// A hyphen between IDENTIFIERS stays reported. `n - 1` is arithmetic too, but it
// is indistinguishable from prose without reading the surrounding code, and the
// tree this was measured on holds one of them against 4513 findings.
func numeric(s string, at int) bool {
	left := at - 1
	for left >= 0 && (s[left] == ' ' || s[left] == '\t') {
		left--
	}

	right := at + 1
	for right < len(s) && (s[right] == ' ' || s[right] == '\t') {
		right++
	}

	return left >= 0 && isDigit(s[left]) && right < len(s) && isDigit(s[right])
}

func isDigit(b byte) bool { return '0' <= b && b <= '9' }

// blankBackticks overwrites every backtick span with a character that carries no
// meaning to the scan, preserving length so the reported offset still points at
// the source byte.
//
// An unterminated backtick blanks to the end of the line rather than being
// ignored. A span that opens on one line and closes on the next therefore hides
// the first half and not the second, which is a false positive on the closing
// line; carrying the state across lines instead would let one stray backtick
// silence the rest of the comment, and a false negative in a linter is the one
// nobody ever reports.
func blankBackticks(s string) string {
	if !strings.ContainsRune(s, '`') {
		return s
	}

	b := []byte(s)

	for i := 0; i < len(b); {
		if b[i] != '`' {
			i++

			continue
		}

		end := i + 1
		for end < len(b) && b[end] != '`' {
			end++
		}

		if end < len(b) {
			end++
		}

		for ; i < end; i++ {
			b[i] = '#'
		}
	}

	return string(b)
}

// listMarker returns the length of the list marker at the head of body plus the
// whitespace after it, or 0 when body does not open a list item.
//
// The markers are the ones go/doc/comment recognizes. A marker with nothing
// after it is not an item, which is what keeps a line holding a bare hyphen from
// silencing the rest of the comment as list content.
func listMarker(body string) int {
	end := 0

	switch {
	case strings.HasPrefix(body, "-"), strings.HasPrefix(body, "*"), strings.HasPrefix(body, "+"):
		end = 1
	case strings.HasPrefix(body, "•"):
		end = len("•")
	default:
		for end < len(body) && isDigit(body[end]) {
			end++
		}

		if end == 0 || end >= len(body) || (body[end] != '.' && body[end] != ')') {
			return 0
		}

		end++
	}

	rest := body[end:]

	after := strings.TrimLeft(rest, " \t")
	if len(after) == len(rest) {
		return 0
	}

	return len(body) - len(after)
}

// line is one physical line of a comment group, with the file position of its
// first byte so a diagnostic can point at the hyphen itself.
type line struct {
	text string
	pos  token.Pos
}

// groupLines splits a comment group into physical lines with the comment markers
// removed, dropping the toolchain directives along the way.
//
// The single leading space comes off for the reason [go/ast.CommentGroup.Text]
// takes it off: it is the marker's padding, not indentation, and leaving it on
// would make every ordinary comment line look preformatted.
func groupLines(group *ast.CommentGroup) []line {
	var out []line

	for _, c := range group.List {
		if strings.HasPrefix(c.Text, "//") {
			body := c.Text[2:]
			if isDirective(body) {
				continue
			}

			out = append(out, trimMarkerSpace(line{text: body, pos: c.Pos() + 2}))

			continue
		}

		parts := strings.Split(c.Text, "\n")
		offset := 0

		for i, part := range parts {
			ln := line{text: part, pos: c.Pos() + token.Pos(offset)}
			if i == 0 {
				ln.text, ln.pos = ln.text[2:], ln.pos+2
			}

			if i == len(parts)-1 {
				ln.text = strings.TrimSuffix(ln.text, "*/")
			}

			out = append(out, trimMarkerSpace(ln))
			offset += len(part) + 1
		}
	}

	return out
}

func trimMarkerSpace(ln line) line {
	if strings.HasPrefix(ln.text, " ") {
		ln.text, ln.pos = ln.text[1:], ln.pos+1
	}

	return ln
}

// isDirective mirrors go/ast's unexported isDirective, which is unreachable from
// here. A directive is read by the toolchain rather than by a person, so its
// spacing is not this rule's business.
func isDirective(c string) bool {
	if strings.HasPrefix(c, "line ") || strings.HasPrefix(c, "extern ") || strings.HasPrefix(c, "export ") {
		return true
	}

	colon := strings.Index(c, ":")
	if colon <= 0 || colon+1 >= len(c) {
		return false
	}

	for i := range colon {
		if b := c[i]; b != '_' && !isDigit(b) &&
			!('a' <= b && b <= 'z') && !('A' <= b && b <= 'Z') {
			return false
		}
	}

	return true
}

// unindent removes the whitespace prefix every non-blank line shares, matching
// what go/doc/comment does before deciding which lines are preformatted. Without
// it a doc comment whose every line is indented would read as one long code
// block.
func unindent(lines []line) {
	prefix := ""
	found := false

	for _, ln := range lines {
		if strings.TrimSpace(ln.text) == "" {
			continue
		}

		lead := ln.text[:len(ln.text)-len(strings.TrimLeft(ln.text, " \t"))]
		if !found {
			prefix, found = lead, true

			continue
		}

		prefix = commonPrefix(prefix, lead)
	}

	if prefix == "" {
		return
	}

	for i, ln := range lines {
		if strings.HasPrefix(ln.text, prefix) {
			lines[i].text = ln.text[len(prefix):]
			lines[i].pos = ln.pos + token.Pos(len(prefix))
		}
	}
}

func commonPrefix(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}

	return a[:n]
}
