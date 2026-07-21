package std

import (
	"context"
	"encoding/json"
	"fmt"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
)

// debugModule builds the "debug" module matching Buzz's debug reference:
// https://buzz-lang.dev/0.5.0/reference/std/debug.html
func debugModule() vm.Value {
	m := mod()
	m.MapSet("dump", fn("debug.dump", debugDump))
	m.MapSet("ast", fn("debug.ast", debugAST))
	return m
}

func debugDump(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 {
		fmt.Println("null")
		return vm.Null, nil
	}
	fmt.Println(args[0].String())
	return vm.Null, nil
}

func debugAST(_ context.Context, args []vm.Value) (vm.Value, error) {
	if len(args) < 1 || !args[0].IsStr() {
		return vm.Null, fmt.Errorf("debug.ast: requires a str source argument")
	}
	prog, err := buzz.Parse(args[0].AsString())
	if err != nil {
		return vm.Null, fmt.Errorf("debug.ast: %w", err)
	}
	stmts := make([]any, len(prog.Stmts))
	for i, s := range prog.Stmts {
		stmts[i] = nodeToMap(s)
	}
	b, err := json.Marshal(map[string]any{"kind": "Program", "stmts": stmts})
	if err != nil {
		return vm.Null, fmt.Errorf("debug.ast: %w", err)
	}
	return vm.StrValue(string(b)), nil
}

func nodeToMap(n ast.Node) any {
	if n == nil {
		return nil
	}
	pos := func(p ast.Pos) map[string]any {
		return map[string]any{"line": p.Line, "col": p.Col}
	}
	ns := func(xs []ast.Node) []any {
		out := make([]any, len(xs))
		for i, x := range xs {
			out[i] = nodeToMap(x)
		}
		return out
	}
	switch v := n.(type) {
	case *ast.ImportStmt:
		return map[string]any{"kind": "ImportStmt", "pos": pos(v.Pos), "path": v.Path, "alias": v.Alias}
	case *ast.NamespaceStmt:
		return map[string]any{"kind": "NamespaceStmt", "pos": pos(v.Pos), "name": v.Name}
	case *ast.DeclStmt:
		return map[string]any{"kind": "DeclStmt", "pos": pos(v.Pos), "isExported": v.IsExported, "isConst": v.IsConst, "name": v.Name, "typeAnnot": v.TypeAnnot, "value": nodeToMap(v.Value)}
	case *ast.AssignStmt:
		return map[string]any{"kind": "AssignStmt", "pos": pos(v.Pos), "target": nodeToMap(v.Target), "value": nodeToMap(v.Value)}
	case *ast.ReturnStmt:
		return map[string]any{"kind": "ReturnStmt", "pos": pos(v.Pos), "value": nodeToMap(v.Value)}
	case *ast.ExprStmt:
		return map[string]any{"kind": "ExprStmt", "pos": pos(v.Pos), "expr": nodeToMap(v.Expr)}
	case *ast.BlockStmt:
		return map[string]any{"kind": "BlockStmt", "pos": pos(v.Pos), "stmts": ns(v.Stmts)}
	case *ast.IfStmt:
		return map[string]any{"kind": "IfStmt", "pos": pos(v.Pos), "cond": nodeToMap(v.Cond), "then": nodeToMap(v.Then), "else": nodeToMap(v.Else)}
	case *ast.WhileStmt:
		return map[string]any{"kind": "WhileStmt", "pos": pos(v.Pos), "cond": nodeToMap(v.Cond), "body": nodeToMap(v.Body)}
	case *ast.ForStmt:
		return map[string]any{"kind": "ForStmt", "pos": pos(v.Pos), "init": nodeToMap(v.Init), "cond": nodeToMap(v.Cond), "post": nodeToMap(v.Post), "body": nodeToMap(v.Body)}
	case *ast.ForEachStmt:
		return map[string]any{"kind": "ForEachStmt", "pos": pos(v.Pos), "keyName": v.KeyName, "valName": v.ValName, "iter": nodeToMap(v.Iter), "body": nodeToMap(v.Body)}
	case *ast.BreakStmt:
		return map[string]any{"kind": "BreakStmt", "pos": pos(v.Pos)}
	case *ast.ContinueStmt:
		return map[string]any{"kind": "ContinueStmt", "pos": pos(v.Pos)}
	case *ast.FunDecl:
		return map[string]any{"kind": "FunDecl", "pos": pos(v.Pos), "isExported": v.IsExported, "name": v.Name, "params": v.Params, "paramAnnots": v.ParamAnnots, "retAnnot": v.RetAnnot, "body": nodeToMap(v.Body)}
	case *ast.ObjectDecl:
		fields := make([]any, len(v.Fields))
		for i, f := range v.Fields {
			fields[i] = map[string]any{"name": f.Name, "typeAnnot": f.TypeAnnot, "default": nodeToMap(f.Default)}
		}
		methods := make([]any, len(v.Methods))
		for i, meth := range v.Methods {
			methods[i] = nodeToMap(meth)
		}
		return map[string]any{"kind": "ObjectDecl", "pos": pos(v.Pos), "name": v.Name, "fields": fields, "methods": methods}
	case *ast.EnumDecl:
		return map[string]any{"kind": "EnumDecl", "pos": pos(v.Pos), "name": v.Name, "cases": v.Cases}
	case *ast.DoStmt:
		return map[string]any{"kind": "DoStmt", "pos": pos(v.Pos), "body": nodeToMap(v.Body), "cond": nodeToMap(v.Cond)}
	case *ast.TryStmt:
		return map[string]any{"kind": "TryStmt", "pos": pos(v.Pos), "body": nodeToMap(v.Body), "errName": v.ErrName, "catch": nodeToMap(v.Catch)}
	case *ast.ThrowStmt:
		return map[string]any{"kind": "ThrowStmt", "pos": pos(v.Pos), "value": nodeToMap(v.Value)}
	case *ast.YieldExpr:
		return map[string]any{"kind": "YieldExpr", "pos": pos(v.Pos), "value": nodeToMap(v.Value)}
	case *ast.BinaryExpr:
		return map[string]any{"kind": "BinaryExpr", "pos": pos(v.Pos), "op": v.Op, "left": nodeToMap(v.Left), "right": nodeToMap(v.Right)}
	case *ast.UnaryExpr:
		return map[string]any{"kind": "UnaryExpr", "pos": pos(v.Pos), "op": v.Op, "operand": nodeToMap(v.Operand)}
	case *ast.CallExpr:
		return map[string]any{"kind": "CallExpr", "pos": pos(v.Pos), "callee": nodeToMap(v.Callee), "args": ns(v.Args)}
	case *ast.MemberExpr:
		return map[string]any{"kind": "MemberExpr", "pos": pos(v.Pos), "object": nodeToMap(v.Object), "name": v.Name}
	case *ast.IndexExpr:
		return map[string]any{"kind": "IndexExpr", "pos": pos(v.Pos), "object": nodeToMap(v.Object), "index": nodeToMap(v.Index)}
	case *ast.FunExpr:
		return map[string]any{"kind": "FunExpr", "pos": pos(v.Pos), "params": v.Params, "paramAnnots": v.ParamAnnots, "retAnnot": v.RetAnnot, "body": nodeToMap(v.Body)}
	case *ast.MapExpr:
		return map[string]any{"kind": "MapExpr", "pos": pos(v.Pos), "keys": ns(v.Keys), "values": ns(v.Values)}
	case *ast.ListExpr:
		return map[string]any{"kind": "ListExpr", "pos": pos(v.Pos), "items": ns(v.Items)}
	case *ast.ObjectLit:
		return map[string]any{"kind": "ObjectLit", "pos": pos(v.Pos), "typeName": v.TypeName, "keys": v.Keys, "values": ns(v.Values)}
	case *ast.InterpExpr:
		parts := make([]any, len(v.Parts))
		for i, p := range v.Parts {
			parts[i] = map[string]any{"lit": p.Lit, "expr": nodeToMap(p.Expr)}
		}
		return map[string]any{"kind": "InterpExpr", "pos": pos(v.Pos), "parts": parts}
	case *ast.IdentExpr:
		return map[string]any{"kind": "IdentExpr", "pos": pos(v.Pos), "name": v.Name}
	case *ast.StringLit:
		return map[string]any{"kind": "StringLit", "pos": pos(v.Pos), "val": v.Val}
	case *ast.IntLit:
		return map[string]any{"kind": "IntLit", "pos": pos(v.Pos), "val": v.Val}
	case *ast.FloatLit:
		return map[string]any{"kind": "FloatLit", "pos": pos(v.Pos), "val": v.Val}
	case *ast.BoolLit:
		return map[string]any{"kind": "BoolLit", "pos": pos(v.Pos), "val": v.Val}
	case *ast.NullLit:
		return map[string]any{"kind": "NullLit", "pos": pos(v.Pos)}
	case *ast.RangeExpr:
		return map[string]any{"kind": "RangeExpr", "pos": pos(v.Pos), "lo": nodeToMap(v.Lo), "hi": nodeToMap(v.Hi)}
	case *ast.IsExpr:
		return map[string]any{"kind": "IsExpr", "pos": pos(v.Pos), "expr": nodeToMap(v.Expr), "typeName": v.TypeName}
	case *ast.AsExpr:
		return map[string]any{"kind": "AsExpr", "pos": pos(v.Pos), "expr": nodeToMap(v.Expr), "typeName": v.TypeName}
	default:
		return map[string]any{"kind": "Unknown"}
	}
}
