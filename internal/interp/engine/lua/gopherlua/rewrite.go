package gopherlua

import (
	"fmt"
	"strconv"
	"strings"

	lua_ast "github.com/yuin/gopher-lua/ast"
	"github.com/yuin/gopher-lua/parse"
)

type rewriteResult struct {
	Rewritten string
}

// rewriteSteps injects __magus_step_hook calls before every executable statement and return point.
func rewriteSteps(src string) (rewriteResult, error) {
	stmts, err := parse.Parse(strings.NewReader(src), "<step>")
	if err != nil {
		return rewriteResult{Rewritten: src}, fmt.Errorf("steprewrite: %w", err)
	}

	lineNums := map[int]struct{}{}
	retNums := map[int]struct{}{}

	c := &stepCollector{lineNums: lineNums, retNums: retNums}
	c.walkStmts(stmts)

	if len(lineNums) == 0 && len(retNums) == 0 {
		return rewriteResult{Rewritten: src}, nil
	}

	return rewriteResult{Rewritten: stepSplice(src, lineNums, retNums)}, nil
}

type stepCollector struct {
	lineNums map[int]struct{}
	retNums  map[int]struct{}
}

func (c *stepCollector) walkStmts(stmts []lua_ast.Stmt) {
	for _, s := range stmts {
		c.walkStmt(s)
	}
}

func (c *stepCollector) walkStmt(s lua_ast.Stmt) {
	if s == nil {
		return
	}
	switch v := s.(type) {
	case *lua_ast.ReturnStmt:
		c.retNums[v.Line()] = struct{}{}
		c.walkExprs(v.Exprs)
	case *lua_ast.AssignStmt:
		c.lineNums[v.Line()] = struct{}{}
		c.walkExprs(v.Rhs)
	case *lua_ast.LocalAssignStmt:
		c.lineNums[v.Line()] = struct{}{}
		c.walkExprs(v.Exprs)
	case *lua_ast.FuncCallStmt:
		c.lineNums[v.Line()] = struct{}{}
		c.walkExpr(v.Expr)
	case *lua_ast.DoBlockStmt:
		c.lineNums[v.Line()] = struct{}{}
		c.walkStmts(v.Stmts)
	case *lua_ast.WhileStmt:
		c.lineNums[v.Line()] = struct{}{}
		c.walkExpr(v.Condition)
		c.walkStmts(v.Stmts)
	case *lua_ast.RepeatStmt:
		c.lineNums[v.Line()] = struct{}{}
		c.walkExpr(v.Condition)
		c.walkStmts(v.Stmts)
	case *lua_ast.IfStmt:
		c.lineNums[v.Line()] = struct{}{}
		c.walkExpr(v.Condition)
		c.walkStmts(v.Then)
		c.walkStmts(v.Else)
	case *lua_ast.NumberForStmt:
		c.lineNums[v.Line()] = struct{}{}
		c.walkStmts(v.Stmts)
	case *lua_ast.GenericForStmt:
		c.lineNums[v.Line()] = struct{}{}
		c.walkStmts(v.Stmts)
	case *lua_ast.FuncDefStmt:
		c.lineNums[v.Line()] = struct{}{}
		if v.Func != nil {
			c.walkFunction(v.Func)
		}
	case *lua_ast.BreakStmt:
		c.lineNums[v.Line()] = struct{}{}
	case *lua_ast.GotoStmt:
		c.lineNums[v.Line()] = struct{}{}
	case *lua_ast.LabelStmt:
		// not executable; skip
	}
}

func (c *stepCollector) walkExprs(exprs []lua_ast.Expr) {
	for _, e := range exprs {
		c.walkExpr(e)
	}
}

func (c *stepCollector) walkExpr(e lua_ast.Expr) {
	if e == nil {
		return
	}
	switch v := e.(type) {
	case *lua_ast.FunctionExpr:
		c.walkFunction(v)
	case *lua_ast.FuncCallExpr:
		c.walkExpr(v.Func)
		c.walkExpr(v.Receiver)
		c.walkExprs(v.Args)
	case *lua_ast.TableExpr:
		for _, f := range v.Fields {
			c.walkExpr(f.Key)
			c.walkExpr(f.Value)
		}
	case *lua_ast.ArithmeticOpExpr:
		c.walkExpr(v.Lhs)
		c.walkExpr(v.Rhs)
	case *lua_ast.LogicalOpExpr:
		c.walkExpr(v.Lhs)
		c.walkExpr(v.Rhs)
	case *lua_ast.RelationalOpExpr:
		c.walkExpr(v.Lhs)
		c.walkExpr(v.Rhs)
	case *lua_ast.StringConcatOpExpr:
		c.walkExpr(v.Lhs)
		c.walkExpr(v.Rhs)
	case *lua_ast.UnaryMinusOpExpr:
		c.walkExpr(v.Expr)
	case *lua_ast.UnaryNotOpExpr:
		c.walkExpr(v.Expr)
	case *lua_ast.UnaryLenOpExpr:
		c.walkExpr(v.Expr)
	case *lua_ast.AttrGetExpr:
		c.walkExpr(v.Object)
	}
}

func (c *stepCollector) walkFunction(fn *lua_ast.FunctionExpr) {
	c.walkStmts(fn.Stmts)
	if !stepEndsWithReturn(fn.Stmts) {
		endLine := fn.LastLine()
		if endLine > 0 {
			c.retNums[endLine] = struct{}{}
		}
	}
}

func stepEndsWithReturn(stmts []lua_ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	_, ok := stmts[len(stmts)-1].(*lua_ast.ReturnStmt)
	return ok
}

// stepSplice inserts __magus_step_hook calls before each instrumented line (LF line endings assumed).
func stepSplice(src string, lineNums, retNums map[int]struct{}) string {
	var b strings.Builder
	b.Grow(len(src) + (len(lineNums)+len(retNums))*40)

	var numBuf [20]byte
	lineNum := 1
	i := 0
	for i < len(src) {
		j := i
		for j < len(src) && src[j] != '\n' {
			j++
		}
		line := src[i:j]
		indent := stepLeadingSpace(line)

		if _, ok := lineNums[lineNum]; ok {
			b.WriteString(indent)
			b.WriteString("__magus_step_hook(")
			b.Write(strconv.AppendInt(numBuf[:0], int64(lineNum), 10))
			b.WriteString(", \"line\")\n")
		}
		if _, ok := retNums[lineNum]; ok {
			b.WriteString(indent)
			b.WriteString("__magus_step_hook(")
			b.Write(strconv.AppendInt(numBuf[:0], int64(lineNum), 10))
			b.WriteString(", \"return\")\n")
		}
		b.WriteString(line)
		if j < len(src) {
			b.WriteByte('\n')
			i = j + 1
			lineNum++
		} else {
			break
		}
	}
	return b.String()
}

func stepLeadingSpace(s string) string {
	for i, c := range s {
		if c != ' ' && c != '\t' {
			return s[:i]
		}
	}
	return s
}
