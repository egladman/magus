package std

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/egladman/magus/internal/readlog"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/token"
	"github.com/egladman/magus/types"
)

// Buzz is the "buzz" host module: Buzz source read as data, through the
// parser magus itself runs.
var Buzz = Module{
	Name: "buzz",
	Doc:  "Buzz source read as data, through the parser magus runs.",
	Methods: []Method{
		{
			Name: "parse",
			Doc:  `Parse the Buzz file at path into {path, error, functions, statements, tests}. It never raises: a file that cannot be read or parsed comes back with error set and empty lists. Functions holds every fun, method and function literal with its extent, its !> error type and the lines of the throw statements it owns; statements holds the top-level statements; tests holds the test blocks. Lines are 1-based. embedded parses the way a magusfile's imports load.`,
			Args: []Arg{
				{Name: "path"},
				{Name: "embedded", Optional: true, Default: false},
			},
			Returns: []Ret{{Object: "BuzzFile"}},
			Impl:    BuzzParse,
		},
	},
}

// BuzzParse parses the Buzz file at path. A read or parse failure comes back
// in BuzzFile.Error, never as the error result, so a script reads a value.
func BuzzParse(ctx context.Context, path string, embedded bool) (types.BuzzFile, error) {
	file := types.BuzzFile{
		Path:       path,
		Functions:  []types.BuzzFunction{},
		Statements: []types.BuzzStatement{},
		Tests:      []types.BuzzTest{},
	}
	if err := fillBuzzFile(ctx, &file, embedded); err != nil {
		file.Error = err.Error()
	}
	return file, nil
}

func fillBuzzFile(ctx context.Context, file *types.BuzzFile, embedded bool) error {
	src, err := readBuzzSource(ctx, file.Path)
	if err != nil {
		return err
	}
	parse := buzz.Parse
	if embedded {
		parse = buzz.ParseEmbedded
	}
	prog, err := parse(src)
	if err != nil {
		return err
	}
	// The parser accepted src, so tokenizing it again cannot fail.
	toks, _ := token.Tokenize(src)
	w := buzzWalker{file: file, closers: braceClosers(toks)}
	for i, stmt := range prog.Stmts {
		file.Statements = append(file.Statements, types.BuzzStatement{
			Kind:    statementKind(stmt),
			Name:    statementName(stmt),
			Line:    ast.NodePos(stmt).Line,
			EndLine: statementEnd(toks, prog.Stmts, i),
		})
		w.scan(stmt, nil)
	}
	return nil
}

func readBuzzSource(ctx context.Context, path string) (string, error) {
	resolved := resolvePath(ctx, path)
	if err := checkRead(ctx, resolved); err != nil {
		return "", err
	}
	readlog.File(ctx, resolved)
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("buzz.parse %q: %w", resolved, err)
	}
	return string(data), nil
}

// braceClosers maps each { token to the line of the } that closes it. The AST
// records where a block opens but not where it closes.
func braceClosers(toks []token.Token) map[ast.Pos]int {
	closers := map[ast.Pos]int{}
	var open []ast.Pos
	for _, t := range toks {
		switch t.Kind {
		case token.LBrace:
			open = append(open, ast.Pos{Line: t.Line, Col: t.Col})
		case token.RBrace:
			if len(open) > 0 {
				closers[open[len(open)-1]] = t.Line
				open = open[:len(open)-1]
			}
		}
	}
	return closers
}

// statementEnd is the line of the last token before the next top-level
// statement starts. Comments are not tokens, so a trailing comment does not
// stretch it.
func statementEnd(toks []token.Token, stmts []ast.Node, i int) int {
	next := ast.Pos{Line: int(^uint(0) >> 1)}
	if i+1 < len(stmts) {
		next = ast.NodePos(stmts[i+1])
	}
	end := ast.NodePos(stmts[i]).Line
	for _, t := range toks {
		if t.Kind == token.EOF || !posBefore(ast.Pos{Line: t.Line, Col: t.Col}, next) {
			break
		}
		end = t.Line
	}
	return end
}

func posBefore(a, b ast.Pos) bool {
	return a.Line < b.Line || (a.Line == b.Line && a.Col < b.Col)
}

type buzzWalker struct {
	file    *types.BuzzFile
	closers map[ast.Pos]int
}

func (w *buzzWalker) endLine(body *ast.BlockStmt, line int) int {
	if body == nil {
		return line
	}
	if end, ok := w.closers[body.Pos]; ok {
		return end
	}
	return body.Line
}

// scan records every function and test under n. Each throw goes to throws,
// the list of the innermost function or test that holds it; nil drops it.
func (w *buzzWalker) scan(n ast.Node, throws *[]int) {
	ast.Inspect(n, func(c ast.Node) bool {
		switch c := c.(type) {
		case *ast.ThrowStmt:
			if throws != nil {
				*throws = append(*throws, c.Line)
			}
		case *ast.FunDecl:
			w.function(c.Name, "", c.Pos, c.ErrAnnot, c.ParamDefaults, c.Body)
			return false
		case *ast.FunExpr:
			w.function("", "", c.Pos, c.ErrAnnot, c.ParamDefaults, c.Body)
			return false
		case *ast.ObjectDecl:
			for _, f := range append(append([]ast.ObjField{}, c.Fields...), c.StaticFields...) {
				w.scan(f.Default, throws)
			}
			for _, m := range c.Methods {
				w.function(m.Name, c.Name, m.Pos, m.ErrAnnot, m.ParamDefaults, m.Body)
			}
			return false
		case *ast.TestDecl:
			test := types.BuzzTest{Name: c.Name, Line: c.Line, EndLine: w.endLine(c.Body, c.Line), Throws: []int{}}
			if c.Body != nil {
				w.scan(c.Body, &test.Throws)
			}
			w.file.Tests = append(w.file.Tests, test)
			return false
		}
		return true
	})
}

func (w *buzzWalker) function(name, object string, pos ast.Pos, errType string, defaults []ast.Node, body *ast.BlockStmt) {
	idx := len(w.file.Functions)
	w.file.Functions = append(w.file.Functions, types.BuzzFunction{
		Name:       name,
		ObjectName: object,
		Line:       pos.Line,
		EndLine:    w.endLine(body, pos.Line),
		ErrorType:  errType,
	})
	throws := []int{}
	for _, d := range defaults {
		w.scan(d, &throws)
	}
	if body != nil {
		w.scan(body, &throws)
	}
	// Scanning appends nested functions, so a held pointer could go stale.
	w.file.Functions[idx].Throws = throws
}

func statementKind(n ast.Node) string {
	switch n := n.(type) {
	case *ast.ImportStmt:
		return "import"
	case *ast.NamespaceStmt:
		return "namespace"
	case *ast.DeclStmt:
		return "decl"
	case *ast.AssignStmt:
		return "assign"
	case *ast.ExprStmt:
		return "expr"
	case *ast.FunDecl:
		return "fun"
	case *ast.ObjectDecl:
		if n.IsProtocol {
			return "protocol"
		}
		return "object"
	case *ast.EnumDecl:
		return "enum"
	case *ast.TestDecl:
		return "test"
	case *ast.BlockStmt:
		return "block"
	case *ast.IfStmt:
		return "if"
	case *ast.WhileStmt:
		return "while"
	case *ast.ForStmt:
		return "for"
	case *ast.ForEachStmt:
		return "foreach"
	case *ast.DoStmt:
		return "do"
	case *ast.TryStmt:
		return "try"
	case *ast.ThrowStmt:
		return "throw"
	case *ast.ReturnStmt:
		return "return"
	case *ast.BreakStmt:
		return "break"
	case *ast.ContinueStmt:
		return "continue"
	case *ast.OutStmt:
		return "out"
	}
	// A node kind gopherbuzz adds later still gets a kind, from its Go type.
	return strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast."), "Stmt"))
}

func statementName(n ast.Node) string {
	switch n := n.(type) {
	case *ast.ImportStmt:
		return n.Path
	case *ast.NamespaceStmt:
		return n.Name
	case *ast.DeclStmt:
		return n.Name
	case *ast.FunDecl:
		return n.Name
	case *ast.ObjectDecl:
		return n.Name
	case *ast.EnumDecl:
		return n.Name
	case *ast.TestDecl:
		return n.Name
	}
	return ""
}
