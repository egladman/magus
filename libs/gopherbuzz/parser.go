package buzz

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/token"
)

// maxParseDepth limits expression nesting to prevent stack overflow on
// adversarial input like `(((((...))))`.
const maxParseDepth = 200

// parser produces a Program AST from a token stream.
type parser struct {
	tokens []token.Token
	pos    int
	depth  int
	// strict enables the script-conformance rules upstream Buzz enforces: no
	// control-flow statements at the program top level, and labeled call
	// arguments. On by default via Parse (upstream parity); ParseEmbedded clears
	// it for gopherbuzz's embedded use (REPL/eval/magusfiles), where top-level
	// statements are the whole point.
	strict bool
	// fromDepth counts the `from { ... }` block expressions currently open, which
	// is what makes `out` a statement keyword inside one and an ordinary
	// identifier everywhere else.
	fromDepth int
	// typeTextSkips are half-open token spans that skipType consumed but that
	// readType must leave out of the annotation text it reconstructs (a function
	// type's `!>` error set and `*>` yield type). Append-only for the parse.
	typeTextSkips [][2]int
}

func newParser(tokens []token.Token) *parser {
	return &parser{tokens: tokens}
}

// Parse tokenizes src and returns a Program using upstream Buzz's rules: the
// program top level may contain only declarations, imports, and expression
// statements (no control flow), and call arguments after the first must be
// labeled. This is the default because it matches upstream — leniency is the
// deviation, not strictness, so it must be opted into explicitly (ParseEmbedded).
func Parse(src string) (*ast.Program, error) {
	toks, err := token.Tokenize(src)
	if err != nil {
		return nil, err
	}
	p := newParser(toks)
	p.strict = true
	return p.parseProgram()
}

// ParseEmbedded relaxes the two script-conformance rules Parse enforces (top-level
// statements and labeled args) for gopherbuzz's embedded use: the REPL, magus
// eval, magusfile loading, and interactive snippets, where top-level statements
// are the whole point. It is the named, deliberate deviation from upstream Buzz.
func ParseEmbedded(src string) (*ast.Program, error) {
	toks, err := token.Tokenize(src)
	if err != nil {
		return nil, err
	}
	return newParser(toks).parseProgram()
}

// parseModed parses src strict (Parse) or embedded (ParseEmbedded).
func parseModed(src string, strict bool) (*ast.Program, error) {
	if strict {
		return Parse(src)
	}
	return ParseEmbedded(src)
}

func (p *parser) peek() token.Token {
	if p.pos >= len(p.tokens) {
		return token.Token{Kind: token.EOF}
	}
	return p.tokens[p.pos]
}

func (p *parser) peekAt(n int) token.Token {
	if p.pos+n >= len(p.tokens) {
		return token.Token{Kind: token.EOF}
	}
	return p.tokens[p.pos+n]
}

func (p *parser) advance() token.Token {
	t := p.peek()
	if t.Kind != token.EOF {
		p.pos++
	}
	return t
}

func (p *parser) check(k token.Kind) bool { return p.peek().Kind == k }

func (p *parser) eat(k token.Kind) (token.Token, error) {
	t := p.peek()
	if t.Kind != k {
		return token.Token{}, fmt.Errorf("buzz: line %d:%d: expected %s, got %s", t.Line, t.Col, k, t.Kind)
	}
	p.advance()
	return t, nil
}

func (p *parser) eatIdent() (token.Token, error) {
	t := p.peek()
	if t.Kind != token.Ident {
		return token.Token{}, fmt.Errorf("buzz: line %d:%d: expected identifier, got %s", t.Line, t.Col, t.Kind)
	}
	p.advance()
	return t, nil
}

// reservedIdents are words upstream Buzz reserves as keywords. gopherbuzz lexes
// them as identifiers (it does not use all of them as keywords), but for strict
// parity it must reject them as BINDING names — var/final/fun/param/object/field/
// enum/case/namespace/loop-var. They remain usable in non-binding positions
// (member access, map keys, type names) exactly as upstream allows.
//
// `test` is the one deliberate omission: it is a contextual soft keyword here (see
// parseStmt) because every magus target set defines `export fun test(...)`, the
// canonical test target. Reserving it would break the magus CLI's core target
// model, so test stays usable as a binding name — the single justified place
// gopherbuzz is intentionally a superset of upstream.
var reservedIdents = map[string]bool{
	"out": true, "from": true, "match": true, "pat": true,
	"fib": true, "rg": true, "obj": true, "ud": true, "zdef": true,
	"typeof": true, "type": true, "protocol": true, "static": true,
	"extern": true, "double": true, "any": true, "Function": true,
	"int": true, "str": true, "bool": true, "void": true,
}

// IsReservedIdent reports whether name is a word upstream Buzz reserves, so it
// cannot be used as a plain binding name. A code GENERATOR emitting Buzz needs
// this: a Go field named Type mirrors to a field named `type`, which does not
// parse, and the generator has to reach for a free identifier (@"type") instead.
// Exported so there is one list rather than a copy that silently drifts from the
// parser's.
func IsReservedIdent(name string) bool { return reservedIdents[name] }

// eatBindingIdent consumes an identifier used as a binding name and rejects any
// upstream-reserved word (strict parity with upstream Buzz).
func (p *parser) eatBindingIdent() (token.Token, error) {
	t, err := p.eatIdent()
	if err != nil {
		return t, err
	}
	// A free identifier (@"...") carries its spelling in quotes precisely so a
	// reserved word can be used as a name, so the rule does not apply to it.
	if !t.Raw && reservedIdents[t.Val] {
		return t, fmt.Errorf("buzz: line %d:%d: %q is a reserved word and cannot be used as a name", t.Line, t.Col, t.Val)
	}
	return t, nil
}

// optSemicolon consumes a trailing semicolon if present.
func (p *parser) optSemicolon() {
	if p.check(token.Semicolon) {
		p.advance()
	}
}

func (p *parser) parseProgram() (*ast.Program, error) {
	prog := &ast.Program{}
	for !p.check(token.EOF) {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		if s != nil {
			if p.strict {
				if err := checkTopLevelStmt(s); err != nil {
					return nil, err
				}
			}
			prog.Stmts = append(prog.Stmts, s)
		}
	}
	return prog, nil
}

// checkTopLevelStmt enforces the strict-mode rule that a program's top level
// holds only declarations, imports, and expression statements — matching upstream
// Buzz, which requires control flow to live inside a function (run-script invokes
// main). Returns an error for any control-flow / return / throw / bare-block
// statement at script scope.
func checkTopLevelStmt(s ast.Node) error {
	var kind string
	switch n := s.(type) {
	case *ast.IfStmt:
		kind = "if"
	case *ast.WhileStmt:
		kind = "while"
	case *ast.ForStmt:
		kind = "for"
	case *ast.ForEachStmt:
		kind = "foreach"
	case *ast.DoStmt:
		kind = "do/until"
	case *ast.TryStmt:
		kind = "try"
	case *ast.ThrowStmt:
		kind = "throw"
	case *ast.ReturnStmt:
		kind = "return"
	case *ast.BreakStmt:
		kind = "break"
	case *ast.ContinueStmt:
		kind = "continue"
	case *ast.BlockStmt:
		kind = "block"
	default:
		_ = n
		return nil
	}
	pos := ast.NodePos(s)
	return fmt.Errorf("buzz: line %d:%d: %s statement is not allowed at the top level; move it into a function (strict mode)", pos.Line, pos.Col, kind)
}

func (p *parser) parseStmt() (ast.Node, error) {
	t := p.peek()
	// `test` is a *contextual* soft keyword, not a reserved word: it introduces a
	// test block only in the unambiguous statement-leading shape `test "name" {`.
	// Everywhere else `test` stays an ordinary identifier — so a magusfile target
	// `export fun test(...)`, a variable named `test`, or a call `test(...)` all
	// still parse. This deliberately accepts a superset of upstream Buzz (which
	// hard-reserves `test`): every upstream `test "..." {}` runs identically, and
	// the embedding keeps `test` usable as an identifier. (No other juxtaposition
	// of an identifier and a string literal is valid Buzz, so the lookahead is
	// unambiguous.)
	if t.Kind == token.Ident && t.Val == "test" &&
		p.peekAt(1).Kind == token.String && p.peekAt(2).Kind == token.LBrace {
		return p.parseTestDecl()
	}
	// `extern fun name(...) > T;` declares a native function's SIGNATURE with no
	// body - how upstream types its stdlib (src/lib/*.buzz). Contextual like
	// `test`: `extern` is a reserved word, so it can never be a binding name, but
	// it stays an ordinary identifier in every position other than this one.
	if t.Kind == token.Ident && t.Val == "extern" && p.peekAt(1).Kind == token.Fun {
		return p.parseExternFunDecl()
	}
	// `out expr;` leaves the enclosing block expression. Contextual like `test`:
	// outside a `from` block `out` stays an ordinary identifier, so the check is
	// gated on one actually being open.
	if t.Kind == token.Ident && t.Val == "out" && p.fromDepth > 0 {
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		p.optSemicolon()
		return &ast.OutStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Value: val}, nil
	}
	switch t.Kind {
	case token.Namespace:
		return p.parseNamespace()
	case token.Import:
		return p.parseImport()
	case token.Export:
		// A doc comment above `export fun f()` lands on the `export` token; carry it
		// onto the declaration so an exported handler is documented like a bare one.
		exportDoc := t.Doc
		p.advance()
		node, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		switch n := node.(type) {
		case *ast.FunDecl:
			n.IsExported = true
			if n.Doc == "" {
				n.Doc = exportDoc
			}
		case *ast.DeclStmt:
			n.IsExported = true
		case *ast.ObjectDecl:
			n.IsExported = true
		case *ast.EnumDecl:
			n.IsExported = true
		}
		return node, nil
	case token.Final, token.Var:
		return p.parseDecl()
	case token.Return:
		return p.parseReturn()
	case token.If:
		return p.parseIf()
	case token.While:
		return p.parseWhile()
	case token.Do:
		return p.parseDoUntil()
	case token.For:
		return p.parseForLoop()
	case token.Foreach:
		return p.parseForeach()
	case token.Try:
		return p.parseTryCatch()
	case token.Throw:
		return p.parseThrow()
	case token.Yield, token.Resume, token.Resolve:
		// yield/resume/resolve are expression-level keywords; parse as ExprStmt.
		expr, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.eat(token.Semicolon); err != nil {
			return nil, err
		}
		return &ast.ExprStmt{Pos: ast.NodePos(expr), Expr: expr}, nil
	case token.Break:
		p.advance()
		label := p.parseLoopTargetLabel()
		p.optSemicolon()
		return &ast.BreakStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Label: label}, nil
	case token.Continue:
		p.advance()
		label := p.parseLoopTargetLabel()
		p.optSemicolon()
		return &ast.ContinueStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Label: label}, nil
	case token.Object:
		return p.parseObjectDecl()
	case token.Enum:
		return p.parseEnumDecl()
	case token.Fun:
		if p.peekAt(1).Kind == token.Ident {
			return p.parseFunDecl()
		}
		return p.parseExprOrAssign()
	case token.LBrace:
		return p.parseBlock()
	case token.Semicolon:
		p.advance()
		//nolint:nilnil // empty statement (lone ';'); both callers skip nil nodes
		return nil, nil
	default:
		return p.parseExprOrAssign()
	}
}

func (p *parser) parseImport() (*ast.ImportStmt, error) {
	t, _ := p.eat(token.Import)
	pathTok, err := p.eat(token.String)
	if err != nil {
		return nil, err
	}
	var alias string
	if p.check(token.As) {
		p.advance()
		// Accept any identifier including "_" (flat/erase import)
		if p.check(token.Ident) {
			alias = p.advance().Val
		}
	}
	p.optSemicolon()
	return &ast.ImportStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Path: pathTok.Val, Alias: alias}, nil
}

func (p *parser) parseNamespace() (*ast.NamespaceStmt, error) {
	t, _ := p.eat(token.Namespace)
	nameTok, err := p.eatBindingIdent()
	if err != nil {
		return nil, err
	}
	// Consume optional backslash-separated path segments (e.g. namespace a\b\c)
	// by treating '\' as punctuation and consuming ident pairs until none remain.
	// We just build a human-readable name string; no semantic enforcement yet.
	name := nameTok.Val
	for p.check(token.Backslash) {
		p.advance()
		if !p.check(token.Ident) {
			break
		}
		name += `\` + p.advance().Val
	}
	p.optSemicolon()
	return &ast.NamespaceStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: name}, nil
}

func (p *parser) parseDecl() (*ast.DeclStmt, error) {
	t := p.advance() // const/final/var
	isConst := t.Kind == token.Final
	nameTok, err := p.eatBindingIdent()
	if err != nil {
		return nil, err
	}
	var typeAnnot string
	if p.check(token.Colon) {
		p.advance()
		if typeAnnot, err = p.readType(); err != nil {
			return nil, err
		}
	}
	pos := ast.Pos{Line: t.Line, Col: t.Col}
	// A nullable declaration may omit its initializer: `final hello: int?;` binds
	// null. Only nullable types get this - anything else still has to be assigned.
	if strings.HasSuffix(typeAnnot, "?") && !p.check(token.Assign) {
		p.optSemicolon()
		return &ast.DeclStmt{Pos: pos, IsConst: isConst, Name: nameTok.Val, TypeAnnot: typeAnnot, Value: &ast.NullLit{Pos: pos}}, nil
	}
	if _, err := p.eat(token.Assign); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.optSemicolon()
	return &ast.DeclStmt{Pos: pos, IsConst: isConst, Name: nameTok.Val, TypeAnnot: typeAnnot, Value: val}, nil
}

// skipType skips a type expression; types are unused at runtime in Phase 1/3.
func (p *parser) skipType() error {
	// `mut` is a leading modifier on a collection/object type (mut [int], mut Foo).
	// Mutability is enforced on the value at runtime, so the annotation is consumed
	// and otherwise ignored here.
	if p.check(token.Mut) {
		p.advance()
	}
	t := p.peek()
	switch t.Kind {
	case token.Ident, token.Void:
		// `obj{ field: T }` is an anonymous (structural) object type. Types are erased
		// here, so the shape only has to be consumed.
		if t.Kind == token.Ident && t.Val == "obj" && p.peekAt(1).Kind == token.LBrace {
			p.advance()
			return p.skipBalancedBraces()
		}
		p.advance()
		// Namespace-qualified type: serialize\Boxed, foo\bar\Baz.
		for p.check(token.Backslash) {
			p.advance()
			if _, err := p.eat(token.Ident); err != nil {
				return err
			}
		}
		// A generic instantiation: `Payload::<str, int>` (or the bare `Foo<T>`
		// spelling). Type arguments are erased, so the identity is the bare name -
		// the span is recorded for readType to drop, keeping `x is Payload::<str,
		// int>` a test against `Payload`.
		if p.check(token.Colon) && p.peekAt(1).Kind == token.Colon && p.peekAt(2).Kind == token.Lt {
			from := p.pos
			if _, err := p.skipTypeParams(); err != nil {
				return err
			}
			p.typeTextSkips = append(p.typeTextSkips, [2]int{from, p.pos})
		} else if p.check(token.Lt) {
			from := p.pos
			if err := p.skipGenericArgs(); err != nil {
				return err
			}
			p.typeTextSkips = append(p.typeTextSkips, [2]int{from, p.pos})
		}
		if p.check(token.Question) {
			p.advance()
		}
	case token.LBracket:
		p.advance()
		if err := p.skipType(); err != nil {
			return err
		}
		if _, err := p.eat(token.RBracket); err != nil {
			return err
		}
		if p.check(token.Question) {
			p.advance()
		}
	case token.LBrace:
		p.advance()
		if err := p.skipType(); err != nil {
			return err
		}
		if _, err := p.eat(token.Colon); err != nil {
			return err
		}
		if err := p.skipType(); err != nil {
			return err
		}
		if _, err := p.eat(token.RBrace); err != nil {
			return err
		}
		if p.check(token.Question) {
			p.advance()
		}
	case token.Fun:
		p.advance()
		// A function TYPE may itself be generic: `fun::<C, D>() > int`. Erased like
		// every other type parameter, so it only has to parse.
		if _, err := p.skipTypeParams(); err != nil {
			return err
		}
		if _, err := p.eat(token.LParen); err != nil {
			return err
		}
		for !p.check(token.RParen) && !p.check(token.EOF) {
			// A function-type parameter may be named (`code: str`, the upstream
			// spelling) or a bare type (`str`). Consume the optional `name:`
			// prefix, then the parameter's type.
			if p.check(token.Ident) && p.peekAt(1).Kind == token.Colon {
				p.advance() // name
				p.advance() // ':'
			}
			if err := p.skipType(); err != nil {
				return err
			}
			if !p.check(token.Comma) {
				break
			}
			p.advance()
		}
		if _, err := p.eat(token.RParen); err != nil {
			return err
		}
		// The return type follows an explicit `>` arrow (`fun (...) > void`); the
		// arrowless form leaves it implicit. skipType handles void and a trailing
		// `?`. A `?` after the whole type makes the function value itself optional.
		if p.check(token.Gt) {
			p.advance()
			if err := p.skipType(); err != nil {
				return err
			}
		} else if p.isTypeStart() {
			if err := p.skipType(); err != nil {
				return err
			}
		}
		// A function TYPE carries the same error-set and yield-type suffixes a
		// declaration does: `fn: fun (v: int) > int !> str` and
		// `fn: fun () > int *> int?` both appear in parameter position upstream.
		// Neither is part of the type's identity - a thrown error set and a yield
		// type never affect assignability - so the span is recorded for readType
		// to drop rather than folded into the annotation text.
		for p.check(token.ErrArrow) || p.check(token.YieldArrow) {
			from := p.pos
			p.advance()
			if err := p.skipType(); err != nil {
				return err
			}
			p.typeTextSkips = append(p.typeTextSkips, [2]int{from, p.pos})
		}
		if p.check(token.Question) {
			p.advance()
		}
	default:
		return fmt.Errorf("buzz: line %d:%d: expected type, got token %d", t.Line, t.Col, t.Kind)
	}
	return nil
}

// readType reads a type expression and returns its compact text representation.
// This captures the same tokens as skipType but reconstructs a string for the type checker.
func (p *parser) readType() (string, error) {
	before := p.pos
	if err := p.skipType(); err != nil {
		return "", err
	}
	return p.joinTokens(before, p.pos), nil
}

// joinTokens concatenates the text of tokens[from:to].
func (p *parser) joinTokens(from, to int) string {
	var sb strings.Builder
	for i := from; i < to; i++ {
		if p.inTypeTextSkip(i) {
			continue
		}
		sb.WriteString(tokenText(p.tokens[i]))
	}
	return sb.String()
}

// inTypeTextSkip reports whether token i falls in a span skipType consumed but
// deliberately kept out of the reconstructed annotation text.
func (p *parser) inTypeTextSkip(i int) bool {
	for _, span := range p.typeTextSkips {
		if i >= span[0] && i < span[1] {
			return true
		}
	}
	return false
}

// tokenText returns the source text for a single token.
func tokenText(t token.Token) string {
	switch t.Kind {
	case token.Ident:
		return t.Val
	case token.Void:
		return "void"
	case token.Fun:
		return "fun"
	case token.LParen:
		return "("
	case token.RParen:
		return ")"
	case token.LBracket:
		return "["
	case token.RBracket:
		return "]"
	case token.LBrace:
		return "{"
	case token.RBrace:
		return "}"
	case token.Colon:
		return ":"
	case token.Comma:
		return ","
	case token.Lt:
		return "<"
	case token.Gt:
		return ">"
	case token.Question:
		return "?"
	case token.Backslash:
		return "\\"
	default:
		return ""
	}
}

// skipGenericArgs skips a balanced <...> generic argument list.
// skipTypeParams consumes an upstream `::<T, U>` clause and reports whether one was
// present. Type parameters are ERASED: nothing in the language reflects on them at
// runtime, so a generic function compiles to exactly the code its non-generic twin
// would, and a call site's type arguments only have to parse.
//
// `::` is two Colon tokens - there is no distinct token for it - and the argument
// list reuses skipGenericArgs, so nested `::<A::<B>>` fails for the same reason it
// does upstream: the trailing `>>` lexes as a shift.
func (p *parser) skipTypeParams() (bool, error) {
	if !(p.check(token.Colon) && p.peekAt(1).Kind == token.Colon && p.peekAt(2).Kind == token.Lt) {
		return false, nil
	}
	p.advance()
	p.advance()
	if err := p.skipGenericArgs(); err != nil {
		return false, err
	}
	return true, nil
}

// skipBalancedBraces consumes a { ... } group, nesting included.
func (p *parser) skipBalancedBraces() error {
	if _, err := p.eat(token.LBrace); err != nil {
		return err
	}
	for depth := 1; depth > 0; {
		switch p.peek().Kind {
		case token.LBrace:
			depth++
		case token.RBrace:
			depth--
		case token.EOF:
			t := p.peek()
			return fmt.Errorf("buzz: line %d:%d: unterminated anonymous object type", t.Line, t.Col)
		}
		p.advance()
	}
	return nil
}

func (p *parser) skipGenericArgs() error {
	if _, err := p.eat(token.Lt); err != nil {
		return err
	}
	depth := 1
	for depth > 0 {
		switch p.peek().Kind {
		case token.Lt:
			depth++
			p.advance()
		case token.Gt:
			depth--
			p.advance()
		case token.EOF:
			t := p.peek()
			return fmt.Errorf("buzz: line %d:%d: unterminated generic argument list", t.Line, t.Col)
		default:
			p.advance()
		}
	}
	return nil
}

// readGenericArg consumes a balanced <...> generic argument list and returns
// the inner type text (e.g. "str" from "<str>"). Mirrors skipGenericArgs but
// preserves the source so the checker can resolve the element type.
func (p *parser) readGenericArg() (string, error) {
	if _, err := p.eat(token.Lt); err != nil {
		return "", err
	}
	before := p.pos
	depth := 1
	for depth > 0 {
		switch p.peek().Kind {
		case token.Lt:
			depth++
			p.advance()
		case token.Gt:
			depth--
			if depth == 0 {
				inner := p.joinTokens(before, p.pos)
				p.advance()
				return inner, nil
			}
			p.advance()
		case token.EOF:
			t := p.peek()
			return "", fmt.Errorf("buzz: line %d:%d: unterminated generic argument list", t.Line, t.Col)
		default:
			p.advance()
		}
	}
	return "", nil
}

// isTypeStart reports whether the current token can begin a type.
func (p *parser) isTypeStart() bool {
	switch p.peek().Kind {
	case token.Ident, token.Void, token.LBracket, token.Fun:
		return true
	default:
		return false
	}
}

func (p *parser) parseReturn() (*ast.ReturnStmt, error) {
	t, _ := p.eat(token.Return)
	if p.check(token.Semicolon) {
		p.advance()
		return &ast.ReturnStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}}, nil
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.optSemicolon()
	return &ast.ReturnStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Value: val}, nil
}

func (p *parser) parseIf() (*ast.IfStmt, error) {
	t, _ := p.eat(token.If)
	// Parse `( cond )`, allowing the optional-call narrowing form
	// `( opt -> name )` where name binds the non-null value inside the block.
	if _, err := p.eat(token.LParen); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	bindName := ""
	if p.check(token.Arrow) {
		p.advance()
		id, err := p.eatBindingIdent()
		if err != nil {
			return nil, err
		}
		bindName = id.Val
	}
	if _, err := p.eat(token.RParen); err != nil {
		return nil, err
	}
	then, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	out := &ast.IfStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Cond: cond, Then: then, BindName: bindName}
	if p.check(token.Else) {
		p.advance()
		if p.check(token.If) {
			elseIf, err := p.parseIf()
			if err != nil {
				return nil, err
			}
			out.Else = elseIf
		} else {
			elseBlock, err := p.parseBlock()
			if err != nil {
				return nil, err
			}
			out.Else = elseBlock
		}
	}
	return out, nil
}

func (p *parser) parseTryCatch() (*ast.TryStmt, error) {
	t, _ := p.eat(token.Try)
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	out := &ast.TryStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Body: body}
	// Upstream allows several catch clauses, each narrowing on the error's type,
	// with an untyped one acting as the catch-all. At least one is required.
	for p.check(token.Catch) {
		ct := p.advance()
		clause := ast.CatchClause{Pos: ast.Pos{Line: ct.Line, Col: ct.Col}}
		// Catch-all form `catch { ... }` (upstream Buzz): no binding. The error is
		// still pushed by the VM, so bind it to a throwaway "_" slot the body ignores.
		clause.ErrName = "_"
		if p.check(token.LParen) {
			p.advance()
			nameTok, err := p.eatBindingIdent()
			if err != nil {
				return nil, err
			}
			clause.ErrName = nameTok.Val
			if p.check(token.Colon) {
				p.advance()
				if clause.TypeName, err = p.readType(); err != nil {
					return nil, err
				}
			}
			if _, err := p.eat(token.RParen); err != nil {
				return nil, err
			}
		}
		catch, err := p.parseBlock()
		if err != nil {
			return nil, err
		}
		clause.Body = catch
		out.Catches = append(out.Catches, clause)
	}
	if len(out.Catches) == 0 {
		return nil, fmt.Errorf("buzz: line %d:%d: try requires at least one catch clause", t.Line, t.Col)
	}
	return out, nil
}

func (p *parser) parseThrow() (*ast.ThrowStmt, error) {
	t, _ := p.eat(token.Throw)
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.optSemicolon()
	return &ast.ThrowStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Value: val}, nil
}

func (p *parser) parseDoUntil() (*ast.DoStmt, error) {
	t, _ := p.eat(token.Do)
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	if _, err := p.eat(token.Until); err != nil {
		return nil, err
	}
	hasParen := p.check(token.LParen)
	if hasParen {
		p.advance()
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if hasParen {
		if _, err := p.eat(token.RParen); err != nil {
			return nil, err
		}
	}
	p.optSemicolon()
	return &ast.DoStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Body: body, Cond: cond}, nil
}

func (p *parser) parseWhile() (*ast.WhileStmt, error) {
	t, _ := p.eat(token.While)
	cond, err := p.parseParenCond()
	if err != nil {
		return nil, err
	}
	label, err := p.parseLoopLabel()
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.WhileStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Cond: cond, Body: body, Label: label}, nil
}

func (p *parser) parseForLoop() (*ast.ForStmt, error) {
	t, _ := p.eat(token.For)
	if _, err := p.eat(token.LParen); err != nil {
		return nil, err
	}
	out := &ast.ForStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}}
	for !p.check(token.Semicolon) {
		init, err := p.parseForInit()
		if err != nil {
			return nil, err
		}
		out.Init = append(out.Init, init)
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	if _, err := p.eat(token.Semicolon); err != nil {
		return nil, err
	}
	if !p.check(token.Semicolon) {
		cond, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		out.Cond = cond
	}
	if _, err := p.eat(token.Semicolon); err != nil {
		return nil, err
	}
	for !p.check(token.RParen) {
		post, err := p.parseAssignTail()
		if err != nil {
			return nil, err
		}
		out.Post = append(out.Post, post)
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	if _, err := p.eat(token.RParen); err != nil {
		return nil, err
	}
	label, err := p.parseLoopLabel()
	if err != nil {
		return nil, err
	}
	out.Label = label
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

// parseForInit parses the for-loop init clause: a declaration or assignment/expr.
func (p *parser) parseForInit() (ast.Node, error) {
	if p.check(token.Final) || p.check(token.Var) {
		return p.parseDeclNoSemi()
	}
	// `for (i: int = 0; ...)` - upstream declares the loop variable with a type and
	// no final/var keyword. The `ident :` shape is unambiguous here: an assignment
	// tail can never carry a type annotation, so nothing else in for-init position
	// starts this way. It binds mutable, since a for-loop's own post clause assigns
	// to it.
	if p.check(token.Ident) && p.peekAt(1).Kind == token.Colon {
		return p.parseTypedForDecl()
	}
	return p.parseAssignTail()
}

// parseTypedForDecl parses a keyword-less typed declaration (`i: int = 0`) for a
// for-loop initializer. Separate from parseDecl only because that one opens by
// consuming the final/var token this form does not have.
func (p *parser) parseTypedForDecl() (*ast.DeclStmt, error) {
	nameTok, err := p.eatBindingIdent()
	if err != nil {
		return nil, err
	}
	if _, err := p.eat(token.Colon); err != nil {
		return nil, err
	}
	typeAnnot, err := p.readType()
	if err != nil {
		return nil, err
	}
	if _, err := p.eat(token.Assign); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.DeclStmt{
		Pos:       ast.Pos{Line: nameTok.Line, Col: nameTok.Col},
		Name:      nameTok.Val,
		TypeAnnot: typeAnnot,
		Value:     val,
	}, nil
}

// parseDeclNoSemi parses a const/var declaration without consuming a semicolon.
func (p *parser) parseDeclNoSemi() (*ast.DeclStmt, error) {
	t := p.advance()
	isConst := t.Kind == token.Final
	nameTok, err := p.eatBindingIdent()
	if err != nil {
		return nil, err
	}
	if p.check(token.Colon) {
		p.advance()
		if err := p.skipType(); err != nil {
			return nil, err
		}
	}
	if _, err := p.eat(token.Assign); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.DeclStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, IsConst: isConst, Name: nameTok.Val, Value: val}, nil
}

// parseAssignTail parses an expression and, if followed by '=', an assignment —
// without consuming a trailing semicolon.
func (p *parser) parseAssignTail() (ast.Node, error) {
	t := p.peek()
	expr, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if p.check(token.Assign) {
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if !isAssignable(expr) {
			return nil, fmt.Errorf("buzz: line %d:%d: invalid assignment target", t.Line, t.Col)
		}
		return &ast.AssignStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Target: expr, Value: val}, nil
	}
	if op, ok := compoundAssignOp(p.peek().Kind); ok {
		p.advance()
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if !isAssignable(expr) {
			return nil, fmt.Errorf("buzz: line %d:%d: invalid assignment target", t.Line, t.Col)
		}
		// Desugar `x op= v` into `x = x op v`, sharing the target node so the
		// checker and compiler see the ordinary assignment they already handle.
		// The target is therefore evaluated twice, so a receiver with side effects
		// (`f().n += 1`) would call f() twice where upstream calls it once; every
		// assignable target is an identifier, field, or subscript, and only a call
		// in that position makes the difference observable.
		pos := ast.Pos{Line: t.Line, Col: t.Col}
		return &ast.AssignStmt{
			Pos:    pos,
			Target: expr,
			Value:  &ast.BinaryExpr{Pos: pos, Op: op, Left: expr, Right: val},
		}, nil
	}
	return &ast.ExprStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Expr: expr}, nil
}

// compoundAssignOp maps a compound-assignment token to the binary operator it
// applies, or reports false when k is not one.
func compoundAssignOp(k token.Kind) (string, bool) {
	switch k {
	case token.PlusAssign:
		return "+", true
	case token.MinusAssign:
		return "-", true
	case token.StarAssign:
		return "*", true
	case token.SlashAssign:
		return "/", true
	case token.PercentAssign:
		return "%", true
	case token.AmpAssign:
		return "&", true
	case token.PipeAssign:
		return "|", true
	case token.CaretAssign:
		return "^", true
	case token.ShlAssign:
		return "<<", true
	case token.ShrAssign:
		return ">>", true
	}
	return "", false
}

func (p *parser) parseForeach() (*ast.ForEachStmt, error) {
	t, _ := p.eat(token.Foreach)
	if _, err := p.eat(token.LParen); err != nil {
		return nil, err
	}
	first, err := p.eatBindingIdent()
	if err != nil {
		return nil, err
	}
	if p.check(token.Colon) {
		p.advance()
		if err := p.skipType(); err != nil {
			return nil, err
		}
	}
	out := &ast.ForEachStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, ValName: first.Val}
	if p.check(token.Comma) {
		p.advance()
		second, err := p.eatBindingIdent()
		if err != nil {
			return nil, err
		}
		if p.check(token.Colon) {
			p.advance()
			if err := p.skipType(); err != nil {
				return nil, err
			}
		}
		out.KeyName = first.Val
		out.ValName = second.Val
	}
	if _, err := p.eat(token.In); err != nil {
		return nil, err
	}
	iter, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	out.Iter = iter
	if _, err := p.eat(token.RParen); err != nil {
		return nil, err
	}
	label, err := p.parseLoopLabel()
	if err != nil {
		return nil, err
	}
	out.Label = label
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
}

// parseIfExpr parses the inline-if `if (cond) a else b`. Unlike the statement
// form the else branch is mandatory: an expression has to produce a value on
// every path. `else if` chains by recursing into the same production.
func (p *parser) parseIfExpr() (*ast.IfExpr, error) {
	t, _ := p.eat(token.If)
	cond, err := p.parseParenCond()
	if err != nil {
		return nil, err
	}
	then, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.eat(token.Else); err != nil {
		return nil, err
	}
	els, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	return &ast.IfExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Cond: cond, Then: then, Else: els}, nil
}

// parseBlockExpr parses `from { stmts }`, a block used as an expression whose
// value comes from an `out` statement inside it.
func (p *parser) parseBlockExpr() (*ast.BlockExpr, error) {
	t := p.advance() // `from`
	p.fromDepth++
	body, err := p.parseBlock()
	p.fromDepth--
	if err != nil {
		return nil, err
	}
	return &ast.BlockExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Body: body}, nil
}

// parseLoopLabel parses the optional `:name` a loop header may carry between its
// clause and its body (`while (i < 100) :here { ... }`), which `break name` and
// `continue name` then target.
func (p *parser) parseLoopLabel() (string, error) {
	if !p.check(token.Colon) {
		return "", nil
	}
	p.advance()
	nameTok, err := p.eatIdent()
	if err != nil {
		return "", err
	}
	return nameTok.Val, nil
}

// parseLoopTargetLabel parses the optional loop name after `break`/`continue`.
// Upstream writes the target bare (`break here;`), without the declaration's
// colon.
func (p *parser) parseLoopTargetLabel() string {
	if !p.check(token.Ident) {
		return ""
	}
	return p.advance().Val
}

// parseParenCond parses a parenthesized condition: ( expr ).
func (p *parser) parseParenCond() (ast.Node, error) {
	if _, err := p.eat(token.LParen); err != nil {
		return nil, err
	}
	cond, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.eat(token.RParen); err != nil {
		return nil, err
	}
	return cond, nil
}

func (p *parser) parseFunDecl() (*ast.FunDecl, error) {
	t, _ := p.eat(token.Fun)
	nameTok, err := p.eatBindingIdent()
	if err != nil {
		return nil, err
	}
	if _, err := p.skipTypeParams(); err != nil {
		return nil, err
	}
	fr, err := p.parseFunRest(false)
	if err != nil {
		return nil, err
	}
	return &ast.FunDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: nameTok.Val, Params: fr.params, ParamAnnots: fr.paramAnnots, ParamDefaults: fr.paramDefaults, RetAnnot: fr.retAnnot, YieldAnnot: fr.yieldAnnot, Body: fr.body, Doc: t.Doc}, nil
}

// parseExternFunDecl parses `extern fun name(params) > T;` - the signature of a
// function the HOST implements, with a semicolon where a body would be. It is
// how upstream Buzz gives its native stdlib types (`export extern fun
// dump(value: any) > void;`), and the same declaration types magus's host
// modules: the checker gets a real signature instead of Unknown, and the runtime
// resolves the name against the binding the host already installed.
func (p *parser) parseExternFunDecl() (*ast.FunDecl, error) {
	t := p.advance() // `extern`
	if _, err := p.eat(token.Fun); err != nil {
		return nil, err
	}
	nameTok, err := p.eatBindingIdent()
	if err != nil {
		return nil, err
	}
	if _, err := p.skipTypeParams(); err != nil {
		return nil, err
	}
	fr, err := p.parseFunRest(true)
	if err != nil {
		return nil, err
	}
	return &ast.FunDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, IsExtern: true, Name: nameTok.Val, Params: fr.params, ParamAnnots: fr.paramAnnots, ParamDefaults: fr.paramDefaults, RetAnnot: fr.retAnnot, YieldAnnot: fr.yieldAnnot, Doc: t.Doc}, nil
}

// parseTestDecl parses `test "name" { body }`. The name is a string literal, as
// in upstream Buzz (tests/behavior/testing.buzz). The leading `test` is consumed
// as the soft keyword (it lexes as an identifier; see parseStmt for why).
func (p *parser) parseTestDecl() (*ast.TestDecl, error) {
	t := p.advance() // `test`
	nameTok, err := p.eat(token.String)
	if err != nil {
		return nil, err
	}
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.TestDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: nameTok.Val, Body: body}, nil
}

func (p *parser) parseObjectDecl() (*ast.ObjectDecl, error) {
	t, _ := p.eat(token.Object)
	nameTok, err := p.eatBindingIdent()
	if err != nil {
		return nil, err
	}
	// `object Payload::<K, V>` declares type parameters. They are erased like a
	// generic function's, so the object's identity stays its bare name and the
	// parameters only have to parse - a field typed `K` resolves to Unknown,
	// which is compatible with whatever the instance actually holds.
	if _, err := p.skipTypeParams(); err != nil {
		return nil, err
	}
	if _, err := p.eat(token.LBrace); err != nil {
		return nil, err
	}
	decl := &ast.ObjectDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: nameTok.Val}
	for !p.check(token.RBrace) && !p.check(token.EOF) {
		// `static` marks a member that lives on the type rather than an instance:
		// `static fun` is a method called as Foo.make() with no receiver, and a
		// static field is one mutable value shared by every instance. It is not a
		// keyword here (it lexes as an identifier), so match it by value; that is
		// unambiguous because `static` is reserved and so can never be a member name.
		isStatic := false
		if p.check(token.Ident) && p.peek().Val == "static" {
			p.advance()
			isStatic = true
		}
		// `mut fun` declares a method that mutates the receiver. Mutation is enforced
		// on the receiver value at runtime (an immutable instance rejects field
		// writes), so the modifier is consumed here and the method parsed normally.
		if p.check(token.Mut) && p.peekAt(1).Kind == token.Fun {
			p.advance()
		}
		if p.check(token.Fun) {
			method, err := p.parseFunDecl()
			if err != nil {
				return nil, err
			}
			method.IsStatic = isStatic
			decl.Methods = append(decl.Methods, method)
			p.optSemicolon()
			continue
		}
		nameTok, err := p.eatBindingIdent()
		if err != nil {
			return nil, err
		}
		field := ast.ObjField{Name: nameTok.Val}
		if p.check(token.Colon) {
			p.advance()
			ta, e := p.readType()
			if e != nil {
				return nil, e
			}
			field.TypeAnnot = ta
		}
		if p.check(token.Assign) {
			p.advance()
			def, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			field.Default = def
		}
		if isStatic {
			decl.StaticFields = append(decl.StaticFields, field)
		} else {
			decl.Fields = append(decl.Fields, field)
		}
		if p.check(token.Comma) || p.check(token.Semicolon) {
			p.advance()
		}
	}
	if _, err := p.eat(token.RBrace); err != nil {
		return nil, err
	}
	return decl, nil
}

func (p *parser) parseEnumDecl() (*ast.EnumDecl, error) {
	t, _ := p.eat(token.Enum)
	// Backing type: `enum<str> Name`. It precedes the name, so it is read before
	// eatBindingIdent rather than alongside the other type annotations.
	var backing string
	if p.check(token.Lt) {
		p.advance()
		bt, err := p.readType()
		if err != nil {
			return nil, err
		}
		if _, err := p.eat(token.Gt); err != nil {
			return nil, err
		}
		backing = bt
	}
	nameTok, err := p.eatBindingIdent()
	if err != nil {
		return nil, err
	}
	if p.check(token.LParen) {
		p.advance()
		if err := p.skipType(); err != nil {
			return nil, err
		}
		if _, err := p.eat(token.RParen); err != nil {
			return nil, err
		}
	}
	if _, err := p.eat(token.LBrace); err != nil {
		return nil, err
	}
	decl := &ast.EnumDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: nameTok.Val, Backing: backing}
	for !p.check(token.RBrace) && !p.check(token.EOF) {
		caseTok, err := p.eatBindingIdent()
		if err != nil {
			return nil, err
		}
		var val ast.Node
		if p.check(token.Assign) {
			p.advance()
			if val, err = p.parseExpr(); err != nil {
				return nil, err
			}
		}
		decl.Cases = append(decl.Cases, caseTok.Val)
		decl.Values = append(decl.Values, val)
		if p.check(token.Comma) || p.check(token.Semicolon) {
			p.advance()
		}
	}
	if _, err := p.eat(token.RBrace); err != nil {
		return nil, err
	}
	return decl, nil
}

func (p *parser) parseExprOrAssign() (ast.Node, error) {
	node, err := p.parseAssignTail()
	if err != nil {
		return nil, err
	}
	p.optSemicolon()
	return node, nil
}

func (p *parser) parseBlock() (*ast.BlockStmt, error) {
	t, err := p.eat(token.LBrace)
	if err != nil {
		return nil, err
	}
	block := &ast.BlockStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}}
	for !p.check(token.RBrace) && !p.check(token.EOF) {
		s, err := p.parseStmt()
		if err != nil {
			return nil, err
		}
		if s != nil {
			block.Stmts = append(block.Stmts, s)
		}
	}
	if _, err := p.eat(token.RBrace); err != nil {
		return nil, err
	}
	return block, nil
}

// ---- expression precedence climbing ----

func (p *parser) parseExpr() (ast.Node, error) { return p.parseOr() }

// parseRange sits between comparison and additive, so `..` binds TIGHTER than
// `==`: upstream writes `range == 0..10`, which at a looser precedence would
// parse as `(range == 0)..10` and range a bool.
func (p *parser) parseRange() (ast.Node, error) {
	left, err := p.parseAdditive()
	if err != nil {
		return nil, err
	}
	if p.check(token.DotDot) {
		t := p.advance()
		right, err := p.parseAdditive()
		if err != nil {
			return nil, err
		}
		return &ast.RangeExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Lo: left, Hi: right}, nil
	}
	return left, nil
}

// parseCoalesce parses `??` at upstream Buzz's Precedence.NullCoalescing, which
// sits BETWEEN Term (`+`/`-`) and Bitwise - so `sum + resume f ?? 0` is
// `sum + (resume f ?? 0)`, not `(sum + resume f) ?? 0`.
//
// It used to sit above `or`, looser than every binary operator, which made the
// upstream idiom "coalesce a nullable right where it is used" a runtime error:
// the `+` saw the null before the `??` could replace it
// (tests/behavior/generics.buzz, "Generic with fibers"). `typeof` shares this
// level upstream and belongs here too when it lands.
func (p *parser) parseCoalesce() (ast.Node, error) {
	left, err := p.parseBitwise()
	if err != nil {
		return nil, err
	}
	for p.check(token.Coalesce) {
		t := p.advance()
		right, err := p.parseBitwise()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: "??", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseOr() (ast.Node, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.check(token.Or) {
		t := p.advance()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: "or", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseAnd() (ast.Node, error) {
	left, err := p.parseEquality()
	if err != nil {
		return nil, err
	}
	for p.check(token.And) {
		t := p.advance()
		right, err := p.parseEquality()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: "and", Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseEquality() (ast.Node, error) {
	left, err := p.parseComparison()
	if err != nil {
		return nil, err
	}
	for p.check(token.Eq) || p.check(token.Neq) {
		t := p.advance()
		op := "=="
		if t.Kind == token.Neq {
			op = "!="
		}
		right, err := p.parseComparison()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *parser) parseComparison() (ast.Node, error) {
	left, err := p.parseRange()
	if err != nil {
		return nil, err
	}
	for {
		switch p.peek().Kind {
		case token.Lt, token.Gt, token.Le, token.Ge:
			var op string
			switch p.peek().Kind {
			case token.Lt:
				op = "<"
			case token.Gt:
				op = ">"
			case token.Le:
				op = "<="
			case token.Ge:
				op = ">="
			}
			t := p.advance()
			right, err := p.parseRange()
			if err != nil {
				return nil, err
			}
			left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: op, Left: left, Right: right}
		case token.Is:
			t := p.advance()
			// readType, not eatIdent: upstream tests `x is [int]`, `x is {str: int}`,
			// `x is int?`, `x is a\Hello`, and `x is fun (id: int) > int`. A bare
			// identifier covers none of those. `as` one case below already reads the
			// full type; `is` had simply never been widened to match it.
			typStr, err := p.readType()
			if err != nil {
				return nil, err
			}
			left = &ast.IsExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Expr: left, TypeName: typStr}
		case token.As:
			t := p.advance()
			optional := false
			if p.check(token.Question) { // `as?` — null on mismatch (upstream Buzz)
				p.advance()
				optional = true
			} else if p.check(token.Bang) {
				// `as!` — force the cast, raise on mismatch. That is already what a
				// bare `as` does here, so this only accepts the spelling: upstream
				// distinguishes them because its bare `as` is statically checked,
				// while gopherbuzz resolves the cast at runtime either way.
				p.advance()
			}
			typStr, err := p.readType()
			if err != nil {
				return nil, err
			}
			left = &ast.AsExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Expr: left, TypeName: typStr, Optional: optional}
		default:
			return left, nil
		}
	}
}

func (p *parser) parseAdditive() (ast.Node, error) {
	left, err := p.parseCoalesce()
	if err != nil {
		return nil, err
	}
	for p.check(token.Plus) || p.check(token.Minus) {
		t := p.advance()
		op := "+"
		if t.Kind == token.Minus {
			op = "-"
		}
		right, err := p.parseCoalesce()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: op, Left: left, Right: right}
	}
	return left, nil
}

// parseBitwise parses `&`, `|`, and `^` at one shared level, which is upstream
// Buzz's Precedence.Bitwise: tighter than `+`/`-`, looser than the shifts. An
// `&` only reaches here in infix position; the prefix `&call()` fiber form is
// parsed by parseUnary, so the two uses never compete.
func (p *parser) parseBitwise() (ast.Node, error) {
	left, err := p.parseShift()
	if err != nil {
		return nil, err
	}
	for {
		var op string
		switch p.peek().Kind {
		case token.Amp:
			op = "&"
		case token.Pipe:
			op = "|"
		case token.Caret:
			op = "^"
		default:
			return left, nil
		}
		t := p.advance()
		right, err := p.parseShift()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: op, Left: left, Right: right}
	}
}

func (p *parser) parseShift() (ast.Node, error) {
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for {
		var op string
		switch p.peek().Kind {
		case token.Shl:
			op = "<<"
		case token.Shr:
			op = ">>"
		default:
			return left, nil
		}
		t := p.advance()
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: op, Left: left, Right: right}
	}
}

func (p *parser) parseMultiplicative() (ast.Node, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		var op string
		switch p.peek().Kind {
		case token.Star:
			op = "*"
		case token.Slash:
			op = "/"
		case token.Percent:
			op = "%"
		default:
			return left, nil
		}
		t := p.advance()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: op, Left: left, Right: right}
	}
}

func (p *parser) parseUnary() (ast.Node, error) {
	// `<T>` in PREFIX position is a type used as a value. Upstream gives Less both
	// a prefix handler (typeExpression) and an infix one (comparison); reaching
	// this function at all means nothing preceded the `<`, so it cannot be the
	// comparison operator and the two uses never compete.
	if p.check(token.Lt) {
		t := p.advance()
		annot, err := p.readType()
		if err != nil {
			return nil, err
		}
		if _, err := p.eat(token.Gt); err != nil {
			return nil, fmt.Errorf("buzz: line %d:%d: expected '>' after the type in a type expression", t.Line, t.Col)
		}
		return &ast.TypeExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Annot: annot}, nil
	}
	// `typeof x`. A reserved word rather than a token kind, so match it the same
	// contextual way `extern fun` is matched.
	if p.peek().Kind == token.Ident && p.peek().Val == "typeof" && !p.peek().Raw {
		t := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.TypeOfExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Operand: operand}, nil
	}
	// `mut` marks a list, map, or object literal as mutable. Collections are
	// immutable by default in Buzz; only a mut value may be mutated in place.
	if p.check(token.Mut) {
		t := p.advance()
		expr, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		switch e := expr.(type) {
		case *ast.ListExpr:
			e.Mut = true
		case *ast.MapExpr:
			e.Mut = true
		case *ast.ObjectLit:
			e.Mut = true
		default:
			return nil, fmt.Errorf("buzz: line %d:%d: 'mut' must precede a list, map, or object literal", t.Line, t.Col)
		}
		return expr, nil
	}
	if p.check(token.Bang) || p.check(token.Minus) || p.check(token.Tilde) {
		t := p.advance()
		op := "!"
		switch t.Kind {
		case token.Minus:
			op = "-"
		case token.Tilde:
			op = "~"
		}
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.UnaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: op, Operand: operand}, nil
	}
	// &call(args) — fiber creation
	if p.check(token.Amp) {
		t := p.advance()
		expr, err := p.parsePostfix()
		if err != nil {
			return nil, err
		}
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return nil, fmt.Errorf("buzz: line %d:%d: '&' must be applied to a function call", t.Line, t.Col)
		}
		return &ast.FiberExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Call: call}, nil
	}
	// resume fiber
	if p.check(token.Resume) {
		t := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.ResumeExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Fiber: operand}, nil
	}
	// resolve fiber
	if p.check(token.Resolve) {
		t := p.advance()
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &ast.ResolveExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Fiber: operand}, nil
	}
	// yield expr — suspends the fiber (or is dismissed outside one).
	//
	// KNOWN CONFORMANCE GAP: upstream Buzz parses `yield` as a .Primary-precedence
	// prefix, so `yield a + b` means `(yield a) + b`; gopherbuzz consumes the full
	// expression (`yield (a + b)`). The clean fix (parseUnary here) is blocked on a
	// separate VM bug: resuming a fiber whose `yield` sits mid-expression (pending
	// stack ops after it) yields null for the resumed sub-expression, so
	// `(yield a) + b` would error at runtime — which would make gopherbuzz reject a
	// program upstream accepts, violating the superset invariant. Fix the resume
	// continuation first, then narrow this to parseUnary. Until then, write
	// `yield (a + b)` for an unambiguous, cross-runtime-identical result.
	if p.check(token.Yield) {
		t := p.advance()
		operand, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		return &ast.YieldExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Value: operand}, nil
	}
	return p.parsePostfix()
}

func (p *parser) parsePostfix() (ast.Node, error) {
	node, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	// pendingTypeArg carries a `::<T>` generic argument from where it is parsed
	// to the call `(` that immediately follows, so it lands on that CallExpr.
	pendingTypeArg := ""
	// optionalRecv is set by a `?` that introduces the next hop (`a?.b`, `a?[i]`)
	// and consumed by that hop, which then yields null instead of erroring when
	// its receiver is null.
	optionalRecv := false
	for {
		switch p.peek().Kind {
		case token.Question:
			// Only `?.` and `?[` are postfix optional-chaining markers; a bare `?`
			// belongs to a type annotation or a ternary the caller handles.
			if next := p.peekAt(1).Kind; next != token.Dot && next != token.LBracket {
				return node, nil
			}
			p.advance()
			optionalRecv = true
		case token.Dot:
			// `callMe .{ ... }`: the parentheses around a lone anonymous-object
			// argument may be omitted. A `.` followed by `{` is never member
			// access, so the shape is unambiguous.
			if p.peekAt(1).Kind == token.LBrace {
				t := p.advance()
				arg, err := p.parseAnonObjectLit()
				if err != nil {
					return nil, err
				}
				node = &ast.CallExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Callee: node, Args: []ast.Node{arg}}
				continue
			}
			t := p.advance()
			nameTok, err := p.eatIdent()
			if err != nil {
				return nil, err
			}
			node = &ast.MemberExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Object: node, Name: nameTok.Val, OptionalRecv: optionalRecv}
			optionalRecv = false
		case token.Backslash:
			// Namespace access: std\print. Resolves a member of an imported module,
			// which is the same machinery as `.` member access on the module value.
			t := p.advance()
			nameTok, err := p.eatIdent()
			if err != nil {
				return nil, err
			}
			node = &ast.MemberExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Object: node, Name: nameTok.Val}
		case token.Bang:
			// Postfix force-unwrap: operand!. A lone '!' here (not '!=') unwraps an
			// optional, erroring at runtime if the value is null.
			t := p.advance()
			node = &ast.ForceExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Operand: node}
		case token.Colon:
			// Generic call type arguments: foo::<T>(args) / buf.readZAt::<f64>(...).
			// Upstream Buzz attaches explicit type arguments to a generic
			// function/method call. The VM tracks types dynamically and ignores
			// them, but the static checker needs the hint: capture it and attach it
			// to the following '(' call so the call's result type is known. A lone
			// ':' here is not otherwise meaningful in postfix position.
			if p.peekAt(1).Kind != token.Colon || p.peekAt(2).Kind != token.Lt {
				return node, nil
			}
			p.advance() // first ':'
			p.advance() // second ':'
			pendingTypeArg, err = p.readGenericArg()
			if err != nil {
				return nil, err
			}
		case token.LParen:
			t := p.advance()
			args, names, err := p.parseArgList()
			if err != nil {
				return nil, err
			}
			if _, err := p.eat(token.RParen); err != nil {
				return nil, err
			}
			node = &ast.CallExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Callee: node, Args: args, ArgNames: names, TypeArg: pendingTypeArg}
			pendingTypeArg = ""
			// Inline catch (upstream Buzz): `call(args) catch default`. If the call
			// throws, the expression yields `default` instead. The default is a full
			// expression and the error is not bound, matching upstream's call suffix.
			if p.check(token.Catch) {
				ct := p.advance()
				// `call() catch void` swallows the error and yields nothing. `void`
				// is a type keyword, not an expression, so it is matched here rather
				// than left to parseExpr; null is what "nothing" is at runtime.
				var def ast.Node
				if p.check(token.Void) {
					vt := p.advance()
					def = &ast.NullLit{Pos: ast.Pos{Line: vt.Line, Col: vt.Col}}
				} else {
					d, err := p.parseExpr()
					if err != nil {
						return nil, err
					}
					def = d
				}
				return &ast.CatchExpr{Pos: ast.Pos{Line: ct.Line, Col: ct.Col}, Expr: node, Default: def}, nil
			}
		case token.LBracket:
			t := p.advance()
			// Checked subscript: object[?index] yields null on an out-of-bounds index.
			optional := false
			if p.check(token.Question) {
				p.advance()
				optional = true
			}
			idx, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.eat(token.RBracket); err != nil {
				return nil, err
			}
			node = &ast.IndexExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Object: node, Index: idx, Optional: optional, OptionalRecv: optionalRecv}
			optionalRecv = false
		case token.LBrace:
			// `Name{...}` and the upstream-qualified `ns\Name{...}` are object
			// literals. A namespaced type parses as a MemberExpr (`config\Bind`);
			// resolve it by the last segment, which gopherbuzz's import splat binds
			// to the same object def upstream reaches as `ns\Name`.
			var typeName string
			switch n := node.(type) {
			case *ast.IdentExpr:
				typeName = n.Name
			case *ast.MemberExpr:
				typeName = n.Name
			default:
				return node, nil
			}
			lit, err := p.parseObjectLitNamed(typeName)
			if err != nil {
				return nil, err
			}
			node = lit
		default:
			return node, nil
		}
	}
}

// parseArgList parses call arguments, positional or labeled (upstream Buzz's
// `name: expr` named arguments — an identifier immediately followed by a
// colon). The names slice is nil when every argument is positional.
func (p *parser) parseArgList() ([]ast.Node, []string, error) {
	var args []ast.Node
	var names []string
	sawName := false
	for !p.check(token.RParen) && !p.check(token.EOF) {
		name := ""
		// `ident :` is a label, but `ident ::` opens a generic call argument
		// (count::<int>(...)), so the second colon has to be excluded or every
		// generic call nested inside another call's arguments is misread as a label.
		if p.check(token.Ident) && p.peekAt(1).Kind == token.Colon && p.peekAt(2).Kind != token.Colon {
			name = p.advance().Val
			p.advance() // ':'
			sawName = true
		}
		arg, err := p.parseExpr()
		if err != nil {
			return nil, nil, err
		}
		// Strict parity: upstream Buzz requires every argument after the first to be
		// labeled. A bare identifier counts as an implicit same-name label
		// (`f(a, b)` == `f(a, b: b)`); any other unlabeled expression (literal,
		// call, …) past the first arg is rejected. Resolving the implicit label
		// against the callee's params is left to the checker (and is impossible for
		// host bindings), so this enforces only the syntactic rule upstream's parser
		// applies.
		if p.strict && len(args) > 0 && name == "" {
			if _, isIdent := arg.(*ast.IdentExpr); !isIdent {
				pos := ast.NodePos(arg)
				return nil, nil, fmt.Errorf("buzz: line %d:%d: argument %d must be labeled (name: value) (strict mode)", pos.Line, pos.Col, len(args)+1)
			}
		}
		args = append(args, arg)
		names = append(names, name)
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	if !sawName {
		names = nil
	}
	return args, names, nil
}

func (p *parser) parsePrimary() (ast.Node, error) {
	t := p.peek()
	// `from` is a contextual keyword like `test`: it opens a block expression
	// only in the unambiguous `from {` shape, so a variable or field named
	// `from` still parses everywhere else.
	if t.Kind == token.Ident && t.Val == "from" && p.peekAt(1).Kind == token.LBrace {
		return p.parseBlockExpr()
	}
	switch t.Kind {
	case token.If:
		return p.parseIfExpr()
	case token.Dot:
		// Inferred enum case: `.one` with no receiver. Unambiguous in primary
		// position, since member access always has an expression to its left and so
		// never reaches here. Which enum it names is not knowable yet; the checker
		// fills that in from the expected type.
		if p.peekAt(1).Kind == token.LBrace {
			// Anonymous object literal `.{ name = expr }`. Its type is structural, and
			// gopherbuzz has no structural object runtime - but a map already is one:
			// stored keys are reachable as members, so `.{ list = l }.list` works with
			// no new value kind. Fields use `=`, unlike a map literal's `:`.
			p.advance() // '.'
			return p.parseAnonObjectLit()
		}
		if p.peekAt(1).Kind != token.Ident {
			return nil, fmt.Errorf("buzz: line %d:%d: expected an enum case name after '.'", t.Line, t.Col)
		}
		p.advance()
		name := p.advance()
		return &ast.EnumCaseExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: name.Val}, nil
	case token.Fun:
		return p.parseFunExpr()
	case token.LBrace:
		return p.parseMapLit()
	case token.LBracket:
		return p.parseListLit()
	case token.String:
		p.advance()
		return &ast.StringLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Val: t.Val}, nil
	case token.InterpStr:
		p.advance()
		return p.buildInterp(t)
	case token.Pat:
		p.advance()
		return &ast.PatLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Pattern: t.Val}, nil
	case token.Int:
		p.advance()
		// Base 0 lets strconv auto-detect 0x/0o/0b prefixes from the lexer.
		// Strip underscore digit separators (Zig/Rust convention: allowed
		// between digits for readability).
		cleaned := strings.ReplaceAll(t.Val, "_", "")
		n, err := strconv.ParseInt(cleaned, 0, 64)
		if err != nil {
			return nil, fmt.Errorf("buzz: line %d:%d: invalid int %q", t.Line, t.Col, t.Val)
		}
		return &ast.IntLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Val: n}, nil
	case token.Float:
		p.advance()
		cleaned := strings.ReplaceAll(t.Val, "_", "")
		f, err := strconv.ParseFloat(cleaned, 64)
		if err != nil {
			return nil, fmt.Errorf("buzz: line %d:%d: invalid float %q", t.Line, t.Col, t.Val)
		}
		return &ast.FloatLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Val: f}, nil
	case token.True:
		p.advance()
		return &ast.BoolLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Val: true}, nil
	case token.False:
		p.advance()
		return &ast.BoolLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Val: false}, nil
	case token.Null:
		p.advance()
		return &ast.NullLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}}, nil
	case token.Ident:
		p.advance()
		return &ast.IdentExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: t.Val}, nil
	case token.LParen:
		p.depth++
		if p.depth > maxParseDepth {
			return nil, fmt.Errorf("buzz: line %d:%d: expression nested too deeply (limit %d)", t.Line, t.Col, maxParseDepth)
		}
		p.advance()
		expr, err := p.parseExpr()
		p.depth--
		if err != nil {
			return nil, err
		}
		if _, err := p.eat(token.RParen); err != nil {
			return nil, err
		}
		return expr, nil
	default:
		return nil, fmt.Errorf("buzz: line %d:%d: unexpected token %s", t.Line, t.Col, t.Kind)
	}
}

// buildInterp parses each embedded expression source of an interpolation token.
func (p *parser) buildInterp(t token.Token) (ast.Node, error) {
	expr := &ast.InterpExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}}
	for _, part := range t.Parts {
		if !part.IsExpr {
			expr.Parts = append(expr.Parts, ast.InterpPart{Lit: part.Text})
			continue
		}
		// Sub-parse the interpolation expression in the same mode as the enclosing
		// parser so strictness is consistent across the program.
		sub, err := parseModed(part.Text+";", p.strict)
		if err != nil {
			return nil, fmt.Errorf("buzz: line %d:%d: bad interpolation %q: %w", t.Line, t.Col, part.Text, err)
		}
		if len(sub.Stmts) != 1 {
			return nil, fmt.Errorf("buzz: line %d:%d: interpolation must be a single expression: %q", t.Line, t.Col, part.Text)
		}
		es, ok := sub.Stmts[0].(*ast.ExprStmt)
		if !ok {
			return nil, fmt.Errorf("buzz: line %d:%d: interpolation must be an expression: %q", t.Line, t.Col, part.Text)
		}
		expr.Parts = append(expr.Parts, ast.InterpPart{Expr: es.Expr})
	}
	return expr, nil
}

// parseFunExpr parses `fun(params) rettype { body }` as an expression.
func (p *parser) parseFunExpr() (*ast.FunExpr, error) {
	t, _ := p.eat(token.Fun)
	// A lambda can declare type parameters too: `fun::<E, F>() > int => 12`.
	if _, err := p.skipTypeParams(); err != nil {
		return nil, err
	}
	fr, err := p.parseFunRest(false)
	if err != nil {
		return nil, err
	}
	return &ast.FunExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Params: fr.params, ParamAnnots: fr.paramAnnots, ParamDefaults: fr.paramDefaults, RetAnnot: fr.retAnnot, YieldAnnot: fr.yieldAnnot, Body: fr.body}, nil
}

// funRest is the shared tail of a function: (params) rettype *> yieldtype { body }.
type funRest struct {
	params      []string
	paramAnnots []string // parallel to params
	// paramDefaults is parallel to params, with a nil entry for a parameter that
	// declares no `= expr` default.
	paramDefaults []ast.Node
	retAnnot      string
	yieldAnnot    string
	body          *ast.BlockStmt
}

// parseFunRest parses the shared tail of a function: (params) rettype *> yieldtype { body }.
// With extern set the tail ends at a `;` instead of a body, and out.body stays nil.
func (p *parser) parseFunRest(extern bool) (funRest, error) {
	var out funRest
	if _, err := p.eat(token.LParen); err != nil {
		return funRest{}, err
	}
	for !p.check(token.RParen) && !p.check(token.EOF) {
		nameTok, err := p.eatBindingIdent()
		if err != nil {
			return funRest{}, err
		}
		out.params = append(out.params, nameTok.Val)
		// Strict parity with upstream Buzz: every parameter must be typed
		// (`name: type`), including `_` and lambda params.
		if !p.check(token.Colon) {
			return funRest{}, fmt.Errorf("buzz: line %d:%d: parameter %q must have a type annotation (name: type)", nameTok.Line, nameTok.Col, nameTok.Val)
		}
		p.advance()
		pa, err := p.readType()
		if err != nil {
			return funRest{}, err
		}
		out.paramAnnots = append(out.paramAnnots, pa)
		var def ast.Node
		if p.check(token.Assign) {
			p.advance()
			if def, err = p.parseExpr(); err != nil {
				return funRest{}, err
			}
		} else if strings.HasSuffix(pa, "?") {
			// A nullable parameter defaults to null, so a call may omit it - the
			// same rule that lets `final hello: int?;` bind without an initializer.
			def = &ast.NullLit{Pos: ast.Pos{Line: nameTok.Line, Col: nameTok.Col}}
		}
		out.paramDefaults = append(out.paramDefaults, def)
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	if _, err := p.eat(token.RParen); err != nil {
		return funRest{}, err
	}
	// Return type. With an explicit '>' arrow a return type is required, so a
	// leading '{' there unambiguously begins a map type (e.g. fun f() > {str:
	// str}) rather than the function body — isTypeStart deliberately omits '{'
	// for the arrowless case, where '{' is the body. Without '>', the type is
	// optional (a bare '{' is the body, meaning void).
	if p.check(token.Gt) {
		p.advance()
		if p.check(token.Void) {
			p.advance()
			out.retAnnot = "void"
		} else {
			rt, err := p.readType()
			if err != nil {
				return funRest{}, err
			}
			out.retAnnot = rt
		}
	} else if p.check(token.Void) || p.isTypeStart() {
		// Strict parity with upstream Buzz: a return type must be introduced by '>'.
		// `fun f() int {}` and `fun f() void {}` are rejected; write `fun f() > int {}`
		// or, for no return value, `fun f() {}` (implicit void).
		rt := p.peek()
		return funRest{}, fmt.Errorf("buzz: line %d:%d: return type must be preceded by '>' (write `> %s ...`)", rt.Line, rt.Col, rt.Val)
	}
	// Consume optional !> error-set annotation: fun f() > T !> ErrType { }
	if p.check(token.ErrArrow) {
		p.advance()
		if p.isTypeStart() {
			if err := p.skipType(); err != nil {
				return funRest{}, err
			}
		}
	}
	// Consume optional *> yield-type annotation: fun f() > R *> Y { }
	if p.check(token.YieldArrow) {
		ya := p.advance()
		if p.check(token.Void) {
			p.advance()
			out.yieldAnnot = "void"
		} else {
			ya, err := p.readType()
			if err != nil {
				return funRest{}, err
			}
			out.yieldAnnot = ya
		}
		// Strict parity with upstream Buzz (src/Parser.zig): a fiber's yield type
		// must be optional or void — resume returns null on completion, so the
		// yielded type is inherently optional. Reject a non-optional yield type
		// rather than leniently accepting it.
		if out.yieldAnnot != "void" && !strings.HasSuffix(out.yieldAnnot, "?") {
			return funRest{}, fmt.Errorf("buzz: line %d:%d: expected optional type or void for fiber yield type, got %q", ya.Line, ya.Col, out.yieldAnnot)
		}
	}
	// An extern declaration stops here: the semicolon stands where a body would be,
	// and the implementation comes from the host rather than from this source.
	if extern {
		if _, err := p.eat(token.Semicolon); err != nil {
			return funRest{}, err
		}
		return out, nil
	}
	// Expression-body function: `fun f(x: int) > int => expr;` desugars to a
	// block that returns expr, matching upstream Buzz's arrow-body sugar. It
	// works with or without an explicit return type (upstream permits
	// `fun bright(text: str) => color(...)`, no `>` clause), so this check sits
	// after the optional return/error/yield annotations and before the block form.
	if p.check(token.FatArrow) {
		arrow := p.advance()
		pos := ast.Pos{Line: arrow.Line, Col: arrow.Col}
		// A `> void` arrow body is a statement position: its value is discarded,
		// and upstream writes an assignment there (`=> sum = sum + value`). An
		// assignment is not an expression in Buzz, so parseExpr would stop at the
		// target and choke on the `=`; parseAssignTail accepts both shapes and
		// already returns a statement node.
		var stmt ast.Node
		if out.retAnnot == "void" {
			s, err := p.parseAssignTail()
			if err != nil {
				return funRest{}, err
			}
			stmt = s
		} else {
			expr, err := p.parseExpr()
			if err != nil {
				return funRest{}, err
			}
			stmt = &ast.ReturnStmt{Pos: pos, Value: expr}
		}
		if p.check(token.Semicolon) {
			p.advance()
		}
		out.body = &ast.BlockStmt{Pos: pos, Stmts: []ast.Node{stmt}}
		return out, nil
	}
	body, err := p.parseBlock()
	if err != nil {
		return funRest{}, err
	}
	out.body = body
	return out, nil
}

// parseMapLit parses {"key": val, ...}; an empty {} is an empty map.
// parseAnonObjectLit parses `{ name = expr, ... }` (the brace of a `.{...}` literal)
// into a map keyed by the field names.
func (p *parser) parseAnonObjectLit() (*ast.MapExpr, error) {
	t, err := p.eat(token.LBrace)
	if err != nil {
		return nil, err
	}
	m := &ast.MapExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Anon: true}
	for !p.check(token.RBrace) && !p.check(token.EOF) {
		nameTok, err := p.eatIdent()
		if err != nil {
			return nil, err
		}
		val, err := p.parseFieldValue(nameTok)
		if err != nil {
			return nil, err
		}
		m.Keys = append(m.Keys, &ast.StringLit{Pos: ast.Pos{Line: nameTok.Line, Col: nameTok.Col}, Val: nameTok.Val})
		m.Values = append(m.Values, val)
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	if _, err := p.eat(token.RBrace); err != nil {
		return nil, err
	}
	return m, nil
}

func (p *parser) parseMapLit() (*ast.MapExpr, error) {
	t, err := p.eat(token.LBrace)
	if err != nil {
		return nil, err
	}
	m := &ast.MapExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}}
	// Typed empty-map literal `{<K: V>}` (upstream Buzz): the element types are
	// only a static hint, which gopherbuzz tracks dynamically — skip them.
	if p.check(token.Lt) {
		if err := p.skipGenericArgs(); err != nil {
			return nil, err
		}
		if _, err := p.eat(token.RBrace); err != nil {
			return nil, err
		}
		return m, nil
	}
	for !p.check(token.RBrace) && !p.check(token.EOF) {
		key, err := p.parseMapKey()
		if err != nil {
			return nil, err
		}
		if _, err := p.eat(token.Colon); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		m.Keys = append(m.Keys, key)
		m.Values = append(m.Values, val)
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	if _, err := p.eat(token.RBrace); err != nil {
		return nil, err
	}
	return m, nil
}

// parseMapKey accepts a bare identifier (as a string key) or an expression key.
func (p *parser) parseMapKey() (ast.Node, error) {
	t := p.peek()
	if t.Kind == token.Ident && p.peekAt(1).Kind == token.Colon {
		p.advance()
		return &ast.StringLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Val: t.Val}, nil
	}
	return p.parseExpr()
}

func (p *parser) parseListLit() (*ast.ListExpr, error) {
	t, err := p.eat(token.LBracket)
	if err != nil {
		return nil, err
	}
	lst := &ast.ListExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}}
	// Typed empty-list literal `[<T>]` (upstream Buzz): capture the element type
	// so the checker can infer `[T]` rather than defaulting to `[any]`. The VM
	// tracks element types dynamically, but the static checker needs the hint to
	// type-check returns of accumulated lists.
	if p.check(token.Lt) {
		elem, err := p.readGenericArg()
		if err != nil {
			return nil, err
		}
		lst.ElemType = elem
		if _, err := p.eat(token.RBracket); err != nil {
			return nil, err
		}
		return lst, nil
	}
	for !p.check(token.RBracket) && !p.check(token.EOF) {
		item, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		lst.Items = append(lst.Items, item)
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	if _, err := p.eat(token.RBracket); err != nil {
		return nil, err
	}
	return lst, nil
}

// parseObjectLit parses `Name{ field = val, ... }` given the already-parsed name.
func (p *parser) parseObjectLit(name *ast.IdentExpr) (*ast.ObjectLit, error) {
	return p.parseObjectLitNamed(name.Name)
}

// parseFieldValue parses the value of one object-literal field whose name token
// has already been consumed. `field = expr` is the full form; upstream also
// allows punning a same-named variable, so a field followed by `,` or `}`
// resolves to the identifier of the same name.
func (p *parser) parseFieldValue(nameTok token.Token) (ast.Node, error) {
	if p.check(token.Comma) || p.check(token.RBrace) {
		return &ast.IdentExpr{Pos: ast.Pos{Line: nameTok.Line, Col: nameTok.Col}, Name: nameTok.Val}, nil
	}
	if _, err := p.eat(token.Assign); err != nil {
		return nil, err
	}
	return p.parseExpr()
}

// parseObjectLitNamed parses `{ field = val, ... }` for an object whose type
// name is already known (a bare `Name` or the last segment of a qualified
// `ns\Name`).
func (p *parser) parseObjectLitNamed(typeName string) (*ast.ObjectLit, error) {
	t, err := p.eat(token.LBrace)
	if err != nil {
		return nil, err
	}
	lit := &ast.ObjectLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, TypeName: typeName}
	for !p.check(token.RBrace) && !p.check(token.EOF) {
		fieldTok, err := p.eatIdent()
		if err != nil {
			return nil, err
		}
		val, err := p.parseFieldValue(fieldTok)
		if err != nil {
			return nil, err
		}
		lit.Keys = append(lit.Keys, fieldTok.Val)
		lit.Values = append(lit.Values, val)
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	if _, err := p.eat(token.RBrace); err != nil {
		return nil, err
	}
	return lit, nil
}

// isAssignable reports whether n is a valid assignment target.
func isAssignable(n ast.Node) bool {
	switch n.(type) {
	case *ast.IdentExpr, *ast.MemberExpr, *ast.IndexExpr:
		return true
	default:
		return false
	}
}
