// Package token defines the lexical token types and scanner for Buzz source.
package token

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kind identifies the category of a lexical token.
type Kind int

const (
	// literals
	Ident     Kind = iota
	String         // plain string literal
	InterpStr      // string with {expr} interpolation; segments in Token.Parts
	Int
	Float
	Pat // pattern literal $"..."; raw regex source in Token.Val

	// keywords
	Import
	Export
	Final
	Var
	Mut
	Fun
	Return
	True
	False
	Null
	Void
	If
	Else
	While
	For
	Foreach
	In
	Break
	Continue
	And
	Or
	Object
	Enum

	// punctuation & operators
	LParen     // (
	RParen     // )
	LBrace     // {
	RBrace     // }
	LBracket   // [
	RBracket   // ]
	Comma      // ,
	Semicolon  // ;
	Colon      // :
	Dot        // .
	Assign     // =
	Question   // ?
	Coalesce   // ??
	Bang       // !
	Plus       // +
	Minus      // -
	Star       // *
	Slash      // /
	Percent    // %
	Eq         // ==
	Neq        // !=
	Lt         // <
	Gt         // >
	Le         // <=
	Ge         // >=
	DotDot     // ..
	ErrArrow   // !>
	YieldArrow // *>
	Backslash  // \
	Amp        // &

	// keywords added for syntax parity
	Is
	As
	Do
	Until

	// error-handling keywords
	Try
	Catch
	Throw

	// fiber keywords
	Yield
	Resume
	Resolve

	// module keywords
	Namespace

	EOF
)

// String returns a human-readable name for the token kind, used in parse errors.
func (k Kind) String() string {
	switch k {
	case Ident:
		return "identifier"
	case String:
		return "string literal"
	case InterpStr:
		return "interpolated string"
	case Int:
		return "integer literal"
	case Float:
		return "float literal"
	case Pat:
		return "pattern literal"
	case Import:
		return "'import'"
	case Export:
		return "'export'"
	case Final:
		return "'final'"
	case Mut:
		return "'mut'"
	case Var:
		return "'var'"
	case Fun:
		return "'fun'"
	case Return:
		return "'return'"
	case True:
		return "'true'"
	case False:
		return "'false'"
	case Null:
		return "'null'"
	case Void:
		return "'void'"
	case If:
		return "'if'"
	case Else:
		return "'else'"
	case While:
		return "'while'"
	case For:
		return "'for'"
	case Foreach:
		return "'foreach'"
	case In:
		return "'in'"
	case Break:
		return "'break'"
	case Continue:
		return "'continue'"
	case And:
		return "'and'"
	case Or:
		return "'or'"
	case Object:
		return "'object'"
	case Enum:
		return "'enum'"
	case LParen:
		return "'('"
	case RParen:
		return "')'"
	case LBrace:
		return "'{'"
	case RBrace:
		return "'}'"
	case LBracket:
		return "'['"
	case RBracket:
		return "']'"
	case Comma:
		return "','"
	case Semicolon:
		return "';'"
	case Colon:
		return "':'"
	case Dot:
		return "'.'"
	case Assign:
		return "'='"
	case Eq:
		return "'=='"
	case Neq:
		return "'!='"
	case Lt:
		return "'<'"
	case Gt:
		return "'>'"
	case Le:
		return "'<='"
	case Ge:
		return "'>='"
	case DotDot:
		return "'..'"
	case EOF:
		return "EOF"
	default:
		return fmt.Sprintf("token(%d)", int(k))
	}
}

var keywords = map[string]Kind{
	"import":    Import,
	"export":    Export,
	"final":     Final,
	"var":       Var,
	"mut":       Mut,
	"fun":       Fun,
	"return":    Return,
	"true":      True,
	"false":     False,
	"null":      Null,
	"void":      Void,
	"if":        If,
	"else":      Else,
	"while":     While,
	"for":       For,
	"foreach":   Foreach,
	"in":        In,
	"break":     Break,
	"continue":  Continue,
	"and":       And,
	"or":        Or,
	"object":    Object,
	"enum":      Enum,
	"is":        Is,
	"as":        As,
	"do":        Do,
	"until":     Until,
	"try":       Try,
	"catch":     Catch,
	"throw":     Throw,
	"yield":     Yield,
	"resume":    Resume,
	"resolve":   Resolve,
	"namespace": Namespace,
}

// IsKeyword reports whether word is a reserved Buzz keyword (as opposed to an
// identifier). It lets callers outside the parser — e.g. a syntax highlighter —
// classify a word against the canonical keyword set without re-deriving it.
func IsKeyword(word string) bool {
	_, ok := keywords[word]
	return ok
}

// StringPart is one segment of an interpolated string: either a literal run of
// text (IsExpr=false) or the raw source of an embedded expression (IsExpr=true).
type StringPart struct {
	IsExpr bool
	Text   string
}

// Token is a single lexical token.
type Token struct {
	Kind  Kind
	Val   string       // raw text (ident, plain string, number)
	Parts []StringPart // only for InterpStr
	Line  int
	Col   int
	// Doc carries the documentation comment block immediately preceding this
	// token — the contiguous run of // line comments (or a single /* */ block)
	// on the lines directly above, with no blank line in between. It is "" for
	// every token not directly preceded by such a block. The scanner still emits
	// no token for a comment; Doc only annotates the next real token, which the
	// parser reads off the `fun`/`export` token to attach to a declaration.
	Doc string
}

// lexer tokenizes a Buzz source string.
type lexer struct {
	src    string
	pos    int
	line   int
	col    int
	tokens []Token
	// pendingDoc accumulates the most recent contiguous comment block; pendingDocLine
	// is the source line of its last comment line. tokenize attaches pendingDoc to the
	// next token only when that token begins on pendingDocLine+1 (no blank-line gap).
	pendingDoc     string
	pendingDocLine int
	// lastTokenLine is the end line of the most recently emitted token. A comment
	// that begins on this line is a trailing comment (code precedes it), not a
	// leading doc, so it is not recorded as pending doc.
	lastTokenLine int
}

func newLexer(src string) *lexer {
	// ultra-opt: pre-size the token slice to avoid repeated append regrowth+copy
	// of the backing array; tokenize is ~88% of parse-time allocation (alloc_space
	// pprof). len(src)/4 is a slight over-estimate of the token count for Buzz
	// source (most tokens span ≥4 bytes incl. surrounding whitespace), so the
	// common case never reallocs; a denser source just costs one growth.
	//   measured: BenchmarkParse -25% sec/op, -47% B/op, -12% allocs/op;
	//   BenchmarkCompile -19% sec/op, -30% B/op (benchstat, n=10, p<0.01). Off the
	//   VM Exec dispatch path entirely, so no inner-loop regression risk.
	//   trade-off: a tiny constant of slack capacity for short sources (+16).
	return &lexer{src: src, line: 1, col: 1, tokens: make([]Token, 0, len(src)/4+16)}
}

// Tokenize lexes src and returns the complete token stream including EOF.
func Tokenize(src string) ([]Token, error) {
	return newLexer(src).tokenize()
}

func (l *lexer) tokenize() ([]Token, error) {
	for {
		l.skipWhitespaceAndComments()
		if l.pos >= len(l.src) {
			l.tokens = append(l.tokens, Token{Kind: EOF, Line: l.line, Col: l.col})
			return l.tokens, nil
		}
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		tok, err := l.nextToken(r, size)
		if err != nil {
			return nil, err
		}
		// Attach a pending doc block only to the token directly below it (no blank
		// line); either way the pending block is consumed so it can't leak onto a
		// later token.
		if l.pendingDoc != "" {
			if tok.Line == l.pendingDocLine+1 {
				tok.Doc = l.pendingDoc
			}
			l.pendingDoc = ""
		}
		l.tokens = append(l.tokens, tok)
		l.lastTokenLine = l.line
	}
}

// recordDoc folds a just-scanned comment's text into pendingDoc. A line gap from
// the previous comment line starts a fresh block, so only a contiguous run is kept.
func (l *lexer) recordDoc(text string, line int) {
	if l.pendingDoc != "" && line != l.pendingDocLine+1 {
		l.pendingDoc = ""
	}
	if l.pendingDoc == "" {
		l.pendingDoc = text
	} else {
		l.pendingDoc += "\n" + text
	}
	l.pendingDocLine = line
}

func (l *lexer) skipWhitespaceAndComments() {
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if r == '\n' {
			l.pos++
			l.line++
			l.col = 1
			continue
		}
		if unicode.IsSpace(r) {
			l.pos += size
			l.col += size
			continue
		}
		if r == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '/' {
			commentLine := l.line
			start := l.pos + 2
			for l.pos < len(l.src) && l.src[l.pos] != '\n' {
				l.pos++
			}
			if commentLine != l.lastTokenLine { // skip trailing comments
				l.recordDoc(strings.TrimSpace(l.src[start:l.pos]), commentLine)
			}
			continue
		}
		if r == '/' && l.pos+1 < len(l.src) && l.src[l.pos+1] == '*' {
			commentLine := l.line
			l.pos += 2
			l.col += 2
			start := l.pos
			for l.pos < len(l.src) {
				if l.src[l.pos] == '\n' {
					l.line++
					l.col = 1
					l.pos++
					continue
				}
				if l.pos+1 < len(l.src) && l.src[l.pos] == '*' && l.src[l.pos+1] == '/' {
					if commentLine != l.lastTokenLine { // skip trailing comments
						l.recordDoc(strings.TrimSpace(l.src[start:l.pos]), l.line)
					}
					l.pos += 2
					l.col += 2
					break
				}
				l.pos++
				l.col++
			}
			continue
		}
		break
	}
}

func (l *lexer) nextToken(r rune, size int) (Token, error) {
	startLine := l.line
	startCol := l.col
	simple := func(k Kind, n int) Token {
		l.advance(n)
		return Token{Kind: k, Line: startLine, Col: startCol}
	}

	switch r {
	case '(':
		return simple(LParen, size), nil
	case ')':
		return simple(RParen, size), nil
	case '{':
		return simple(LBrace, size), nil
	case '}':
		return simple(RBrace, size), nil
	case '[':
		return simple(LBracket, size), nil
	case ']':
		return simple(RBracket, size), nil
	case ',':
		return simple(Comma, size), nil
	case ';':
		return simple(Semicolon, size), nil
	case ':':
		return simple(Colon, size), nil
	case '.':
		if l.peekByte() == '.' {
			return simple(DotDot, 2), nil
		}
		return simple(Dot, size), nil
	case '+':
		return simple(Plus, size), nil
	case '&':
		return simple(Amp, size), nil
	case '*':
		if l.peekByte() == '>' {
			return simple(YieldArrow, 2), nil
		}
		return simple(Star, size), nil
	case '/':
		return simple(Slash, size), nil
	case '%':
		return simple(Percent, size), nil
	case '=':
		if l.peekByte() == '=' {
			return simple(Eq, 2), nil
		}
		return simple(Assign, size), nil
	case '!':
		if l.peekByte() == '=' {
			return simple(Neq, 2), nil
		}
		if l.peekByte() == '>' {
			return simple(ErrArrow, 2), nil
		}
		return simple(Bang, size), nil
	case '<':
		if l.peekByte() == '=' {
			return simple(Le, 2), nil
		}
		return simple(Lt, size), nil
	case '>':
		if l.peekByte() == '=' {
			return simple(Ge, 2), nil
		}
		return simple(Gt, size), nil
	case '?':
		if l.peekByte() == '?' {
			return simple(Coalesce, 2), nil
		}
		return simple(Question, size), nil
	case '-':
		// Negative numeric literal vs minus operator is resolved by the parser
		// (unary). Always emit minus here.
		return simple(Minus, size), nil
	case '"':
		return l.lexString(startLine, startCol)
	case '`':
		return l.lexRawString(startLine, startCol)
	case '$':
		if l.peekByte() == '"' {
			return l.lexPattern(startLine, startCol)
		}
		return Token{}, fmt.Errorf("buzz: unexpected character %q at line %d:%d", r, l.line, l.col)
	case '\\':
		return simple(Backslash, size), nil
	}

	if r >= '0' && r <= '9' {
		return l.lexNumber(startLine, startCol)
	}
	if isIdentStart(r) {
		return l.lexIdent(startLine, startCol)
	}
	return Token{}, fmt.Errorf("buzz: unexpected character %q at line %d:%d", r, l.line, l.col)
}

func (l *lexer) advance(n int) {
	l.pos += n
	l.col += n
}

// peekByte returns the byte one position ahead of the current one, or 0.
func (l *lexer) peekByte() byte {
	if l.pos+1 >= len(l.src) {
		return 0
	}
	return l.src[l.pos+1]
}

// lexString scans a double-quoted string, splitting on {expr} interpolation.
func (l *lexer) lexString(line, col int) (Token, error) {
	l.pos++ // opening "
	l.col++
	var parts []StringPart
	var lit strings.Builder
	hasExpr := false

	flushLit := func() {
		if lit.Len() > 0 {
			parts = append(parts, StringPart{Text: lit.String()})
			lit.Reset()
		}
	}

	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		switch r {
		case '"':
			l.pos++
			l.col++
			flushLit()
			if !hasExpr {
				s := ""
				if len(parts) == 1 {
					s = parts[0].Text
				}
				return Token{Kind: String, Val: s, Line: line, Col: col}, nil
			}
			return Token{Kind: InterpStr, Parts: parts, Line: line, Col: col}, nil
		case '\\':
			if l.pos+1 < len(l.src) {
				l.pos++
				l.col++
				esc, esz := utf8.DecodeRuneInString(l.src[l.pos:])
				switch esc {
				case 'n':
					lit.WriteByte('\n')
				case 't':
					lit.WriteByte('\t')
				case 'r':
					lit.WriteByte('\r')
				case '"':
					lit.WriteByte('"')
				case '\\':
					lit.WriteByte('\\')
				case '{':
					lit.WriteByte('{')
				case '}':
					lit.WriteByte('}')
				default:
					lit.WriteRune('\\')
					lit.WriteRune(esc)
				}
				l.pos += esz
				l.col += esz
				continue
			}
			return Token{}, fmt.Errorf("buzz: dangling escape in string at line %d:%d", line, col)
		case '{':
			// Begin interpolation: capture balanced expression source.
			hasExpr = true
			flushLit()
			l.pos++
			l.col++
			expr, err := l.captureInterpExpr(line, col)
			if err != nil {
				return Token{}, err
			}
			parts = append(parts, StringPart{IsExpr: true, Text: expr})
			continue
		case '\n':
			l.line++
			l.col = 1
			lit.WriteRune(r)
			l.pos += size
			continue
		default:
			lit.WriteRune(r)
			l.pos += size
			l.col += size
		}
	}
	return Token{}, fmt.Errorf("buzz: unterminated string at line %d:%d", line, col)
}

// lexRawString scans a backtick-quoted raw string — upstream Buzz's
// multiline string form, used above all for zdef declaration blocks. No
// escapes, no interpolation: every byte up to the closing backtick is
// literal, newlines included.
func (l *lexer) lexRawString(line, col int) (Token, error) {
	l.pos++ // opening `
	l.col++
	start := l.pos
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		if c == '`' {
			val := l.src[start:l.pos]
			l.pos++
			l.col++
			return Token{Kind: String, Val: val, Line: line, Col: col}, nil
		}
		if c == '\n' {
			l.line++
			l.col = 1
		} else {
			l.col++
		}
		l.pos++
	}
	return Token{}, fmt.Errorf("buzz: unterminated raw string at line %d:%d", line, col)
}

// lexPattern scans a $"..." pattern literal. Unlike a string, backslash escapes
// are NOT interpreted — the regex source is preserved verbatim (so \d, \w, etc.
// reach the regex engine intact) — except that a backslash defers the next byte,
// which both lets a literal \" appear inside the pattern and reaches the engine
// as \" (which it treats as a literal quote). The opening $ and " are consumed
// here; scanning stops at the first unescaped ".
func (l *lexer) lexPattern(line, col int) (Token, error) {
	l.pos++ // $
	l.col++
	l.pos++ // opening "
	l.col++
	var sb strings.Builder
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		switch r {
		case '\\':
			sb.WriteRune(r)
			l.pos += size
			l.col += size
			if l.pos < len(l.src) {
				r2, s2 := utf8.DecodeRuneInString(l.src[l.pos:])
				sb.WriteRune(r2)
				l.pos += s2
				l.col += s2
			}
		case '"':
			l.pos++
			l.col++
			return Token{Kind: Pat, Val: sb.String(), Line: line, Col: col}, nil
		case '\n':
			l.line++
			l.col = 1
			sb.WriteRune(r)
			l.pos += size
		default:
			sb.WriteRune(r)
			l.pos += size
			l.col += size
		}
	}
	return Token{}, fmt.Errorf("buzz: unterminated pattern at line %d:%d", line, col)
}

// captureInterpExpr reads source up to the matching closing brace, honoring
// nested braces and embedded strings. The opening brace is already consumed.
func (l *lexer) captureInterpExpr(line, col int) (string, error) {
	depth := 1
	var sb strings.Builder
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		switch r {
		case '{':
			depth++
			sb.WriteRune(r)
		case '}':
			depth--
			if depth == 0 {
				l.pos++
				l.col++
				return sb.String(), nil
			}
			sb.WriteRune(r)
		case '"':
			// Copy nested string verbatim so its braces aren't miscounted.
			sb.WriteRune(r)
			l.pos += size
			l.col += size
			for l.pos < len(l.src) {
				r2, s2 := utf8.DecodeRuneInString(l.src[l.pos:])
				sb.WriteRune(r2)
				l.pos += s2
				l.col += s2
				if r2 == '\\' && l.pos < len(l.src) {
					r3, s3 := utf8.DecodeRuneInString(l.src[l.pos:])
					sb.WriteRune(r3)
					l.pos += s3
					l.col += s3
					continue
				}
				if r2 == '"' {
					break
				}
			}
			continue
		case '\n':
			l.line++
			l.col = 1
			sb.WriteRune(r)
		default:
			sb.WriteRune(r)
		}
		l.pos += size
		l.col += size
	}
	return "", fmt.Errorf("buzz: unterminated interpolation at line %d:%d", line, col)
}

func (l *lexer) lexNumber(line, col int) (Token, error) {
	start := l.pos
	isFloat := false
	for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
		l.pos++
		l.col++
	}
	if l.pos < len(l.src) && l.src[l.pos] == '.' &&
		l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9' {
		isFloat = true
		l.pos++
		l.col++
		for l.pos < len(l.src) && l.src[l.pos] >= '0' && l.src[l.pos] <= '9' {
			l.pos++
			l.col++
		}
	}
	raw := l.src[start:l.pos]
	if isFloat {
		return Token{Kind: Float, Val: raw, Line: line, Col: col}, nil
	}
	return Token{Kind: Int, Val: raw, Line: line, Col: col}, nil
}

func (l *lexer) lexIdent(line, col int) (Token, error) {
	start := l.pos
	for l.pos < len(l.src) {
		r, size := utf8.DecodeRuneInString(l.src[l.pos:])
		if !isIdentContinue(r) {
			break
		}
		l.pos += size
		l.col += size
	}
	word := l.src[start:l.pos]
	if kind, ok := keywords[word]; ok {
		return Token{Kind: kind, Val: word, Line: line, Col: col}, nil
	}
	return Token{Kind: Ident, Val: word, Line: line, Col: col}, nil
}

func isIdentStart(r rune) bool {
	return r == '_' || unicode.IsLetter(r)
}

func isIdentContinue(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}
