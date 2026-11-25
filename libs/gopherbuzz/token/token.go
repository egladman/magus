// Package token defines the lexical token types and scanner for Buzz source.
package token

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Kind identifies the category of a lexical token.
type Kind uint8

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
	// Line and Col place an expression's first token for diagnostics, in
	// upstream's convention (StringParser.zig): Col is the column of the `{`
	// itself, and the text of earlier expressions on the same line is not
	// counted, because upstream's string scanner never advances over it. Zero
	// for a literal run.
	Line, Col int
}

// Token is a single lexical token.
type Token struct {
	// optimization: Kind is a byte and sits beside At, sharing one padded word.
	//   measured: unsafe.Sizeof(Token{}) 88 -> 80 B; describe targets retains
	//   the cached tokens of every module it loads, 1.6 MB less.
	//   trade-off: fields are not in reading order.
	Kind Kind
	// At marks an Ident written in the free-identifier form @"...". Its spelling
	// is whatever the quotes held, so the reserved-word rule does not apply.
	At    bool
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

// scanner tokenizes a Buzz source string.
type scanner struct {
	host   *Scanner // nil scans upstream Buzz
	src    string
	pos    int
	line   int
	col    int
	tokens []Token
	// pendingDoc holds the lines of the most recent contiguous comment block as
	// slices of src; pendingDocLine is the source line of its last comment line.
	// tokenize joins them onto the next token only when that token begins on
	// pendingDocLine+1 (no blank-line gap).
	pendingDoc     []string
	pendingDocLine int
	// lastTokenLine is the end line of the most recently emitted token. A comment
	// that begins on this line is a trailing comment (code precedes it), not a
	// leading doc, so it is not recorded as pending doc.
	lastTokenLine int
}

// scratch holds token buffers between Tokenize calls. A buffer is returned zeroed,
// so it pins no source text or doc string while it waits.
var scratch = sync.Pool{New: func() any { return new([]Token) }}

// maxScratch bounds the buffer the pool keeps to 1.3 MB, enough for about 64 KB
// of source; describe targets lexes three modules larger than that. A bound of
// 1<<16 kept 4 MB more live at exit for 2 MB less allocated.
const maxScratch = 1 << 14

// Tokenize lexes src as upstream Buzz: it is (*Scanner)(nil).Tokenize.
func Tokenize(src string) ([]Token, error) {
	return (*Scanner)(nil).Tokenize(src)
}

// Tokenize lexes src and returns the complete token stream including EOF. A nil
// s scans upstream Buzz. The returned slice has no spare capacity and belongs to
// the caller. Safe for concurrent use.
func (s *Scanner) Tokenize(src string) ([]Token, error) {
	// optimization: lex into a pooled buffer and return an exact-length copy.
	//   measured: BenchmarkTokenize -44% B/op, BenchmarkParseCacheMiss -41% B/op
	//   (benchstat, n=10); in describe targets the 50 MB newLexer allocated plus
	//   the cache's 17 MB clone became 38 MB (GODEBUG=memprofilerate=1).
	//   trade-off: one memmove of the tokens; a pooled buffer sized to the
	//   largest recent source lives until the second GC after its last use.
	buf := scratch.Get().(*[]Token)
	// Magus's own sources average 8.1 bytes per token and none passes 4, so a
	// buffer this size never regrows mid-lex.
	if bound := len(src)/4 + 16; cap(*buf) < bound {
		*buf = make([]Token, 0, bound)
	}
	sc := scanner{host: s, src: src, line: 1, col: 1, tokens: (*buf)[:0]}
	err := sc.tokenize()
	var out []Token
	if err == nil {
		out = make([]Token, len(sc.tokens))
		copy(out, sc.tokens)
	}
	clear(sc.tokens)
	if cap(sc.tokens) <= maxScratch {
		*buf = sc.tokens[:0]
		scratch.Put(buf)
	}
	return out, err
}

func (sc *scanner) tokenize() error {
	for {
		sc.skipWhitespaceAndComments()
		if sc.pos >= len(sc.src) {
			sc.tokens = append(sc.tokens, Token{Kind: EOF, Line: sc.line, Col: sc.col})
			return nil
		}
		r, size := utf8.DecodeRuneInString(sc.src[sc.pos:])
		tok, err := sc.nextToken(r, size)
		if err != nil {
			return err
		}
		// Attach a pending doc block only to the token directly below it (no blank
		// line); either way the pending block is consumed so it can't leak onto a
		// later token.
		if sc.hasPendingDoc() {
			if tok.Line == sc.pendingDocLine+1 {
				tok.Doc = sc.joinPendingDoc()
			}
			sc.pendingDoc = sc.pendingDoc[:0]
		}
		sc.tokens = append(sc.tokens, tok)
		sc.lastTokenLine = sc.line
	}
}

// hasPendingDoc reports whether the pending block joins to a non-empty doc. A
// block that starts with an empty comment line restarts at its next line, so an
// empty first line only ever appears alone.
func (sc *scanner) hasPendingDoc() bool {
	return len(sc.pendingDoc) > 0 && sc.pendingDoc[0] != ""
}

func (sc *scanner) joinPendingDoc() string {
	if len(sc.pendingDoc) == 1 {
		return sc.pendingDoc[0]
	}
	return strings.Join(sc.pendingDoc, "\n")
}

// recordDoc adds a just-scanned comment's text to the pending block. A line gap
// from the previous comment line starts a fresh block, so only a contiguous run is
// kept.
func (sc *scanner) recordDoc(text string, line int) {
	// optimization: lines are joined once, and only for a block that lands on a
	// token; most comments (headers, section notes, anything above a blank line)
	// never do.
	//   measured: describe targets doc text 4.8 MB -> 0.7 MB allocated
	//   (GODEBUG=memprofilerate=1).
	//   trade-off: a scanner-lifetime slice of line strings.
	if !sc.hasPendingDoc() || line != sc.pendingDocLine+1 {
		sc.pendingDoc = sc.pendingDoc[:0]
	}
	sc.pendingDoc = append(sc.pendingDoc, text)
	sc.pendingDocLine = line
}

func (sc *scanner) skipWhitespaceAndComments() {
	for sc.pos < len(sc.src) {
		r, size := utf8.DecodeRuneInString(sc.src[sc.pos:])
		if r == '\n' {
			sc.pos++
			sc.line++
			sc.col = 1
			continue
		}
		if unicode.IsSpace(r) {
			sc.pos += size
			sc.col += size
			continue
		}
		if r == '/' && sc.pos+1 < len(sc.src) && sc.src[sc.pos+1] == '/' {
			commentLine := sc.line
			start := sc.pos + 2
			for sc.pos < len(sc.src) && sc.src[sc.pos] != '\n' {
				sc.pos++
			}
			sc.col += sc.pos - start + 2
			if commentLine != sc.lastTokenLine { // skip trailing comments
				sc.recordDoc(strings.TrimSpace(sc.src[start:sc.pos]), commentLine)
			}
			continue
		}
		if r == '/' && sc.pos+1 < len(sc.src) && sc.src[sc.pos+1] == '*' {
			commentLine := sc.line
			sc.pos += 2
			sc.col += 2
			start := sc.pos
			for sc.pos < len(sc.src) {
				if sc.src[sc.pos] == '\n' {
					sc.line++
					sc.col = 1
					sc.pos++
					continue
				}
				if sc.pos+1 < len(sc.src) && sc.src[sc.pos] == '*' && sc.src[sc.pos+1] == '/' {
					if commentLine != sc.lastTokenLine { // skip trailing comments
						sc.recordDoc(strings.TrimSpace(sc.src[start:sc.pos]), sc.line)
					}
					sc.pos += 2
					sc.col += 2
					break
				}
				sc.pos++
				sc.col++
			}
			continue
		}
		break
	}
}

func (sc *scanner) nextToken(r rune, size int) (Token, error) {
	startLine := sc.line
	startCol := sc.col
	simple := func(k Kind, n int) Token {
		sc.advance(n)
		return Token{Kind: k, Line: startLine, Col: startCol}
	}

	if sc.host != nil && r < utf8.RuneSelf && sc.host.prefixes[r] {
		lit, n, err := sc.claim()
		if err != nil {
			return Token{}, err
		}
		if n > 0 {
			sc.skip(n)
			return Token{Kind: String, Val: lit, Line: startLine, Col: startCol}, nil
		}
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
		if sc.peekByte() == '.' {
			return simple(DotDot, 2), nil
		}
		return simple(Dot, size), nil
	case '+':
		if sc.peekByte() == '=' {
			return simple(PlusAssign, 2), nil
		}
		return simple(Plus, size), nil
	case '&':
		if sc.peekByte() == '=' {
			return simple(AmpAssign, 2), nil
		}
		return simple(Amp, size), nil
	case '|':
		if sc.peekByte() == '=' {
			return simple(PipeAssign, 2), nil
		}
		return simple(Pipe, size), nil
	case '^':
		if sc.peekByte() == '=' {
			return simple(CaretAssign, 2), nil
		}
		return simple(Caret, size), nil
	case '~':
		return simple(Tilde, size), nil
	case '*':
		if sc.peekByte() == '>' {
			return simple(YieldArrow, 2), nil
		}
		if sc.peekByte() == '=' {
			return simple(StarAssign, 2), nil
		}
		return simple(Star, size), nil
	case '/':
		if sc.peekByte() == '=' {
			return simple(SlashAssign, 2), nil
		}
		return simple(Slash, size), nil
	case '%':
		if sc.peekByte() == '=' {
			return simple(PercentAssign, 2), nil
		}
		return simple(Percent, size), nil
	case '=':
		if sc.peekByte() == '=' {
			return simple(Eq, 2), nil
		}
		if sc.peekByte() == '>' {
			return simple(FatArrow, 2), nil
		}
		return simple(Assign, size), nil
	case '!':
		if sc.peekByte() == '=' {
			return simple(Neq, 2), nil
		}
		if sc.peekByte() == '>' {
			return simple(ErrArrow, 2), nil
		}
		return simple(Bang, size), nil
	case '<':
		// `<<` wins over `<` here, matching upstream's scanner. It costs nested
		// generic type arguments (`::<A::<B>>` scans as `... Shr`), a limitation
		// upstream shares for the same reason.
		if sc.peekByte() == '<' {
			if sc.peekByteAt(2) == '=' {
				return simple(ShlAssign, 3), nil
			}
			return simple(Shl, 2), nil
		}
		if sc.peekByte() == '=' {
			return simple(Le, 2), nil
		}
		return simple(Lt, size), nil
	case '>':
		if sc.peekByte() == '>' {
			if sc.peekByteAt(2) == '=' {
				return simple(ShrAssign, 3), nil
			}
			return simple(Shr, 2), nil
		}
		if sc.peekByte() == '=' {
			return simple(Ge, 2), nil
		}
		return simple(Gt, size), nil
	case '?':
		if sc.peekByte() == '?' {
			return simple(Coalesce, 2), nil
		}
		return simple(Question, size), nil
	case '-':
		if sc.peekByte() == '>' {
			return simple(Arrow, 2), nil
		}
		if sc.peekByte() == '=' {
			return simple(MinusAssign, 2), nil
		}
		// Negative numeric literal vs minus operator is resolved by the parser
		// (unary). Always emit minus here.
		return simple(Minus, size), nil
	case '\'':
		return sc.lexChar(startLine, startCol)
	case '"':
		return sc.lexString(startLine, startCol)
	case '`':
		return sc.lexMultilineString(startLine, startCol)
	case '$':
		if sc.peekByte() == '"' {
			return sc.lexPattern(startLine, startCol)
		}
		return Token{}, fmt.Errorf("buzz: unexpected character %q at line %d:%d", r, sc.line, sc.col)
	case '\\':
		return simple(Backslash, size), nil
	case '@':
		// Free identifier: @"any text" names a binding, field, or member that the
		// ordinary identifier rules would reject. A reserved word qualifies, and so
		// does a name with punctuation in it.
		if sc.peekByte() == '"' {
			sc.pos++ // '@'
			sc.col++
			t, err := sc.lexString(startLine, startCol)
			if err != nil {
				return Token{}, err
			}
			if t.Kind != String {
				return Token{}, fmt.Errorf("buzz: line %d:%d: a free identifier cannot interpolate", startLine, startCol)
			}
			return Token{Kind: Ident, Val: t.Val, At: true, Line: startLine, Col: startCol}, nil
		}
		return Token{}, fmt.Errorf("buzz: unexpected character %q at line %d:%d", r, sc.line, sc.col)
	}

	if r >= '0' && r <= '9' {
		return sc.lexNumber(startLine, startCol)
	}
	if isIdentStart(r) {
		return sc.lexIdent(startLine, startCol)
	}
	return Token{}, fmt.Errorf("buzz: unexpected character %q at line %d:%d", r, sc.line, sc.col)
}

func (sc *scanner) advance(n int) {
	sc.pos += n
	sc.col += n
}

// peekByte returns the byte one position ahead of the current one, or 0.
func (sc *scanner) peekByte() byte { return sc.peekByteAt(1) }

// peekByteAt returns the byte n positions ahead of the current one, or 0. The
// three-byte compound shifts (`<<=`, `>>=`) need to look two ahead.
func (sc *scanner) peekByteAt(n int) byte {
	if sc.pos+n >= len(sc.src) {
		return 0
	}
	return sc.src[sc.pos+n]
}

// lexString scans a double-quoted string, splitting on {expr} interpolation.
// lexChar scans a character literal ('A', '\n', '\”) and emits it as an Int token
// holding the rune's codepoint; upstream compares them against integers directly
// (`'\” == 39`), so a char IS an int and needs no distinct token kind, no distinct
// value tag, and no VM support at all.
func (sc *scanner) lexChar(line, col int) (Token, error) {
	sc.pos++ // opening quote
	sc.col++
	if sc.pos >= len(sc.src) {
		return Token{}, fmt.Errorf("buzz: unterminated character literal at line %d:%d", line, col)
	}

	r := rune(sc.src[sc.pos])
	if r == '\\' {
		sc.pos++
		sc.col++
		if sc.pos >= len(sc.src) {
			return Token{}, fmt.Errorf("buzz: unterminated escape in character literal at line %d:%d", line, col)
		}
		esc, ok := charEscapes[sc.src[sc.pos]]
		if !ok {
			return Token{}, fmt.Errorf("buzz: unknown escape %q in character literal at line %d:%d", string(sc.src[sc.pos]), line, col)
		}
		r = esc
	}
	if sc.src[sc.pos] == '\n' {
		sc.line++
		sc.col = 1
	} else {
		sc.col++
	}
	sc.pos++

	if sc.pos >= len(sc.src) || sc.src[sc.pos] != '\'' {
		return Token{}, fmt.Errorf("buzz: unterminated character literal at line %d:%d", line, col)
	}
	sc.pos++ // closing quote
	sc.col++
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

func (sc *scanner) lexString(line, col int) (Token, error) {
	sc.pos++ // opening "
	sc.col++
	var parts []StringPart
	// optimization: a literal run slices src until an escape copies it into lit.
	// Multiline strings and captureInterpExpr slice src the same way.
	//   measured: BenchmarkTokenize 1446 -> 575 allocs/op (benchstat, n=10).
	//   trade-off: a literal pins its source text, as identifiers already do.
	var lit strings.Builder
	escaped := false
	runStart := sc.pos
	hasExpr := false
	hidden := 0 // see StringPart.Col

	flushLit := func() {
		if escaped {
			parts = append(parts, StringPart{Text: lit.String()})
			lit.Reset()
			escaped = false
		} else if sc.pos > runStart {
			parts = append(parts, StringPart{Text: validRunes(sc.src[runStart:sc.pos])})
		}
	}

	for sc.pos < len(sc.src) {
		r, size := utf8.DecodeRuneInString(sc.src[sc.pos:])
		switch r {
		case '"':
			flushLit()
			sc.pos++
			sc.col++
			if !hasExpr {
				s := ""
				if len(parts) == 1 {
					s = parts[0].Text
				}
				return Token{Kind: String, Val: s, Line: line, Col: col}, nil
			}
			return Token{Kind: InterpStr, Parts: parts, Line: line, Col: col}, nil
		case '\\':
			if sc.pos+1 < len(sc.src) {
				if !escaped {
					lit.WriteString(validRunes(sc.src[runStart:sc.pos]))
					escaped = true
				}
				sc.pos++
				sc.col++
				esc, esz := utf8.DecodeRuneInString(sc.src[sc.pos:])
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
					if esc >= '0' && esc <= '9' && sc.pos+3 <= len(sc.src) && allDigits(sc.src[sc.pos:sc.pos+3]) {
						n, err := strconv.Atoi(sc.src[sc.pos : sc.pos+3])
						if err != nil || n > 255 {
							return Token{}, fmt.Errorf("buzz: invalid escape %q in string at line %d:%d", sc.src[sc.pos:sc.pos+3], line, col)
						}
						lit.WriteByte(byte(n))
						sc.pos += 3
						sc.col += 3
						continue
					}
					lit.WriteRune('\\')
					lit.WriteRune(esc)
				}
				sc.pos += esz
				if esc == '\n' {
					sc.line++
					sc.col = 1
					hidden = 0
				} else {
					sc.col += esz
				}
				continue
			}
			return Token{}, fmt.Errorf("buzz: dangling escape in string at line %d:%d", line, col)
		case '{':
			// Begin interpolation: capture balanced expression source.
			hasExpr = true
			flushLit()
			sc.pos++
			sc.col++
			exprLine, exprCol := sc.line, sc.col-1-hidden
			expr, err := sc.captureInterpExpr(line, col, true)
			if err != nil {
				return Token{}, err
			}
			parts = append(parts, StringPart{IsExpr: true, Text: expr, Line: exprLine, Col: exprCol})
			hidden = hiddenAfter(hidden, expr)
			runStart = sc.pos
			continue
		case '\n':
			sc.line++
			sc.col = 1
			hidden = 0
			if escaped {
				lit.WriteRune(r)
			}
			sc.pos += size
			continue
		default:
			if escaped {
				lit.WriteRune(r)
			}
			sc.pos += size
			sc.col += size
		}
	}
	return Token{}, fmt.Errorf("buzz: unterminated string at line %d:%d", line, col)
}

// hiddenAfter adds an expression's text to the columns upstream skips on the
// current line. Text spanning a line counts only what follows its last newline.
func hiddenAfter(hidden int, expr string) int {
	if i := strings.LastIndexByte(expr, '\n'); i >= 0 {
		return len(expr) - i - 1
	}
	return hidden + len(expr)
}

// validRunes returns s, with each byte that is not valid UTF-8 replaced by
// U+FFFD as decoding it rune by rune would. Valid input, the norm, is returned
// without a copy.
func validRunes(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	var sb strings.Builder
	sb.Grow(len(s))
	for _, r := range s {
		sb.WriteRune(r)
	}
	return sb.String()
}

// lexMultilineString scans upstream's backtick form, which zdef blocks use most.
// Every byte up to the closing backtick is literal, newlines included, except
// the \{ escape and `{ ... }` interpolation. The parser splits those later.
func (sc *scanner) lexMultilineString(line, col int) (Token, error) {
	sc.pos++ // opening `
	sc.col++
	// Structured exactly like lexString: literal runs accumulate into lit, an
	// interpolation is captured whole by captureInterpExpr, and the token is a plain
	// String only when no interpolation appeared. Scanning for the closing backtick
	// without doing this left `{3 + 12}` in the value as literal text.
	var parts []StringPart
	var lit strings.Builder
	escaped := false
	hasExpr := false
	hidden := 0 // see StringPart.Col
	start := sc.pos
	runStart := sc.pos
	flushLit := func() {
		if escaped {
			parts = append(parts, StringPart{Text: lit.String()})
			lit.Reset()
			escaped = false
		} else if sc.pos > runStart {
			parts = append(parts, StringPart{Text: sc.src[runStart:sc.pos]})
		}
	}
	for sc.pos < len(sc.src) {
		c := sc.src[sc.pos]
		// Upstream spells a literal brace \{. Nothing else unescapes, so a regex or
		// Windows path survives intact. Checked first so \{ never interpolates.
		if c == '\\' && sc.pos+1 < len(sc.src) && (sc.src[sc.pos+1] == '{' || sc.src[sc.pos+1] == '}') {
			if !escaped {
				lit.WriteString(sc.src[runStart:sc.pos])
				escaped = true
			}
			lit.WriteByte(sc.src[sc.pos+1])
			sc.pos += 2
			sc.col += 2
			continue
		}
		switch c {
		case '`':
			written := sc.src[start:sc.pos]
			flushLit()
			sc.pos++
			sc.col++
			if !hasExpr {
				s := ""
				if len(parts) == 1 {
					s = parts[0].Text
				}
				return Token{Kind: String, Val: s, Line: line, Col: col}, nil
			}
			// Val keeps the text as written beside Parts. Upstream never interpolates
			// a zdef block, so zdef reads Val (see foldStringConcat).
			return Token{Kind: InterpStr, Val: written, Parts: parts, Line: line, Col: col}, nil
		case '{':
			hasExpr = true
			flushLit()
			sc.pos++
			sc.col++
			exprLine, exprCol := sc.line, sc.col-1-hidden
			expr, err := sc.captureInterpExpr(line, col, false)
			if err != nil {
				return Token{}, err
			}
			parts = append(parts, StringPart{IsExpr: true, Text: expr, Line: exprLine, Col: exprCol})
			hidden = hiddenAfter(hidden, expr)
			runStart = sc.pos
		case '\n':
			sc.line++
			sc.col = 1
			hidden = 0
			if escaped {
				lit.WriteByte(c)
			}
			sc.pos++
		default:
			if escaped {
				lit.WriteByte(c)
			}
			sc.pos++
			sc.col++
		}
	}
	return Token{}, fmt.Errorf("buzz: unterminated string at line %d:%d", line, col)
}

// lexPattern scans a $"..." pattern literal. Unlike a string, backslash escapes
// are NOT interpreted — the regex source is preserved verbatim (so \d, \w, etc.
// reach the regex engine intact) — with one exception: \" collapses to a bare ",
// which is upstream's behaviour (Parser.zig's pattern() runs exactly one
// replacement, `\"` -> `"`, over the raw token slice before compiling). The
// backslash still defers the next byte either way, so an escaped delimiter does
// not terminate the literal. The opening $ and " are consumed here; scanning stops
// at the first unescaped ".
func (sc *scanner) lexPattern(line, col int) (Token, error) {
	sc.pos++ // $
	sc.col++
	sc.pos++ // opening "
	sc.col++
	var sb strings.Builder
	for sc.pos < len(sc.src) {
		r, size := utf8.DecodeRuneInString(sc.src[sc.pos:])
		switch r {
		case '\\':
			sc.pos += size
			sc.col += size
			if sc.pos < len(sc.src) {
				r2, s2 := utf8.DecodeRuneInString(sc.src[sc.pos:])
				// Only the delimiter escape collapses; every other escape reaches the
				// regex engine with its backslash intact.
				if r2 != '"' {
					sb.WriteRune(r)
				}
				sb.WriteRune(r2)
				sc.pos += s2
				if r2 == '\n' {
					sc.line++
					sc.col = 1
				} else {
					sc.col += s2
				}
			} else {
				sb.WriteRune(r)
			}
		case '"':
			sc.pos++
			sc.col++
			return Token{Kind: Pat, Val: sb.String(), Line: line, Col: col}, nil
		case '\n':
			sc.line++
			sc.col = 1
			sb.WriteRune(r)
			sc.pos += size
		default:
			sb.WriteRune(r)
			sc.pos += size
			sc.col += size
		}
	}
	return Token{}, fmt.Errorf("buzz: unterminated pattern at line %d:%d", line, col)
}

// captureInterpExpr returns the source up to the brace matching one already
// consumed. With nest it captures interpolations inside nested strings too, as
// upstream's scanInterpolation does. A multiline string passes false, since
// its braces may hold JSON or a template.
func (sc *scanner) captureInterpExpr(line, col int, nest bool) (string, error) {
	depth := 1
	start := sc.pos
	for sc.pos < len(sc.src) {
		r, size := utf8.DecodeRuneInString(sc.src[sc.pos:])
		// A host literal may hold `"` or `}`, so it is skipped whole. A bad one is
		// left to the parser, which lexes the hole again and reports it.
		if sc.host != nil && r < utf8.RuneSelf && sc.host.prefixes[r] && !sc.inIdent(start) {
			if _, n, err := sc.claim(); err == nil && n > 0 {
				sc.skip(n)
				continue
			}
		}
		switch r {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				expr := validRunes(sc.src[start:sc.pos])
				sc.pos++
				sc.col++
				return expr, nil
			}
		case '"', '`':
			// Skip a nested string whole so its braces aren't miscounted. Upstream
			// lets \` escape a backtick here, though not at the top level.
			delim := r
			sc.pos += size
			sc.col += size
			for sc.pos < len(sc.src) {
				r2, s2 := utf8.DecodeRuneInString(sc.src[sc.pos:])
				sc.pos += s2
				if r2 == '\n' {
					sc.line++
					sc.col = 1
				} else {
					sc.col += s2
				}
				if r2 == '\\' && sc.pos < len(sc.src) {
					r3, s3 := utf8.DecodeRuneInString(sc.src[sc.pos:])
					sc.pos += s3
					if r3 == '\n' {
						sc.line++
						sc.col = 1
					} else {
						sc.col += s3
					}
					continue
				}
				if r2 == '{' && nest {
					if _, err := sc.captureInterpExpr(line, col, nest); err != nil {
						return "", err
					}
					continue
				}
				if r2 == delim {
					break
				}
			}
			continue
		case '\n':
			sc.line++
			sc.col = 1
			sc.pos += size
			continue
		}
		sc.pos += size
		sc.col += size
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
// must not appear at the start or end of the digit run; upstream rejects
// those with "'_' must be between digits". Upstream has NO octal prefix
// (0o) and NO float exponent syntax (e/E); this scanner matches that.
// Leading zero on a decimal stays decimal (no implicit-octal C footgun).
func (sc *scanner) lexNumber(line, col int) (Token, error) {
	start := sc.pos
	// Non-decimal prefix: 0x (hex) or 0b (binary). Lowercase only: upstream
	// dispatches on '0' followed by literal 'x' / 'b'.
	if sc.pos+1 < len(sc.src) && sc.src[sc.pos] == '0' {
		switch sc.src[sc.pos+1] {
		case 'x':
			sc.pos += 2
			sc.col += 2
			for sc.pos < len(sc.src) && isHexDigit(sc.src[sc.pos]) {
				sc.pos++
				sc.col++
			}
			raw := sc.src[start:sc.pos]
			if len(raw) == 2 {
				return Token{}, fmt.Errorf("buzz: line %d:%d: unterminated hexa literal", line, col)
			}
			// BOTH ends: a separator has to sit between digits, so `0x_100` is as
			// wrong as `0x100_`. Only the trailing case was checked, which let the
			// leading form through as a valid literal.
			if raw[2] == '_' || raw[len(raw)-1] == '_' {
				return Token{}, fmt.Errorf("buzz: line %d:%d: '_' must be between digits", line, col)
			}
			return Token{Kind: Int, Val: raw, Line: line, Col: col}, nil
		case 'b':
			sc.pos += 2
			sc.col += 2
			for sc.pos < len(sc.src) && isBinaryDigit(sc.src[sc.pos]) {
				sc.pos++
				sc.col++
			}
			raw := sc.src[start:sc.pos]
			if len(raw) == 2 {
				return Token{}, fmt.Errorf("buzz: line %d:%d: unterminated binary literal", line, col)
			}
			// See the hex arm: leading separator, not only trailing.
			if raw[2] == '_' || raw[len(raw)-1] == '_' {
				return Token{}, fmt.Errorf("buzz: line %d:%d: '_' must be between digits", line, col)
			}
			return Token{Kind: Int, Val: raw, Line: line, Col: col}, nil
		}
	}
	// Decimal integer or float.
	isFloat := false
	for sc.pos < len(sc.src) && isDecimalDigit(sc.src[sc.pos]) {
		sc.pos++
		sc.col++
	}
	// Match upstream: '_' must not trail the integer part.
	if sc.pos > start && sc.src[sc.pos-1] == '_' {
		return Token{}, fmt.Errorf("buzz: line %d:%d: '_' must be between digits", line, col)
	}
	if sc.pos < len(sc.src) && sc.src[sc.pos] == '.' &&
		sc.pos+1 < len(sc.src) && sc.src[sc.pos+1] >= '0' && sc.src[sc.pos+1] <= '9' {
		isFloat = true
		sc.pos++
		sc.col++
		fracStart := sc.pos
		for sc.pos < len(sc.src) && isDecimalDigit(sc.src[sc.pos]) {
			sc.pos++
			sc.col++
		}
		if sc.pos > fracStart && sc.src[sc.pos-1] == '_' {
			return Token{}, fmt.Errorf("buzz: line %d:%d: '_' must be between digits", line, col)
		}
	}
	raw := sc.src[start:sc.pos]
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

func (sc *scanner) lexIdent(line, col int) (Token, error) {
	start := sc.pos
	for sc.pos < len(sc.src) {
		r, size := utf8.DecodeRuneInString(sc.src[sc.pos:])
		if !isIdentContinue(r) {
			break
		}
		sc.pos += size
		sc.col += size
	}
	word := sc.src[start:sc.pos]
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
