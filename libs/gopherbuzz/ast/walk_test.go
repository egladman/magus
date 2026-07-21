package ast

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fixture: fun f() { g(); } — nesting FunDecl > BlockStmt > ExprStmt > CallExpr > IdentExpr.
func walkFixture() *FunDecl {
	return &FunDecl{
		Name: "f",
		Body: &BlockStmt{Stmts: []Node{
			&ExprStmt{Expr: &CallExpr{Callee: &IdentExpr{Name: "g"}}},
		}},
	}
}

func TestInspectVisitsEveryNode(t *testing.T) {
	var visited []string
	Inspect(walkFixture(), func(n Node) bool {
		visited = append(visited, fmt.Sprintf("%T", n))
		return true
	})
	assert.Equal(t, []string{
		"*ast.FunDecl", "*ast.BlockStmt", "*ast.ExprStmt", "*ast.CallExpr", "*ast.IdentExpr",
	}, visited)
}

func TestInspectSkipsChildrenOnFalse(t *testing.T) {
	var visited []string
	Inspect(walkFixture(), func(n Node) bool {
		visited = append(visited, fmt.Sprintf("%T", n))
		_, isBlock := n.(*BlockStmt)
		return !isBlock // stop descending at the block; its children must not be visited
	})
	assert.Equal(t, []string{"*ast.FunDecl", "*ast.BlockStmt"}, visited)
}

func TestInspectNilIsSafe(t *testing.T) {
	assert.NotPanics(t, func() {
		Inspect(nil, func(Node) bool { return true })
		// A nil child (e.g. an if with no else) must not be visited or panic.
		Inspect(&IfStmt{Cond: &IdentExpr{Name: "x"}}, func(Node) bool { return true })
	})
}
