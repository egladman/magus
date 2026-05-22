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
}

func newParser(tokens []token.Token) *parser {
	return &parser{tokens: tokens}
}

// Parse tokenizes src and returns a Program.
func Parse(src string) (*ast.Program, error) {
	toks, err := token.Tokenize(src)
	if err != nil {
		return nil, err
	}
	return newParser(toks).parseProgram()
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
			prog.Stmts = append(prog.Stmts, s)
		}
	}
	return prog, nil
}

func (p *parser) parseStmt() (ast.Node, error) {
	t := p.peek()
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
	case token.Const, token.Var:
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
	nameTok, err := p.eatIdent()
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
	t := p.advance() // const/var
	isConst := t.Kind == token.Const
	nameTok, err := p.eatIdent()
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
	t := p.peek()
	switch t.Kind {
	case token.Ident, token.Void:
		p.advance()
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
		if p.isTypeStart() {
			if err := p.skipType(); err != nil {
				return err
			}
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
	if _, err := p.eat(token.LParen); err != nil {
		return nil, err
	}
	nameTok, err := p.eatIdent()
	if err != nil {
		return nil, err
	}
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
	catch, err := p.parseBlock()
	if err != nil {
		return nil, err
	}
	return &ast.TryStmt{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Body: body, ErrName: nameTok.Val, Catch: catch}, nil
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
	if p.check(token.Const) || p.check(token.Var) {
		return p.parseDeclNoSemi()
	}
	return p.parseAssignTail()
}

// parseDeclNoSemi parses a const/var declaration without consuming a semicolon.
func (p *parser) parseDeclNoSemi() (*ast.DeclStmt, error) {
	t := p.advance()
	isConst := t.Kind == token.Const
	nameTok, err := p.eatIdent()
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
	first, err := p.eatIdent()
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
		second, err := p.eatIdent()
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
	nameTok, err := p.eatIdent()
	if err != nil {
		return nil, err
	}
	params, paramAnnots, retAnnot, yieldAnnot, body, err := p.parseFunRest()
	if err != nil {
		return nil, err
	}
	return &ast.FunDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: nameTok.Val, Params: params, ParamAnnots: paramAnnots, RetAnnot: retAnnot, YieldAnnot: yieldAnnot, Body: body, Doc: t.Doc}, nil
}

func (p *parser) parseObjectDecl() (*ast.ObjectDecl, error) {
	t, _ := p.eat(token.Object)
	nameTok, err := p.eatIdent()
	if err != nil {
		return nil, err
	}
	if _, err := p.eat(token.LBrace); err != nil {
		return nil, err
	}
	decl := &ast.ObjectDecl{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Name: nameTok.Val}
	for !p.check(token.RBrace) && !p.check(token.EOF) {
		if p.check(token.Fun) {
			method, err := p.parseFunDecl()
			if err != nil {
				return nil, err
			}
			decl.Methods = append(decl.Methods, method)
			p.optSemicolon()
			continue
		}
		nameTok, err := p.eatIdent()
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
	nameTok, err := p.eatIdent()
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
		caseTok, err := p.eatIdent()
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
			typStr, err := p.readType()
			if err != nil {
				return nil, err
			}
			left = &ast.AsExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Expr: left, TypeName: typStr}
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
	// yield expr — suspends the fiber (or is dismissed outside one)
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
	for {
		switch p.peek().Kind {
		case token.Dot:
			t := p.advance()
			nameTok, err := p.eatIdent()
			if err != nil {
				return nil, err
			}
			node = &ast.MemberExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Object: node, Name: nameTok.Val}
		case token.LParen:
			t := p.advance()
			args, err := p.parseArgList()
			if err != nil {
				return nil, err
			}
			if _, err := p.eat(token.RParen); err != nil {
				return nil, err
			}
			node = &ast.CallExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Callee: node, Args: args}
		case token.LBracket:
			t := p.advance()
			idx, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			if _, err := p.eat(token.RBracket); err != nil {
				return nil, err
			}
			node = &ast.IndexExpr{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Object: node, Index: idx}
		case token.LBrace:
			id, ok := node.(*ast.IdentExpr)
			if !ok {
				return node, nil
			}
			lit, err := p.parseObjectLit(id)
			if err != nil {
				return nil, err
			}
			node = lit
		default:
			return node, nil
		}
	}
}

func (p *parser) parseArgList() ([]ast.Node, error) {
	var args []ast.Node
	for !p.check(token.RParen) && !p.check(token.EOF) {
		arg, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if !p.check(token.Comma) {
			break
		}
		p.advance()
	}
	return args, nil
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
	case token.Int:
		p.advance()
		n, err := strconv.ParseInt(t.Val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("buzz: line %d:%d: invalid int %q", t.Line, t.Col, t.Val)
		}
		return &ast.IntLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, Val: n}, nil
	case token.Float:
		p.advance()
		f, err := strconv.ParseFloat(t.Val, 64)
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
		sub, err := Parse(part.Text + ";")
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
		nameTok, e := p.eatIdent()
		if e != nil {
			return nil, nil, "", "", nil, e
		}
		params = append(params, nameTok.Val)
		var pa string
		if p.check(token.Colon) {
			p.advance()
			if pa, e = p.readType(); e != nil {
				return nil, nil, "", "", nil, e
			}
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
	} else if p.check(token.Void) {
		p.advance()
		retAnnot = "void"
	} else if p.isTypeStart() {
		if retAnnot, err = p.readType(); err != nil {
			return nil, nil, "", "", nil, err
		}
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
		p.advance()
		if yieldAnnot, err = p.readType(); err != nil {
			return nil, nil, "", "", nil, err
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
	t, err := p.eat(token.LBrace)
	if err != nil {
		return nil, err
	}
	lit := &ast.ObjectLit{Pos: ast.Pos{Line: t.Line, Col: t.Col}, TypeName: name.Name}
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
