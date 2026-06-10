package buzz

import (
	"github.com/egladman/gopherbuzz/ast"
	vmpackage "github.com/egladman/gopherbuzz/vm"
)

// zdef-as-declaration support.
//
// Upstream Buzz's `zdef("lib", "<decls>")` is a statement that declares the C
// functions it names as free functions in the enclosing scope, called by bare
// name. gopherbuzz's zdef() builtin instead returns a handle map (lib.Func()).
// To run upstream-form FFI source unchanged, a top-level zdef statement is
// lowered here: the names are resolved at compile time and bound as module
// globals from the handle (compiler.go), and pre-declared so bare calls
// type-check (checker.go). The handle builtin is untouched underneath.

// zdefDeclNames reports whether expr is a `zdef(<lib>, <decls>)` call with a
// statically-known decls string, and if so returns the call and the symbol
// names it declares. The decls argument may be a string literal or a `+`-chain
// of string literals (the concatenated-prototype style cg() uses).
func zdefDeclNames(expr ast.Node) (*ast.CallExpr, []string, bool) {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil, nil, false
	}
	id, ok := call.Callee.(*ast.IdentExpr)
	if !ok || id.Name != "zdef" || len(call.Args) < 2 {
		return nil, nil, false
	}
	decls, ok := foldStringConcat(call.Args[1])
	if !ok {
		return nil, nil, false
	}
	names := vmpackage.FFIDeclNames(decls)
	if len(names) == 0 {
		return nil, nil, false
	}
	return call, names, true
}

// foldStringConcat folds a string literal or a left-associative `+`-chain of
// string literals into its value. Anything else yields ok=false.
func foldStringConcat(n ast.Node) (string, bool) {
	switch v := n.(type) {
	case *ast.StringLit:
		return v.Val, true
	case *ast.BinaryExpr:
		if v.Op != "+" {
			return "", false
		}
		l, ok := foldStringConcat(v.Left)
		if !ok {
			return "", false
		}
		r, ok := foldStringConcat(v.Right)
		if !ok {
			return "", false
		}
		return l + r, true
	default:
		return "", false
	}
}
