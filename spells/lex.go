package spells

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"
)

// LexKind is what a LexSpan holds.
type LexKind int

const (
	LexComment LexKind = iota + 1
	LexString
	// LexDirective is a comment a tool reads, such as //nolint. Editing one
	// is never comment-only, yet it holds no decision.
	LexDirective
)

// LexSpan is one comment, directive or string of a source: src[Start:End],
// opener through closer, or to the end of the line for a line comment.
type LexSpan struct {
	Start, End int
	Kind       LexKind
	// Closer is a block comment's closing token, "" for anything else.
	Closer string
	// OwnLine: only whitespace precedes the comment's opener on its line.
	OwnLine bool
}

// Spans returns src's comment, directive and string spans in order, read by
// the declared syntax. A comment token inside a string is content, and a
// quote inside a comment is comment. Block comments nest only where declared.
// An unterminated string or block comment runs to the end of src.
func (s CommentSyntax) Spans(src string) []LexSpan {
	lx := newLexer(src, s)
	for lx.i < len(src) {
		lx.step()
	}
	return lx.spans
}

type lexer struct {
	src         string
	syn         CommentSyntax
	quotes      []Quote
	spans       []LexSpan
	i           int
	lineHasCode bool
}

func newLexer(src string, syn CommentSyntax) *lexer {
	quotes := slices.Clone(syn.Quotes)
	// Longest opener first, so `"""` wins over `"` and `r#"` over `r"`.
	slices.SortStableFunc(quotes, func(a, b Quote) int { return len(b.Open) - len(a.Open) })
	return &lexer{src: src, syn: syn, quotes: quotes}
}

func (lx *lexer) step() {
	if lx.quote() || lx.lineComment() || lx.blockComment() {
		return
	}
	lx.plain()
}

func (lx *lexer) plain() {
	switch lx.src[lx.i] {
	case '\n':
		lx.lineHasCode = false
	case ' ', '\t', '\r':
	default:
		lx.lineHasCode = true
	}
	lx.i++
}

func (lx *lexer) quote() bool {
	q, ok := openingQuote(lx.src, lx.i, lx.quotes)
	if !ok {
		return false
	}
	end := stringEnd(lx.src, lx.i+len(q.Open), q)
	lx.spans = append(lx.spans, LexSpan{Start: lx.i, End: end, Kind: LexString})
	lx.i, lx.lineHasCode = end, true
	return true
}

func (lx *lexer) lineComment() bool {
	opener, ok := openingToken(lx.src, lx.i, lx.syn.LineComments)
	if !ok {
		return false
	}
	lx.comment(len(opener), lineEnd(lx.src, lx.i), "")
	return true
}

func (lx *lexer) blockComment() bool {
	b, ok := openingBlock(lx.src, lx.i, lx.syn.BlockComments)
	if !ok {
		return false
	}
	lx.comment(len(b.Open), blockEnd(lx.src, lx.i+len(b.Open), b, lx.syn.Nested), b.Close)
	return true
}

func (lx *lexer) comment(openerLen, end int, closer string) {
	kind := LexComment
	if isDirective(lx.src[lx.i+openerLen:end], lx.syn.Directives) {
		kind = LexDirective
	}
	lx.spans = append(lx.spans, LexSpan{Start: lx.i, End: end, Kind: kind, Closer: closer, OwnLine: !lx.lineHasCode})
	lx.i = end
	// A directive is code, so a comment after it on its line owns no line.
	lx.lineHasCode = lx.lineHasCode || kind == LexDirective
}

func openingQuote(src string, i int, quotes []Quote) (Quote, bool) {
	for _, q := range quotes {
		if strings.HasPrefix(src[i:], q.Open) {
			return q, true
		}
	}
	return Quote{}, false
}

func openingToken(src string, i int, tokens []string) (string, bool) {
	for _, t := range tokens {
		if strings.HasPrefix(src[i:], t) {
			return t, true
		}
	}
	return "", false
}

func openingBlock(src string, i int, blocks []CommentBlock) (CommentBlock, bool) {
	for _, b := range blocks {
		if strings.HasPrefix(src[i:], b.Open) {
			return b, true
		}
	}
	return CommentBlock{}, false
}

// stringEnd scans from just past the opener to just past the closer. An
// unterminated string runs to EOF, which strips the same on both sides of a
// comparison.
func stringEnd(src string, i int, q Quote) int {
	for i < len(src) {
		if !q.IgnoreEscape && src[i] == '\\' {
			i += 2
			continue
		}
		if strings.HasPrefix(src[i:], q.Close) {
			return i + len(q.Close)
		}
		i++
	}
	return len(src)
}

func lineEnd(src string, i int) int {
	if n := strings.IndexByte(src[i:], '\n'); n >= 0 {
		return i + n
	}
	return len(src)
}

// blockEnd scans from just past the opener to just past the matching closer.
func blockEnd(src string, i int, b CommentBlock, nested bool) int {
	for depth := 1; i < len(src) && depth > 0; {
		step, delta := blockStep(src[i:], b, nested)
		i, depth = i+step, depth+delta
	}
	return i
}

func blockStep(rest string, b CommentBlock, nested bool) (step, delta int) {
	if nested && strings.HasPrefix(rest, b.Open) {
		return len(b.Open), 1
	}
	if strings.HasPrefix(rest, b.Close) {
		return len(b.Close), -1
	}
	return 1, 0
}

// isDirective reports whether a comment body opens with a declared directive
// prefix, after leading whitespace.
func isDirective(body string, directives []string) bool {
	body = strings.TrimLeft(body, " \t")
	return slices.ContainsFunc(directives, func(d string) bool { return strings.HasPrefix(body, d) })
}

// Complexity is 1 plus the declared decision tokens in body outside comments
// and strings. A syntax with no Comments or no Decisions cannot measure it,
// and says so rather than answering 1.
func (s Syntax) Complexity(body string) (int, error) {
	if err := s.requireComments(); err != nil {
		return 0, err
	}
	if s.Decisions == nil {
		return 0, errors.New("the syntax declares no decisions, so complexity is unmeasurable")
	}
	decisions := s.Decisions.set()
	n := 1
	for _, t := range s.tokens(s.code(body)) {
		if decisions[t.text] {
			n++
		}
	}
	return n, nil
}

func (s Syntax) requireComments() error {
	if s.Comments == nil {
		return errors.New("the syntax declares no comments, so a token in a comment or string would count as code")
	}
	return nil
}

// code blanks every comment and string byte of body but its newlines, so
// offsets and line numbers in the result are body's.
func (s Syntax) code(body string) string {
	b := []byte(body)
	for _, sp := range s.Comments.Spans(body) {
		for i := sp.Start; i < sp.End; i++ {
			if b[i] != '\n' {
				b[i] = ' '
			}
		}
	}
	return string(b)
}

type token struct {
	text   string
	offset int
}

// tokens splits code into words, declared operators read longest first, and
// single bytes. Whitespace is dropped.
func (s Syntax) tokens(code string) []token {
	ops := longestFirst(s.namedTokens())
	var out []token
	for i := 0; i < len(code); {
		n := tokenLen(code, i, ops)
		if !isSpace(code[i]) {
			out = append(out, token{text: code[i : i+n], offset: i})
		}
		i += n
	}
	return out
}

func tokenLen(code string, i int, ops []string) int {
	if isWordByte(code[i]) {
		return wordEnd(code, i) - i
	}
	if op, ok := openingToken(code, i, ops); ok {
		return len(op)
	}
	return 1
}

func wordEnd(code string, i int) int {
	for i < len(code) && isWordByte(code[i]) {
		i++
	}
	return i
}

const wordBytes = "_0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

// isWordByte counts every non-ASCII byte as a word byte, so a Unicode
// identifier reads as one word.
func isWordByte(c byte) bool {
	return c >= utf8.RuneSelf || strings.IndexByte(wordBytes, c) >= 0
}

func isSpace(c byte) bool { return strings.IndexByte(" \t\r\n\v\f", c) >= 0 }

// namedTokens is every token the syntax names, so a declared decision
// operator is read whole even when Operators leaves it out.
func (s Syntax) namedTokens() []string {
	out := slices.Clone(s.Operators)
	if s.Decisions != nil {
		out = append(out, s.Decisions.Operators...)
	}
	return out
}

// longestFirst keeps the multi-byte operators of tokens, longest first.
func longestFirst(tokens []string) []string {
	ops := slices.DeleteFunc(slices.Clone(tokens), func(t string) bool { return len(t) < 2 || isWordByte(t[0]) })
	slices.SortFunc(ops, func(a, b string) int { return cmp.Or(len(b)-len(a), strings.Compare(a, b)) })
	return slices.Compact(ops)
}

func (d DecisionSyntax) set() map[string]bool {
	set := map[string]bool{}
	for _, t := range slices.Concat(d.Keywords, d.Operators) {
		set[t] = true
	}
	return set
}
