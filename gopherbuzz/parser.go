package buzz

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egladman/gopherbuzz/ast"
	"github.com/egladman/gopherbuzz/token"
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

// eatBindingIdent consumes an identifier used as a binding name and rejects any
// upstream-reserved word (strict parity with upstream Buzz).
func (p *parser) eatBindingIdent() (token.Token, error) {
	t, err := p.eatIdent()
	if err != nil {
		return t, err
	}
	if reservedIdents[t.Val] {
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
		p.optSemicolon()
		return &ast.BreakStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}}, nil
	case token.Continue:
		p.advance()
		p.optSemicolon()
		return &ast.ContinueStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}}, nil
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
	if _, err := p.eat(token.Assign); err != nil {
		return nil, err
	}
	val, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	p.optSemicolon()
	return &ast.DeclStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, IsConst: isConst, Name: nameTok.Val, TypeAnnot: typeAnnot, Value: val}, nil
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
		p.advance()
		// Namespace-qualified type: serialize\Boxed, foo\bar\Baz.
		for p.check(token.Backslash) {
			p.advance()
			if _, err := p.eat(token.Ident); err != nil {
				return err
			}
		}
		if p.check(token.Lt) {
			if err := p.skipGenericArgs(); err != nil {
				return err
			}
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
		sb.WriteString(tokenText(p.tokens[i]))
	}
	return sb.String()
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
	cond, err := p.parseParenCond()
	if err != nil {
		return nil, err
	}
	then, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	out := &ast.IfStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Cond: cond, Then: then}
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
	if _, err := p.eat(token.Catch); err != nil {
		return nil, err
	}
	// Catch-all form `catch { ... }` (upstream Buzz): no binding. The error is
	// still pushed by the VM, so bind it to a throwaway "_" slot the body ignores.
	errName := "_"
	if p.check(token.LParen) {
		p.advance()
		nameTok, err := p.eatBindingIdent()
		if err != nil {
			return nil, err
		}
		errName = nameTok.Val
		// Skip optional type annotation: catch (e: Type)
		if p.check(token.Colon) {
			p.advance()
			if err := p.skipType(); err != nil {
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
	return &ast.TryStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Body: body, ErrName: errName, Catch: catch}, nil
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
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.WhileStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Cond: cond, Body: body}, nil
}

func (p *parser) parseForLoop() (*ast.ForStmt, error) {
	t, _ := p.eat(token.For)
	if _, err := p.eat(token.LParen); err != nil {
		return nil, err
	}
	out := &ast.ForStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}}
	if !p.check(token.Semicolon) {
		init, err := p.parseForInit()
		if err != nil {
			return nil, err
		}
		out.Init = init
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
	if !p.check(token.RParen) {
		post, err := p.parseAssignTail()
		if err != nil {
			return nil, err
		}
		out.Post = post
	}
	if _, err := p.eat(token.RParen); err != nil {
		return nil, err
	}
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
	return p.parseAssignTail()
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
	return &ast.ExprStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Expr: expr}, nil
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
	body, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	out.Body = body
	return out, nil
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
	params, paramAnnots, retAnnot, yieldAnnot, body, err := p.parseFunRest()
	if err != nil {
		return nil, err
	}
	return &ast.FunDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: nameTok.Val, Params: params, ParamAnnots: paramAnnots, RetAnnot: retAnnot, YieldAnnot: yieldAnnot, Body: body, Doc: t.Doc}, nil
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
	if _, err := p.eat(token.LBrace); err != nil {
		return nil, err
	}
	decl := &ast.ObjectDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: nameTok.Val}
	for !p.check(token.RBrace) && !p.check(token.EOF) {
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
		decl.Fields = append(decl.Fields, field)
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
	decl := &ast.EnumDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: nameTok.Val}
	for !p.check(token.RBrace) && !p.check(token.EOF) {
		caseTok, err := p.eatBindingIdent()
		if err != nil {
			return nil, err
		}
		if p.check(token.Assign) {
			p.advance()
			if _, err := p.parseExpr(); err != nil {
				return nil, err
			}
		}
		decl.Cases = append(decl.Cases, caseTok.Val)
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

func (p *parser) parseExpr() (ast.Node, error) { return p.parseRange() }

func (p *parser) parseRange() (ast.Node, error) {
	left, err := p.parseCoalesce()
	if err != nil {
		return nil, err
	}
	if p.check(token.DotDot) {
		t := p.advance()
		right, err := p.parseCoalesce()
		if err != nil {
			return nil, err
		}
		return &ast.RangeExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Lo: left, Hi: right}, nil
	}
	return left, nil
}

func (p *parser) parseCoalesce() (ast.Node, error) {
	left, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	for p.check(token.Coalesce) {
		t := p.advance()
		right, err := p.parseOr()
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
	left, err := p.parseAdditive()
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
			right, err := p.parseAdditive()
			if err != nil {
				return nil, err
			}
			left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: op, Left: left, Right: right}
		case token.Is:
			t := p.advance()
			typeTok, err := p.eatIdent()
			if err != nil {
				return nil, err
			}
			left = &ast.IsExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Expr: left, TypeName: typeTok.Val}
		case token.As:
			t := p.advance()
			optional := false
			if p.check(token.Question) { // `as?` — null on mismatch (upstream Buzz)
				p.advance()
				optional = true
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
	left, err := p.parseMultiplicative()
	if err != nil {
		return nil, err
	}
	for p.check(token.Plus) || p.check(token.Minus) {
		t := p.advance()
		op := "+"
		if t.Kind == token.Minus {
			op = "-"
		}
		right, err := p.parseMultiplicative()
		if err != nil {
			return nil, err
		}
		left = &ast.BinaryExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Op: op, Left: left, Right: right}
	}
	return left, nil
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
	if p.check(token.Bang) || p.check(token.Minus) {
		t := p.advance()
		op := "!"
		if t.Kind == token.Minus {
			op = "-"
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
	for {
		switch p.peek().Kind {
		case token.Dot:
			t := p.advance()
			nameTok, err := p.eatIdent()
			if err != nil {
				return nil, err
			}
			node = &ast.MemberExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Object: node, Name: nameTok.Val}
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
				def, err := p.parseExpr()
				if err != nil {
					return nil, err
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
			node = &ast.IndexExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Object: node, Index: idx, Optional: optional}
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
		if p.check(token.Ident) && p.peekAt(1).Kind == token.Colon {
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
	switch t.Kind {
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
	params, paramAnnots, retAnnot, yieldAnnot, body, err := p.parseFunRest()
	if err != nil {
		return nil, err
	}
	return &ast.FunExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Params: params, ParamAnnots: paramAnnots, RetAnnot: retAnnot, YieldAnnot: yieldAnnot, Body: body}, nil
}

// parseFunRest parses the shared tail of a function: (params) rettype *> yieldtype { body }.
func (p *parser) parseFunRest() (params []string, paramAnnots []string, retAnnot string, yieldAnnot string, body *ast.BlockStmt, err error) {
	if _, err = p.eat(token.LParen); err != nil {
		return nil, nil, "", "", nil, err
	}
	for !p.check(token.RParen) && !p.check(token.EOF) {
		nameTok, e := p.eatBindingIdent()
		if e != nil {
			return nil, nil, "", "", nil, e
		}
		params = append(params, nameTok.Val)
		// Strict parity with upstream Buzz: every parameter must be typed
		// (`name: type`), including `_` and lambda params.
		if !p.check(token.Colon) {
			return nil, nil, "", "", nil, fmt.Errorf("buzz: line %d:%d: parameter %q must have a type annotation (name: type)", nameTok.Line, nameTok.Col, nameTok.Val)
		}
		var pa string
		p.advance()
		if pa, e = p.readType(); e != nil {
			return nil, nil, "", "", nil, e
		}
		paramAnnots = append(paramAnnots, pa)
		if p.check(token.Assign) {
			p.advance()
			if _, e := p.parseExpr(); e != nil {
				return nil, nil, "", "", nil, e
			}
		}
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	if _, err = p.eat(token.RParen); err != nil {
		return nil, nil, "", "", nil, err
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
			retAnnot = "void"
		} else if retAnnot, err = p.readType(); err != nil {
			return nil, nil, "", "", nil, err
		}
	} else if p.check(token.Void) || p.isTypeStart() {
		// Strict parity with upstream Buzz: a return type must be introduced by '>'.
		// `fun f() int {}` and `fun f() void {}` are rejected; write `fun f() > int {}`
		// or, for no return value, `fun f() {}` (implicit void).
		rt := p.peek()
		return nil, nil, "", "", nil, fmt.Errorf("buzz: line %d:%d: return type must be preceded by '>' (write `> %s ...`)", rt.Line, rt.Col, rt.Val)
	}
	// Consume optional !> error-set annotation: fun f() > T !> ErrType { }
	if p.check(token.ErrArrow) {
		p.advance()
		if p.isTypeStart() {
			if err := p.skipType(); err != nil {
				return nil, nil, "", "", nil, err
			}
		}
	}
	// Consume optional *> yield-type annotation: fun f() > R *> Y { }
	if p.check(token.YieldArrow) {
		ya := p.advance()
		if p.check(token.Void) {
			p.advance()
			yieldAnnot = "void"
		} else if yieldAnnot, err = p.readType(); err != nil {
			return nil, nil, "", "", nil, err
		}
		// Strict parity with upstream Buzz (src/Parser.zig): a fiber's yield type
		// must be optional or void — resume returns null on completion, so the
		// yielded type is inherently optional. Reject a non-optional yield type
		// rather than leniently accepting it.
		if yieldAnnot != "void" && !strings.HasSuffix(yieldAnnot, "?") {
			return nil, nil, "", "", nil, fmt.Errorf("buzz: line %d:%d: expected optional type or void for fiber yield type, got %q", ya.Line, ya.Col, yieldAnnot)
		}
	}
	body, err = p.parseBlock()
	if err != nil {
		return nil, nil, "", "", nil, err
	}
	return params, paramAnnots, retAnnot, yieldAnnot, body, nil
}

// parseMapLit parses {"key": val, ...}; an empty {} is an empty map.
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
		if _, err := p.eat(token.Assign); err != nil {
			return nil, err
		}
		val, err := p.parseExpr()
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
