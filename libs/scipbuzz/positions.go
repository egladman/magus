package scipbuzz

import (
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/scip-code/scip/bindings/go/scip"
)

// The gopherbuzz AST keeps only a start position per node, and none at all for
// declaration names, parameters or type annotations. This file recovers them from
// the token stream: every AST position is looked up as a token, and the names a
// node dropped are found by walking the tokens next to it the way the parser did.

// source is one file's text and the byte offset at which each line starts.
type source struct {
	text  string
	lines []int
}

func newSource(text string) *source {
	return &source{text: text, lines: lineStarts(text)}
}

func lineStarts(text string) []int {
	lines := []int{0}
	for i := 0; i < len(text); i++ {
		if text[i] == '\n' {
			lines = append(lines, i+1)
		}
	}
	return lines
}

// position converts a byte offset to SCIP's 0-based line and UTF-8 byte column.
func (s *source) position(off int) scip.Position {
	line := sort.Search(len(s.lines), func(i int) bool { return s.lines[i] > off }) - 1
	return scip.Position{Line: int32(line), Character: int32(off - s.lines[line])}
}

func (s *source) span(start, end int) scip.Range {
	return scip.Range{Start: s.position(start), End: s.position(end)}
}

// tok is a lexer token and the byte offset of its first byte in the file, or -1
// when it could not be placed.
type tok struct {
	token.Token
	off int
}

// stream is the token sequence one parse saw: the whole file, or the source of one
// string interpolation, which the parser lexes separately.
type stream struct {
	src  *source
	toks []tok
	// at maps the position the parser stamped on a node to the token it came from.
	// For an interpolation that is not the token's own place: the parser shifts it
	// by the position of the enclosing `{` in upstream's approximate convention (see
	// token.StringPart.Col), so dl and dc record that shift.
	at     map[ast.Pos]int
	dl, dc int
	subs   map[int][]*stream
}

func fileStream(src *source) (*stream, error) {
	toks, err := token.Tokenize(src.text)
	if err != nil {
		return nil, err
	}
	return newStream(src, toks, func(line, col int) int {
		if line < 1 || line > len(src.lines) {
			return -1
		}
		return src.lines[line-1] + col - 1
	}, 0, 0), nil
}

func newStream(src *source, toks []token.Token, place func(line, col int) int, dl, dc int) *stream {
	s := &stream{src: src, toks: make([]tok, len(toks)), at: make(map[ast.Pos]int, len(toks)), dl: dl, dc: dc}
	for i, t := range toks {
		s.toks[i] = tok{Token: t, off: place(t.Line, t.Col)}
		key := ast.Pos{Line: t.Line + dl, Col: t.Col + dc}
		if _, dup := s.at[key]; !dup {
			s.at[key] = i
		}
	}
	return s
}

func (s *stream) kind(i int) token.Kind {
	if i < 0 || i >= len(s.toks) {
		return token.EOF
	}
	return s.toks[i].Kind
}

func (s *stream) isIdent(i int, val string) bool {
	return s.kind(i) == token.Ident && s.toks[i].Val == val
}

// identRange returns the exact range of the identifier at i. A free identifier
// (@"name") is ranged over the text between its quotes, so the range still holds
// exactly the name and a rename that rewrites it keeps the quotes.
func (s *stream) identRange(i int) (scip.Range, bool) {
	if s.kind(i) != token.Ident || s.toks[i].off < 0 {
		return scip.Range{}, false
	}
	t, text := s.toks[i], s.src.text
	start := t.off
	if t.Raw {
		if !strings.HasPrefix(text[start:], `@"`) {
			return scip.Range{}, false
		}
		start += 2
	}
	end := start + len(t.Val)
	if t.Val == "" || end > len(text) || text[start:end] != t.Val || strings.ContainsRune(t.Val, '\n') {
		return scip.Range{}, false
	}
	if t.Raw && (end >= len(text) || text[end] != '"') {
		return scip.Range{}, false
	}
	return s.src.span(start, end), true
}

// segmentRange returns the range of the last path segment inside the string
// literal at i, when the literal is written without escapes. An import without an
// alias binds that segment as its name.
func (s *stream) segmentRange(i int, segment string) (scip.Range, bool) {
	if s.kind(i) != token.String || s.toks[i].off < 0 {
		return scip.Range{}, false
	}
	t, text := s.toks[i], s.src.text
	start := t.off + 1
	end := start + len(t.Val)
	if end >= len(text) || text[start:end] != t.Val || text[end] != text[t.off] {
		return scip.Range{}, false
	}
	at := strings.LastIndex(t.Val, segment)
	if at < 0 || at+len(segment) != len(t.Val) {
		return scip.Range{}, false
	}
	return s.src.span(start+at, end), true
}

// sub returns the streams of the interpolations in the string token at i,
// parallel to its Parts: nil for a literal part, and nil for an expression whose
// source could not be placed in the file.
func (s *stream) sub(i int) []*stream {
	if subs, ok := s.subs[i]; ok {
		return subs
	}
	if s.subs == nil {
		s.subs = make(map[int][]*stream)
	}
	t := s.toks[i]
	out := make([]*stream, len(t.Parts))
	s.subs[i] = out
	if t.off < 0 {
		return out
	}
	starts := interpStarts(s.src.text, t.off)
	k := 0
	for j, part := range t.Parts {
		if !part.IsExpr {
			continue
		}
		if k >= len(starts) {
			break
		}
		start := starts[k]
		k++
		if start+len(part.Text) > len(s.src.text) || s.src.text[start:start+len(part.Text)] != part.Text {
			continue
		}
		// The same source the parser sub-parses, so the token streams line up.
		toks, err := token.Tokenize(part.Text + ";")
		if err != nil {
			continue
		}
		lines := lineStarts(part.Text)
		place := func(line, col int) int {
			if line < 1 || line > len(lines) {
				return -1
			}
			return start + lines[line-1] + col - 1
		}
		dl, dc := 0, 0
		if part.Line != 0 {
			dl, dc = part.Line+s.dl-1, part.Col+s.dc-1
		}
		out[j] = newStream(s.src, toks, place, dl, dc)
	}
	return out
}

// interpStarts returns the byte offset where each interpolated expression's
// source begins in the string literal at off. It retraces token.lexString and
// lexRawString, which keep only upstream's approximate columns.
func interpStarts(text string, off int) []int {
	if off < 0 || off >= len(text) {
		return nil
	}
	var starts []int
	raw := text[off] == '`'
	if !raw && text[off] != '"' {
		return nil
	}
	p := off + 1
	for p < len(text) {
		c := text[p]
		if raw && c == '\\' && p+1 < len(text) && (text[p+1] == '{' || text[p+1] == '}') {
			p += 2
			continue
		}
		switch {
		case raw && c == '`', !raw && c == '"':
			return starts
		case !raw && c == '\\':
			p++
			if p+3 <= len(text) && strings.Trim(text[p:p+3], "0123456789") == "" {
				p += 3
				continue
			}
			_, size := utf8.DecodeRuneInString(text[p:])
			p += size
		case c == '{':
			p++
			starts = append(starts, p)
			p = interpEnd(text, p, !raw)
		default:
			_, size := utf8.DecodeRuneInString(text[p:])
			p += size
		}
	}
	return starts
}

// interpEnd returns the offset just past the `}` closing the interpolation whose
// source begins at p, retracing token.captureInterpExpr.
func interpEnd(text string, p int, nest bool) int {
	depth := 1
	for p < len(text) {
		r, size := utf8.DecodeRuneInString(text[p:])
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return p + 1
			}
		case '"', '`':
			delim := r
			p += size
			for p < len(text) {
				r2, s2 := utf8.DecodeRuneInString(text[p:])
				p += s2
				if r2 == '\\' && p < len(text) {
					_, s3 := utf8.DecodeRuneInString(text[p:])
					p += s3
					continue
				}
				if r2 == '{' && nest {
					p = interpEnd(text, p, nest)
					continue
				}
				if r2 == delim {
					break
				}
			}
			continue
		}
		p += size
	}
	return p
}

// typeRef is a type name inside an annotation: the identifier, and for ns\Name
// the qualifier, as token indexes. ns is -1 for a bare name.
type typeRef struct{ ns, name int }

// skipType returns the index just past the type that starts at i, appending the
// type names it passes to refs. It follows the parser's skipType token for token.
func (s *stream) skipType(i int, refs *[]typeRef) (int, bool) {
	if s.kind(i) == token.Mut {
		i++
	}
	ok := true
	switch s.kind(i) {
	case token.Ident, token.Void:
		if s.isIdent(i, "obj") && s.kind(i+1) == token.LBrace {
			return s.skipBalanced(i + 1)
		}
		first, last, hops := i, i, 0
		i++
		for s.kind(i) == token.Backslash {
			if s.kind(i+1) != token.Ident {
				return i, false
			}
			last = i + 1
			i += 2
			hops++
		}
		switch {
		case s.kind(first) != token.Ident:
		case hops == 0:
			*refs = append(*refs, typeRef{ns: -1, name: first})
		case hops == 1:
			*refs = append(*refs, typeRef{ns: first, name: last})
		}
		if s.kind(i) == token.Colon && s.kind(i+1) == token.Colon && s.kind(i+2) == token.Lt {
			i, ok = s.typeArgs(i+2, refs)
		} else if s.kind(i) == token.Lt {
			i, ok = s.typeArgs(i, refs)
		}
		if s.kind(i) == token.Question {
			i++
		}
	case token.LBracket:
		if i, ok = s.skipType(i+1, refs); !ok || s.kind(i) != token.RBracket {
			return i, false
		}
		i++
		if s.kind(i) == token.Question {
			i++
		}
	case token.LBrace:
		if i, ok = s.skipType(i+1, refs); !ok || s.kind(i) != token.Colon {
			return i, false
		}
		if i, ok = s.skipType(i+1, refs); !ok || s.kind(i) != token.RBrace {
			return i, false
		}
		i++
		if s.kind(i) == token.Question {
			i++
		}
	case token.Fun:
		i++
		// A generic function type's own `::<T>` names stand for its parameters, so
		// its type names are kept apart and those are left out.
		var params []int
		own := refs
		if s.kind(i) == token.Colon && s.kind(i+1) == token.Colon && s.kind(i+2) == token.Lt {
			if params, i, ok = s.typeParams(i + 2); !ok {
				return i, false
			}
			own = new([]typeRef)
		}
		if s.kind(i) != token.LParen {
			return i, false
		}
		i++
		for s.kind(i) != token.RParen && s.kind(i) != token.EOF {
			if s.kind(i) == token.Ident && s.kind(i+1) == token.Colon {
				i += 2
			}
			if i, ok = s.skipType(i, own); !ok {
				return i, false
			}
			if s.kind(i) != token.Comma {
				break
			}
			i++
		}
		if s.kind(i) != token.RParen {
			return i, false
		}
		i++
		if s.kind(i) == token.Gt {
			i, ok = s.skipType(i+1, own)
		} else if s.typeStart(i) {
			i, ok = s.skipType(i, own)
		}
		for ok && (s.kind(i) == token.ErrArrow || s.kind(i) == token.YieldArrow) {
			i, ok = s.skipType(i+1, own)
		}
		if s.kind(i) == token.Question {
			i++
		}
		if own != refs {
			for _, r := range *own {
				if r.ns >= 0 || !slices.ContainsFunc(params, func(p int) bool { return s.toks[p].Val == s.toks[r.name].Val }) {
					*refs = append(*refs, r)
				}
			}
		}
	default:
		return i, false
	}
	return i, ok
}

func (s *stream) typeStart(i int) bool {
	switch s.kind(i) {
	case token.Ident, token.Void, token.LBracket, token.Fun:
		return true
	default:
		return false
	}
}

// skipAngles returns the index just past the <...> list opening at i.
func (s *stream) skipAngles(i int) (int, bool) {
	if s.kind(i) != token.Lt {
		return i, false
	}
	depth := 0
	for ; i < len(s.toks); i++ {
		switch s.kind(i) {
		case token.Lt:
			depth++
		case token.Gt:
			depth--
			if depth == 0 {
				return i + 1, true
			}
		case token.EOF:
			return i, false
		}
	}
	return i, false
}

// typeArgs returns the index just past the `<...>` type argument list opening at
// i, appending the type names of its arguments to refs. A list it cannot read as
// types is skipped whole and adds nothing.
func (s *stream) typeArgs(i int, refs *[]typeRef) (int, bool) {
	var args []typeRef
	for j := i + 1; ; j++ {
		var ok bool
		if j, ok = s.skipType(j, &args); !ok {
			break
		}
		if s.kind(j) == token.Gt {
			*refs = append(*refs, args...)
			return j + 1, true
		}
		if s.kind(j) != token.Comma {
			break
		}
	}
	return s.skipAngles(i)
}

// typeParams reads the `<T, U>` of a declaration's `::<T, U>` clause opening at
// i. It returns the parameters' indexes and the index just past the list. Like the
// parser's typeParamList it takes every identifier inside the brackets as a name.
func (s *stream) typeParams(i int) ([]int, int, bool) {
	end, ok := s.skipAngles(i)
	if !ok {
		return nil, end, false
	}
	var params []int
	for j := i + 1; j < end-1; j++ {
		if s.kind(j) == token.Ident {
			params = append(params, j)
		}
	}
	return params, end, true
}

// closer returns the index of the bracket closing the one opening at i.
func (s *stream) closer(i int) (int, bool) {
	depth := 0
	for ; i < len(s.toks); i++ {
		switch s.kind(i) {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			depth--
			if depth == 0 {
				return i, true
			}
		case token.EOF:
			return i, false
		}
	}
	return i, false
}

func (s *stream) skipBalanced(i int) (int, bool) {
	end, ok := s.closer(i)
	return end + 1, ok
}

// skipExpr returns the index of the first comma or unmatched closing bracket at
// or after i: where a parameter default or a field default ends.
func (s *stream) skipExpr(i int) int {
	depth := 0
	for ; i < len(s.toks); i++ {
		switch s.kind(i) {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			if depth == 0 {
				return i
			}
			depth--
		case token.Comma:
			if depth == 0 {
				return i
			}
		case token.EOF:
			return i
		}
	}
	return i
}

// funcSig is a function signature located in a stream.
type funcSig struct {
	name       int // -1 for a function expression
	typeParams []int
	params     []int
	refs       []typeRef
	// body is the index just past the signature: a `{`, a `=>`, or the `;` that
	// ends an extern declaration.
	body int
}

// signatureAt reads the signature starting at i, the `fun` token or the `extern`
// before it, following the parser's parseFunDecl and parseFunRest. A function
// expression, whose `fun` no name follows, comes back with name -1.
func (s *stream) signatureAt(i int) (funcSig, bool) {
	h := funcSig{name: -1}
	if s.isIdent(i, "extern") {
		i++
	}
	if s.kind(i) != token.Fun {
		return h, false
	}
	i++
	if s.kind(i) == token.Ident {
		h.name = i
		i++
	}
	ok := true
	if s.kind(i) == token.Colon && s.kind(i+1) == token.Colon && s.kind(i+2) == token.Lt {
		if h.typeParams, i, ok = s.typeParams(i + 2); !ok {
			return h, false
		}
	}
	if s.kind(i) != token.LParen {
		return h, false
	}
	i++
	for s.kind(i) != token.RParen && s.kind(i) != token.EOF {
		if s.kind(i) != token.Ident || s.kind(i+1) != token.Colon {
			return h, false
		}
		h.params = append(h.params, i)
		if i, ok = s.skipType(i+2, &h.refs); !ok {
			return h, false
		}
		if s.kind(i) == token.Assign {
			i = s.skipExpr(i + 1)
		}
		if s.kind(i) != token.Comma {
			break
		}
		i++
	}
	if s.kind(i) != token.RParen {
		return h, false
	}
	i++
	if s.kind(i) == token.Gt {
		if s.kind(i+1) == token.Void {
			i += 2
		} else if i, ok = s.skipType(i+1, &h.refs); !ok {
			return h, false
		}
	}
	if s.kind(i) == token.YieldArrow {
		if s.kind(i+1) == token.Void {
			i += 2
		} else if i, ok = s.skipType(i+1, &h.refs); !ok {
			return h, false
		}
	}
	if s.kind(i) == token.ErrArrow {
		i++
		if s.typeStart(i) {
			if i, ok = s.skipType(i, &h.refs); !ok {
				return h, false
			}
		}
	}
	h.body = i
	return h, true
}

// bodyEnd returns the index of the last token of the body starting at i: the
// closing brace of a block, or the `;` ending an extern declaration or a `=>`
// expression body.
func (s *stream) bodyEnd(i int) (int, bool) {
	switch s.kind(i) {
	case token.LBrace:
		return s.closer(i)
	case token.Semicolon:
		return i, true
	case token.FatArrow:
		depth := 0
		for j := i + 1; j < len(s.toks); j++ {
			switch s.kind(j) {
			case token.LParen, token.LBracket, token.LBrace:
				depth++
			case token.RParen, token.RBracket, token.RBrace:
				depth--
				if depth < 0 {
					return j - 1, true
				}
			case token.Semicolon:
				if depth == 0 {
					return j, true
				}
			case token.EOF:
				return j - 1, true
			}
		}
	}
	return i, false
}
