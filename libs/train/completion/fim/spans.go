package fim

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"sort"
	"unicode/utf8"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	buzzast "github.com/egladman/magus/libs/gopherbuzz/ast"
	buzztoken "github.com/egladman/magus/libs/gopherbuzz/token"

	"github.com/egladman/magus/libs/train/dedup"
)

// Range is a byte range [Start, End) of a file's text.
type Range struct{ Start, End int }

// MaxMiddle is the longest middle an AST span may have. A longer function body is cut
// into its statements, which are separate candidates.
const MaxMiddle = 2000

// Constructs returns the byte ranges of the file's statements, blocks, call argument lists
// and function bodies, the candidates of the 40% AST share. Go ranges come from go/ast and
// Buzz statement starts from the gopherbuzz parser. A TypeScript range comes from a bracket
// match over dedup.LexTS: no TypeScript parser is linked here, so a statement there ends at
// a depth-zero semicolon or a closing brace. A file that does not parse answers none and
// the caller falls back loudly. Ranges are trimmed of surrounding blanks, non-empty,
// at most MaxMiddle bytes, and sorted and unique.
func Constructs(language, src string) []Range {
	var rs []Range
	switch language {
	case dedup.Go:
		rs = goConstructs(src)
	case dedup.Buzz:
		rs = buzzConstructs(src)
	case dedup.TypeScript:
		rs = tsConstructs(src)
	}
	return tidy(src, rs)
}

func tidy(src string, rs []Range) []Range {
	out := rs[:0]
	for _, r := range rs {
		r.Start, r.End = max(r.Start, 0), min(r.End, len(src))
		for r.Start < r.End && isBlank(src[r.Start]) {
			r.Start++
		}
		for r.End > r.Start && isBlank(src[r.End-1]) {
			r.End--
		}
		if r.End > r.Start && r.End-r.Start <= MaxMiddle && utf8.ValidString(src[r.Start:r.End]) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].End < out[j].End
	})
	return slices.Compact(out)
}

func isBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func goConstructs(src string) []Range {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "", src, parser.SkipObjectResolution)
	if err != nil || file == nil {
		return nil
	}
	off := func(p token.Pos) int { return fset.Position(p).Offset }
	var rs []Range
	stmts := func(list []ast.Stmt) {
		for _, s := range list {
			rs = append(rs, Range{off(s.Pos()), off(s.End())})
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BlockStmt:
			rs = append(rs, Range{off(n.Lbrace) + 1, off(n.Rbrace)})
			stmts(n.List)
		case *ast.CaseClause:
			stmts(n.Body)
		case *ast.CommClause:
			stmts(n.Body)
		case *ast.CallExpr:
			if len(n.Args) > 0 {
				rs = append(rs, Range{off(n.Lparen) + 1, off(n.Rparen)})
			}
		}
		return true
	})
	return rs
}

// buzzTokens is a Buzz file's tokens with each one's byte offset. The lexer reports a
// 1-based line and a byte column.
type buzzTokens struct {
	toks   []buzztoken.Token
	starts []int
	lines  []int
	match  map[int]int
}

func lexBuzz(src string) (*buzzTokens, bool) {
	toks, err := buzztoken.Tokenize(src)
	if err != nil {
		return nil, false
	}
	lineAt := []int{0}
	for i := 0; i < len(src); i++ {
		if src[i] == '\n' {
			lineAt = append(lineAt, i+1)
		}
	}
	b := &buzzTokens{toks: toks, starts: make([]int, len(toks)), lines: lineAt, match: map[int]int{}}
	for i, t := range toks {
		if t.Line < 1 || t.Line > len(lineAt) {
			return nil, false
		}
		b.starts[i] = lineAt[t.Line-1] + t.Col - 1
	}
	var stack []int
	for i, t := range toks {
		switch t.Kind {
		case buzztoken.LParen, buzztoken.LBrace, buzztoken.LBracket:
			stack = append(stack, i)
		case buzztoken.RParen, buzztoken.RBrace, buzztoken.RBracket:
			if len(stack) == 0 {
				return nil, false
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			b.match[open], b.match[i] = i, open
		}
	}
	return b, len(stack) == 0
}

// at is the index of the token starting at the parser position, or -1.
func (b *buzzTokens) at(p buzzast.Pos) int {
	if p.Line < 1 || p.Line > len(b.lines) {
		return -1
	}
	want := b.lines[p.Line-1] + p.Col - 1
	i := sort.SearchInts(b.starts, want)
	if i < len(b.starts) && b.starts[i] == want {
		return i
	}
	return -1
}

// endOf is the byte offset after token i, known for the closers that end a statement.
func (b *buzzTokens) endOf(i int) (int, bool) {
	switch b.toks[i].Kind {
	case buzztoken.Semicolon, buzztoken.RBrace, buzztoken.RParen, buzztoken.RBracket:
		return b.starts[i] + 1, true
	}
	return 0, false
}

func buzzConstructs(src string) []Range {
	b, ok := lexBuzz(src)
	if !ok {
		return nil
	}
	prog, err := buzz.ParseEmbedded(src)
	if err != nil || prog == nil {
		return nil
	}
	var rs []Range
	// list adds each statement of one statement list. A statement runs from its parser
	// position to the last token before the next statement, or before closeTok for the last.
	list := func(stmts []buzzast.Node, closeTok int) {
		starts := make([]int, 0, len(stmts))
		for _, s := range stmts {
			starts = append(starts, b.at(buzzast.NodePos(s)))
		}
		for i, st := range starts {
			if st < 0 {
				continue
			}
			next := closeTok
			if i+1 < len(starts) {
				next = starts[i+1]
			}
			if next <= st {
				continue
			}
			if end, ok := b.endOf(next - 1); ok {
				rs = append(rs, Range{b.starts[st], end})
			}
		}
	}
	list(prog.Stmts, len(b.toks)-1)
	for i, t := range b.toks {
		switch t.Kind {
		case buzztoken.LBrace:
			rs = append(rs, Range{b.starts[i] + 1, b.starts[b.match[i]]})
		case buzztoken.LParen:
			if i > 0 && b.match[i] > i+1 {
				if p := b.toks[i-1].Kind; p == buzztoken.Ident || p == buzztoken.RParen || p == buzztoken.RBracket {
					rs = append(rs, Range{b.starts[i] + 1, b.starts[b.match[i]]})
				}
			}
		}
	}
	for _, top := range prog.Stmts {
		buzzast.Inspect(top, func(n buzzast.Node) bool {
			if blk, ok := n.(*buzzast.BlockStmt); ok && blk != nil {
				if open := b.at(blk.Pos); open >= 0 && b.toks[open].Kind == buzztoken.LBrace {
					list(blk.Stmts, b.match[open])
				}
			}
			return true
		})
	}
	return rs
}

func tsConstructs(src string) []Range {
	lex := dedup.LexTS(src)
	text := func(i int) string { return lex[i].Text(src) }
	match := map[int]int{}
	var stack []int
	for i, l := range lex {
		if l.Kind != dedup.LexPunct {
			continue
		}
		switch text(i) {
		case "(", "{", "[":
			stack = append(stack, i)
		case ")", "}", "]":
			if len(stack) == 0 {
				return nil
			}
			open := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			match[open], match[i] = i, open
		}
	}
	if len(stack) != 0 {
		return nil
	}
	var rs []Range
	for i := range lex {
		if lex[i].Kind != dedup.LexPunct {
			continue
		}
		switch text(i) {
		case "{":
			rs = append(rs, Range{lex[i].End, lex[match[i]].Start})
		case "(":
			if i > 0 && match[i] > i+1 {
				if p := lex[i-1]; p.Kind == dedup.LexIdent || p.Kind == dedup.LexPunct && (text(i-1) == ")" || text(i-1) == "]" || text(i-1) == ">") {
					rs = append(rs, Range{lex[i].End, lex[match[i]].Start})
				}
			}
		}
	}
	// Statements: runs of tokens ending at a depth-zero semicolon, or at the brace that
	// closes a block unless the next token continues the statement.
	var walk func(from, to int)
	walk = func(from, to int) {
		start := from
		for i := from; i < to; i++ {
			if lex[i].Kind != dedup.LexPunct {
				continue
			}
			switch text(i) {
			case "(", "[":
				i = match[i]
			case "{":
				closer := match[i]
				walk(i+1, closer)
				i = closer
				if !continues(lex, src, closer+1, to) {
					rs = append(rs, Range{lex[start].Start, lex[closer].End})
					start = closer + 1
				}
			case ";":
				rs = append(rs, Range{lex[start].Start, lex[i].End})
				start = i + 1
			}
			if start >= to {
				return
			}
		}
	}
	if len(lex) > 0 {
		walk(0, len(lex))
	}
	return rs
}

// continues reports whether the token at i keeps the statement a closing brace ended
// going: else, catch, finally, a call or member access, or a delimiter.
func continues(lex []dedup.Lexeme, src string, i, to int) bool {
	if i >= to {
		return false
	}
	switch lex[i].Text(src) {
	case "else", "catch", "finally", "while", ".", ",", ")", ";", "(", "?", ":", "=", "as", "satisfies":
		return true
	}
	return false
}
