package spells

import (
	"cmp"
	"errors"
	"fmt"
	"regexp"
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
	// is never comment-only, yet it holds no decision and no mutant.
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

// MutationSite is one mutant of a body: the Find token at Offset replaced
// by Replace. Offset is a byte offset into the body MutationSites read.
// Line and Col are 1-based, Col in bytes.
type MutationSite struct {
	Operator          string
	Offset, Line, Col int
	Find, Replace     string
}

// MutationSites lists body's mutants: each declared swap at each token it
// finds outside comments, strings and arid lines, then each declared body
// replacement. A function with no body the style can cut yields no body
// mutant.
func (s Syntax) MutationSites(body string) ([]MutationSite, error) {
	if err := s.requireMutations(); err != nil {
		return nil, err
	}
	arid, err := s.Mutations.AridPatterns()
	if err != nil {
		return nil, err
	}
	code, lines := s.code(body), newLineIndex(body)
	sites := s.swapSites(code, lines, aridLines(body, arid), s.bodyStart(code))
	return append(sites, s.bodySites(body, code, lines)...), nil
}

// Apply returns src with the site's mutation made. It refuses a src that
// does not hold Find at Offset, so a stale site never mutates the wrong text.
func (m MutationSite) Apply(src string) (string, error) {
	if m.Offset > len(src) || !strings.HasPrefix(src[m.Offset:], m.Find) {
		return "", fmt.Errorf("the source holds no %q at offset %d", m.Find, m.Offset)
	}
	return fmt.Sprintf("%s%s%s", src[:m.Offset], m.Replace, src[m.Offset+len(m.Find):]), nil
}

// AridPatterns compiles Arid, naming the first pattern that does not parse.
func (m MutationSyntax) AridPatterns() ([]*regexp.Regexp, error) {
	out := make([]*regexp.Regexp, 0, len(m.Arid))
	for i, p := range m.Arid {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("mutations.arid[%d]: %w", i, err)
		}
		out = append(out, re)
	}
	return out, nil
}

func (s Syntax) requireComments() error {
	if s.Comments == nil {
		return errors.New("the syntax declares no comments, so a token in a comment or string would count as code")
	}
	return nil
}

func (s Syntax) requireMutations() error {
	if err := s.requireComments(); err != nil {
		return err
	}
	if s.Mutations == nil {
		return errors.New("the syntax declares no mutations, so a function yields no mutants")
	}
	if len(s.Mutations.Bodies) > 0 && s.bodyStyle() == "" {
		return errors.New("mutations.bodies need stubs.bodyStyle to find the body they replace")
	}
	return nil
}

func (s Syntax) bodyStyle() string {
	if s.Stubs == nil {
		return ""
	}
	return s.Stubs.BodyStyle
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

// namedTokens is every token the syntax names, so a declared swap or
// decision operator is read whole even when Operators leaves it out.
func (s Syntax) namedTokens() []string {
	out := slices.Clone(s.Operators)
	if s.Decisions != nil {
		out = append(out, s.Decisions.Operators...)
	}
	if s.Mutations != nil {
		out = append(out, s.Mutations.swapTokens()...)
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

func (m MutationSyntax) swapTokens() []string {
	out := make([]string, 0, 2*len(m.Swaps))
	for _, sw := range m.Swaps {
		out = append(out, sw.Find, sw.Replace)
	}
	return out
}

func (m MutationSyntax) swapsByToken() map[string][]Swap {
	out := map[string][]Swap{}
	for _, sw := range m.Swaps {
		out[sw.Find] = append(out[sw.Find], sw)
	}
	return out
}

func (s Syntax) swapSites(code string, lines lineIndex, arid map[int]bool, from int) []MutationSite {
	bySwap := s.Mutations.swapsByToken()
	var out []MutationSite
	for _, t := range s.tokens(code) {
		if t.offset < from {
			continue
		}
		line, col := lines.position(t.offset)
		if arid[line] {
			continue
		}
		for _, sw := range bySwap[t.text] {
			out = append(out, MutationSite{Operator: sw.Name, Offset: t.offset, Line: line, Col: col, Find: sw.Find, Replace: sw.Replace})
		}
	}
	return out
}

func aridLines(body string, patterns []*regexp.Regexp) map[int]bool {
	arid := map[int]bool{}
	for n, line := range strings.Split(body, "\n") {
		arid[n+1] = slices.ContainsFunc(patterns, func(re *regexp.Regexp) bool { return re.MatchString(line) })
	}
	return arid
}

// bodyStart is where the body begins, or 0 when the style cuts none. A swap
// before it would land in the signature, where a return-type arrow reads as a
// comparison and the mutant does not parse.
func (s Syntax) bodyStart(code string) int {
	start, _, ok := s.bodyRange(code)
	if !ok {
		return 0
	}
	return start
}

func (s Syntax) bodySites(body, code string, lines lineIndex) []MutationSite {
	start, end, ok := s.bodyRange(code)
	if !ok {
		return nil
	}
	line, col := lines.position(start)
	out := make([]MutationSite, 0, len(s.Mutations.Bodies))
	for _, b := range s.Mutations.Bodies {
		out = append(out, MutationSite{Operator: BodyMutation, Offset: start, Line: line, Col: col,
			Find: body[start:end], Replace: s.bodyReplacement(body[start:], b)})
	}
	return out
}

func (s Syntax) bodyRange(code string) (start, end int, ok bool) {
	switch s.bodyStyle() {
	case StubBodyBrace:
		return braceBody(code)
	case StubBodyIndent:
		return indentBody(code)
	}
	return 0, 0, false
}

// braceBody is the final {...} of code.
func braceBody(code string) (start, end int, ok bool) {
	closer := strings.LastIndexByte(code, '}')
	if closer < 0 {
		return 0, 0, false
	}
	open := matchingOpen(code, closer)
	return open, closer + 1, open >= 0
}

func matchingOpen(code string, closer int) int {
	depth := 0
	for i := closer; i >= 0; i-- {
		depth -= nestingDelta(code[i])
		if depth == 0 {
			return i
		}
	}
	return -1
}

// indentBody is what follows the header's first top-level colon, trailing
// whitespace cut.
func indentBody(code string) (start, end int, ok bool) {
	colon := headerColon(code)
	if colon < 0 {
		return 0, 0, false
	}
	return colon + 1, len(strings.TrimRight(code, " \t\r\n")), true
}

func headerColon(code string) int {
	depth := 0
	for i := range len(code) {
		depth += nestingDelta(code[i])
		if code[i] == ':' && depth == 0 {
			return i
		}
	}
	return -1
}

func nestingDelta(c byte) int {
	switch c {
	case '(', '[', '{':
		return 1
	case ')', ']', '}':
		return -1
	}
	return 0
}

// bodyReplacement places an indent-style body where the cut one sat: on the
// header's line, or on the next line at the old body's indentation.
func (s Syntax) bodyReplacement(rest, tmpl string) string {
	if s.bodyStyle() != StubBodyIndent {
		return tmpl
	}
	nl := strings.IndexByte(rest, '\n')
	if nl < 0 || strings.TrimSpace(rest[:nl]) != "" {
		return " " + tmpl
	}
	return fmt.Sprintf("\n%s%s", bodyIndent(rest[nl+1:]), tmpl)
}

func bodyIndent(lines string) string {
	for _, line := range strings.Split(lines, "\n") {
		if code := strings.TrimLeft(line, " \t"); code != "" {
			return line[:len(line)-len(code)]
		}
	}
	return ""
}

// lineIndex holds the offset each line of a source starts at.
type lineIndex []int

func newLineIndex(src string) lineIndex {
	starts := lineIndex{0}
	for i := range len(src) {
		if src[i] == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

func (l lineIndex) position(offset int) (line, col int) {
	line, _ = slices.BinarySearch(l, offset+1)
	return line, offset - l[line-1] + 1
}
