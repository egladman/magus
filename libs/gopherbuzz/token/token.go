// Package token defines the lexical token types and scanner for Buzz source.
package token

import (
	"fmt"
	"slices"
	"strconv"
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
	FatArrow   // =>
	Arrow      // ->
	Backslash  // \
	Amp        // &
	Pipe       // |
	Caret      // ^
	Tilde      // ~
	Shl        // <<
	Shr        // >>

	// compound assignment
	PlusAssign    // +=
	MinusAssign   // -=
	StarAssign    // *=
	SlashAssign   // /=
	PercentAssign // %=
	AmpAssign     // &=
	PipeAssign    // |=
	CaretAssign   // ^=
	ShlAssign     // <<=
	ShrAssign     // >>=

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
	case FatArrow:
		return "'=>'"
	case Arrow:
		return "'->'"
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
	case Amp:
		return "'&'"
	case Pipe:
		return "'|'"
	case Caret:
		return "'^'"
	case Tilde:
		return "'~'"
	case Shl:
		return "'<<'"
	case Shr:
		return "'>>'"
	case PlusAssign:
		return "'+='"
	case MinusAssign:
		return "'-='"
	case StarAssign:
		return "'*='"
	case SlashAssign:
		return "'/='"
	case PercentAssign:
		return "'%='"
	case AmpAssign:
		return "'&='"
	case PipeAssign:
		return "'|='"
	case CaretAssign:
		return "'^='"
	case ShlAssign:
		return "'<<='"
	case ShrAssign:
		return "'>>='"
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

// Keywords returns the reserved Buzz keywords in sorted order. Editor tooling
// (completion) uses it to offer the canonical keyword set without re-deriving it,
// staying in sync with IsKeyword and the language.
func Keywords() []string {
	out := make([]string, 0, len(keywords))
	for k := range keywords {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
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
	// Raw marks an Ident that was written in the free-identifier form @"...".
	// Its spelling is whatever the quotes held, so the reserved-word rule that
	// governs ordinary identifiers does not apply to it.
	Raw bool
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
	// optimization: pre-size the token slice to avoid repeated append regrowth+copy
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
		if l.peekByte() == '=' {
			return simple(PlusAssign, 2), nil
		}
		return simple(Plus, size), nil
	case '&':
		if l.peekByte() == '=' {
			return simple(AmpAssign, 2), nil
		}
		return simple(Amp, size), nil
	case '|':
		if l.peekByte() == '=' {
			return simple(PipeAssign, 2), nil
		}
		return simple(Pipe, size), nil
	case '^':
		if l.peekByte() == '=' {
			return simple(CaretAssign, 2), nil
		}
		return simple(Caret, size), nil
	case '~':
		return simple(Tilde, size), nil
	case '*':
		if l.peekByte() == '>' {
			return simple(YieldArrow, 2), nil
		}
		if l.peekByte() == '=' {
			return simple(StarAssign, 2), nil
		}
		return simple(Star, size), nil
	case '/':
		if l.peekByte() == '=' {
			return simple(SlashAssign, 2), nil
		}
		return simple(Slash, size), nil
	case '%':
		if l.peekByte() == '=' {
			return simple(PercentAssign, 2), nil
		}
		return simple(Percent, size), nil
	case '=':
		if l.peekByte() == '=' {
			return simple(Eq, 2), nil
		}
		if l.peekByte() == '>' {
			return simple(FatArrow, 2), nil
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
		// `<<` wins over `<` here, matching upstream's scanner. It costs nested
		// generic type arguments (`::<A::<B>>` scans as `... Shr`), a limitation
		// upstream shares for the same reason.
		if l.peekByte() == '<' {
			if l.peekByteAt(2) == '=' {
				return simple(ShlAssign, 3), nil
			}
			return simple(Shl, 2), nil
		}
		if l.peekByte() == '=' {
			return simple(Le, 2), nil
		}
		return simple(Lt, size), nil
	case '>':
		if l.peekByte() == '>' {
			if l.peekByteAt(2) == '=' {
				return simple(ShrAssign, 3), nil
			}
			return simple(Shr, 2), nil
		}
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
		if l.peekByte() == '>' {
			return simple(Arrow, 2), nil
		}
		if l.peekByte() == '=' {
			return simple(MinusAssign, 2), nil
		}
		// Negative numeric literal vs minus operator is resolved by the parser
		// (unary). Always emit minus here.
		return simple(Minus, size), nil
	case '\'':
		return l.lexChar(startLine, startCol)
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
	case '@':
		// Free (raw) identifier: @"any text" names a binding, field, or member
		// whose spelling the ordinary identifier rules would reject - including a
		// reserved word or a name with punctuation in it.
		if l.peekByte() == '"' {
			l.pos++ // '@'
			l.col++
			t, err := l.lexString(startLine, startCol)
			if err != nil {
				return Token{}, err
			}
			if t.Kind != String {
				return Token{}, fmt.Errorf("buzz: line %d:%d: a free identifier cannot interpolate", startLine, startCol)
			}
			return Token{Kind: Ident, Val: t.Val, Raw: true, Line: startLine, Col: startCol}, nil
		}
		return Token{}, fmt.Errorf("buzz: unexpected character %q at line %d:%d", r, l.line, l.col)
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
func (l *lexer) peekByte() byte { return l.peekByteAt(1) }

// peekByteAt returns the byte n positions ahead of the current one, or 0. The
// three-byte compound shifts (`<<=`, `>>=`) need to look two ahead.
func (l *lexer) peekByteAt(n int) byte {
	if l.pos+n >= len(l.src) {
		return 0
	}
	return l.src[l.pos+n]
}

// lexString scans a double-quoted string, splitting on {expr} interpolation.
// lexChar scans a character literal ('A', '\n', '\”) and emits it as an Int token
// holding the rune's codepoint - upstream compares them against integers directly
// (`'\” == 39`), so a char IS an int and needs no distinct token kind, no distinct
// value tag, and no VM support at all.
func (l *lexer) lexChar(line, col int) (Token, error) {
	l.pos++ // opening quote
	l.col++
	if l.pos >= len(l.src) {
		return Token{}, fmt.Errorf("buzz: unterminated character literal at line %d:%d", line, col)
	}

	r := rune(l.src[l.pos])
	if r == '\\' {
		l.pos++
		l.col++
		if l.pos >= len(l.src) {
			return Token{}, fmt.Errorf("buzz: unterminated escape in character literal at line %d:%d", line, col)
		}
		esc, ok := charEscapes[l.src[l.pos]]
		if !ok {
			return Token{}, fmt.Errorf("buzz: unknown escape %q in character literal at line %d:%d", string(l.src[l.pos]), line, col)
		}
		r = esc
	}
	l.pos++
	l.col++

	if l.pos >= len(l.src) || l.src[l.pos] != '\'' {
		return Token{}, fmt.Errorf("buzz: unterminated character literal at line %d:%d", line, col)
	}
	l.pos++ // closing quote
	l.col++
	return Token{Kind: Int, Val: strconv.Itoa(int(r)), Line: line, Col: col}, nil
}

// allDigits reports whether every byte of s is an ASCII digit.
func allDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// charEscapes are the escapes valid inside a character literal, matching the set
// upstream's basic-types behavior test exercises.
var charEscapes = map[byte]rune{
	'n': '\n', 'r': '\r', 't': '\t', '0': 0,
	'a': 7, 'b': 8, 'f': 12, 'v': 11,
	'\'': '\'', '"': '"', '\\': '\\',
}

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
					// \NNN is a DECIMAL byte escape, not octal: upstream asserts
					// "\008" is 8 and "\012" is 12, which octal would make 8 and 10.
					// Exactly three digits, so "\0" followed by text stays unambiguous.
					if esc >= '0' && esc <= '9' && l.pos+3 <= len(l.src) && allDigits(l.src[l.pos:l.pos+3]) {
						n, err := strconv.Atoi(l.src[l.pos : l.pos+3])
						if err != nil || n > 255 {
							return Token{}, fmt.Errorf("buzz: invalid escape %q in string at line %d:%d", l.src[l.pos:l.pos+3], line, col)
						}
						lit.WriteByte(byte(n))
						l.pos += 3
						l.col += 3
						continue
					}
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

// lexRawString scans a backtick-quoted raw string — upstream Buzz's multiline
// string form, used above all for zdef declaration blocks. Every byte up to the
// closing backtick is literal, newlines included; the two exceptions are the \{
// escape and `{ ... }` interpolation, whose text is left verbatim in the value for
// the parser to split later.
func (l *lexer) lexRawString(line, col int) (Token, error) {
	l.pos++ // opening `
	l.col++
	// Structured exactly like lexString: literal runs accumulate into lit, an
	// interpolation is captured whole by captureInterpExpr, and the token is a plain
	// String only when no interpolation appeared. Scanning for the closing backtick
	// without doing this left `{3 + 12}` in the value as literal text.
	var parts []StringPart
	var lit strings.Builder
	hasExpr := false
	start := l.pos
	flushLit := func() {
		if lit.Len() > 0 {
			parts = append(parts, StringPart{Text: lit.String()})
			lit.Reset()
		}
	}
	for l.pos < len(l.src) {
		c := l.src[l.pos]
		// A raw string needs a way to write a literal brace, since it interpolates;
		// upstream spells that \{. Nothing else is unescaped - that is what "raw" means,
		// and a regex or Windows path inside one must survive intact. Handled before the
		// switch so an escaped brace never opens an interpolation.
		if c == '\\' && l.pos+1 < len(l.src) && (l.src[l.pos+1] == '{' || l.src[l.pos+1] == '}') {
			lit.WriteByte(l.src[l.pos+1])
			l.pos += 2
			l.col += 2
			continue
		}
		switch c {
		case '`':
			raw := l.src[start:l.pos]
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
			// Val carries the text EXACTLY as written, interpolation delimiters included,
			// alongside the split Parts. A zdef declaration block is a raw string full of
			// braces (`extern struct { id: c_int }`) and upstream never interpolates it:
			// zdefStatement hands the raw token to its FFI parser rather than to the string
			// parser. Keeping both lets the value path interpolate while zdef reads the
			// block verbatim (see foldStringConcat).
			return Token{Kind: InterpStr, Val: raw, Parts: parts, Line: line, Col: col}, nil
		case '{':
			hasExpr = true
			flushLit()
			l.pos++
			l.col++
			expr, err := l.captureInterpExpr(line, col)
			if err != nil {
				return Token{}, err
			}
			parts = append(parts, StringPart{IsExpr: true, Text: expr})
		case '\n':
			l.line++
			l.col = 1
			lit.WriteByte(c)
			l.pos++
		default:
			lit.WriteByte(c)
			l.pos++
			l.col++
		}
	}
	return Token{}, fmt.Errorf("buzz: unterminated raw string at line %d:%d", line, col)
}

// lexPattern scans a $"..." pattern literal. Unlike a string, backslash escapes
// are NOT interpreted — the regex source is preserved verbatim (so \d, \w, etc.
// reach the regex engine intact) — with one exception: \" collapses to a bare ",
// which is upstream's behaviour (Parser.zig's pattern() runs exactly one
// replacement, `\"` -> `"`, over the raw token slice before compiling). The
// backslash still defers the next byte either way, so an escaped delimiter does
// not terminate the literal. The opening $ and " are consumed here; scanning stops
// at the first unescaped ".
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
			l.pos += size
			l.col += size
			if l.pos < len(l.src) {
				r2, s2 := utf8.DecodeRuneInString(l.src[l.pos:])
				// Only the delimiter escape collapses; every other escape reaches the
				// regex engine with its backslash intact.
				if r2 != '"' {
					sb.WriteRune(r)
				}
				sb.WriteRune(r2)
				l.pos += s2
				l.col += s2
			} else {
				sb.WriteRune(r)
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
		case '"', '`':
			// Copy a nested string verbatim so its braces aren't miscounted. A BACKTICK
			// string counts here too: an interpolation may hold a raw string, and its
			// braces (or an unbalanced one in a zdef block) would otherwise close the
			// interpolation early.
			delim := r
			sb.WriteRune(r)
			l.pos += size
			l.col += size
			for l.pos < len(l.src) {
				r2, s2 := utf8.DecodeRuneInString(l.src[l.pos:])
				sb.WriteRune(r2)
				l.pos += s2
				if r2 == '\n' {
					l.line++
					l.col = 1
				} else {
					l.col += s2
				}
				if r2 == '\\' && l.pos < len(l.src) {
					r3, s3 := utf8.DecodeRuneInString(l.src[l.pos:])
					sb.WriteRune(r3)
					l.pos += s3
					l.col += s3
					continue
				}
				if r2 == delim {
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

// lexNumber consumes a numeric literal. Matches upstream Buzz 0.6.0-dev
// (src/Scanner.zig, functions number / hexa / binary). Accepts:
//
//   - Decimal ints and floats:  42, 1_000_000, 3.14, 1_000.5
//   - Hex ints:                 0x1a, 0xFF_FF, 0xDEADBEEF   (lowercase prefix only)
//   - Binary ints:              0b1010, 0b1100_1010          (lowercase prefix only)
//
// Underscores are permitted between digits as a readability separator, but
// must not appear at the start or end of the digit run - upstream rejects
// those with "'_' must be between digits". Upstream has NO octal prefix
// (0o) and NO float exponent syntax (e/E); this lexer matches that.
// Leading zero on a decimal stays decimal (no implicit-octal C footgun).
func (l *lexer) lexNumber(line, col int) (Token, error) {
	start := l.pos
	// Non-decimal prefix: 0x (hex) or 0b (binary). Lowercase only - upstream
	// dispatches on '0' followed by literal 'x' / 'b'.
	if l.pos+1 < len(l.src) && l.src[l.pos] == '0' {
		switch l.src[l.pos+1] {
		case 'x':
			l.pos += 2
			l.col += 2
			for l.pos < len(l.src) && isHexDigit(l.src[l.pos]) {
				l.pos++
				l.col++
			}
			raw := l.src[start:l.pos]
			if len(raw) == 2 {
				return Token{}, fmt.Errorf("buzz: line %d:%d: unterminated hexa literal", line, col)
			}
			if raw[len(raw)-1] == '_' {
				return Token{}, fmt.Errorf("buzz: line %d:%d: '_' must be between digits", line, col)
			}
			return Token{Kind: Int, Val: raw, Line: line, Col: col}, nil
		case 'b':
			l.pos += 2
			l.col += 2
			for l.pos < len(l.src) && isBinaryDigit(l.src[l.pos]) {
				l.pos++
				l.col++
			}
			raw := l.src[start:l.pos]
			if len(raw) == 2 {
				return Token{}, fmt.Errorf("buzz: line %d:%d: unterminated binary literal", line, col)
			}
			if raw[len(raw)-1] == '_' {
				return Token{}, fmt.Errorf("buzz: line %d:%d: '_' must be between digits", line, col)
			}
			return Token{Kind: Int, Val: raw, Line: line, Col: col}, nil
		}
	}
	// Decimal integer or float.
	isFloat := false
	for l.pos < len(l.src) && isDecimalDigit(l.src[l.pos]) {
		l.pos++
		l.col++
	}
	// Match upstream: '_' must not trail the integer part.
	if l.pos > start && l.src[l.pos-1] == '_' {
		return Token{}, fmt.Errorf("buzz: line %d:%d: '_' must be between digits", line, col)
	}
	if l.pos < len(l.src) && l.src[l.pos] == '.' &&
		l.pos+1 < len(l.src) && l.src[l.pos+1] >= '0' && l.src[l.pos+1] <= '9' {
		isFloat = true
		l.pos++
		l.col++
		fracStart := l.pos
		for l.pos < len(l.src) && isDecimalDigit(l.src[l.pos]) {
			l.pos++
			l.col++
		}
		if l.pos > fracStart && l.src[l.pos-1] == '_' {
			return Token{}, fmt.Errorf("buzz: line %d:%d: '_' must be between digits", line, col)
		}
	}
	raw := l.src[start:l.pos]
	if isFloat {
		return Token{Kind: Float, Val: raw, Line: line, Col: col}, nil
	}
	return Token{Kind: Int, Val: raw, Line: line, Col: col}, nil
}

// isDecimalDigit accepts 0-9 or _ (digit separator).
func isDecimalDigit(b byte) bool { return (b >= '0' && b <= '9') || b == '_' }

// isHexDigit accepts 0-9, a-f, A-F, or _.
func isHexDigit(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F') || b == '_'
}

// isBinaryDigit accepts 0, 1, or _.
func isBinaryDigit(b byte) bool { return b == '0' || b == '1' || b == '_' }

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
