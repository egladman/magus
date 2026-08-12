package buzz

import (
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
	vmpackage "github.com/egladman/magus/libs/gopherbuzz/vm"
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

// zdefStructDecls returns a synthesized object declaration for every `extern
// struct` a top-level zdef declares.
//
// A zdef struct used to bind only a {size, align, offsets} LAYOUT, so `Data` was a
// value and not a type: `Data{ id = 1 }` failed with "undefined type", and
// `typeof Data` had nothing to name. Synthesizing an ast.ObjectDecl hands the
// struct to the machinery that already exists - the checker registers it as an
// ObjectType, and the compiler resolves the literal through c.typeDecls exactly as
// it does for a hand-written `object`. Nothing about object literals had to change.
//
// The instance is an ordinary Buzz object. It becomes C memory only at the FFI
// boundary, where a struct-pointer parameter marshals it into a block and reads it
// back (see ffi_purego.go). That keeps raw pointers out of ordinary field syntax.
func zdefStructDecls(expr ast.Node) []*ast.ObjectDecl {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return nil
	}
	id, ok := call.Callee.(*ast.IdentExpr)
	if !ok || id.Name != "zdef" || len(call.Args) < 2 {
		return nil
	}
	decls, ok := foldStringConcat(call.Args[1])
	if !ok {
		return nil
	}
	structs := vmpackage.FFIStructDecls(decls)
	if len(structs) == 0 {
		return nil
	}
	out := make([]*ast.ObjectDecl, 0, len(structs))
	for _, s := range structs {
		od := &ast.ObjectDecl{Pos: ast.NodePos(expr), Name: s.Name}
		for i, fname := range s.FieldNames {
			annot := ""
			if i < len(s.FieldTypeNames) {
				annot = buzzTypeForCType(s.FieldTypeNames[i])
			}
			od.Fields = append(od.Fields, ast.ObjField{Name: fname, TypeAnnot: annot})
		}
		out = append(out, od)
	}
	return out
}

// buzzTypeForCType maps a C/Zig field spelling onto the Buzz type a script sees.
// An unrecognized spelling yields "" (unannotated), which reads as Unknown in the
// checker - the tracking-failure sentinel, so an exotic field stays usable rather
// than becoming a hard error.
func buzzTypeForCType(ct string) string {
	switch ct {
	case "bool":
		return "bool"
	case "f32", "f64":
		return "double"
	case "i8", "u8", "i16", "u16", "i32", "u32", "i64", "u64",
		"c_int", "c_uint", "c_long", "c_ulong", "c_longlong", "c_ulonglong",
		"usize", "isize":
		return "int"
	}
	// A pointer. `[*:0]u8` and friends are what ffi\cstr produces and what a
	// foreign function hands back, which a script reads as a str.
	if strings.Contains(ct, "*") {
		return "str"
	}
	return ""
}
