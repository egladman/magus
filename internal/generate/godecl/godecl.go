// Package godecl reads declarations out of Go source, for the generators whose
// source of truth is a Go file.
//
// It is the reading half of the generator pipeline; [github.com/egladman/magus/internal/generate/emit]
// is the writing half. Only the GENERIC readers live here - a struct's tags, a doc
// line, a slice-of-struct literal, a file's flag bindings. Domain walkers that know
// what the declaration MEANS (config's flag/env/yaml derivation, for one) stay beside
// the schema they interpret, because their output type is domain-specific and moving
// them here would drag that vocabulary along.
//
// The reason it exists as a package at all: three copies of this parsing had already
// diverged. config.go walked struct tags, completions.go read a slice literal, and a
// test grew a third flag-binding scanner that read only ONE function - so it reported
// three false positives for every real finding, because magus binds flags across
// several functions and files. One tested reader is the fix.
//
// What does NOT belong here: a reader that looks generic but encodes one domain's
// meaning. config's type reader was extracted into this package and reverted, because
// it deliberately keeps the star on *bool, the package on time.Duration and the
// brackets on []string - config's flag-kind switch turns on exactly those, and a
// "clean" reader returning the bare identifier silently changed 148 lines of generated
// output. If a reader's callers would disagree about its result, it is a domain
// classifier wearing a generic name; leave it with its domain.
//
// Build-time only. Nothing in the magus binary imports it; go/ast has no place in a
// task runner at runtime.
package godecl

import (
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
)

// Parse reads path into an AST. Callers that need several readers over one file
// should Parse once and pass the result, rather than re-reading per reader.
func Parse(path string) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
}

// Tag returns the value of key in a struct tag, without options.
//
// `json:"size_mb,omitempty"` under key "json" yields "size_mb". Use [TagRaw] when
// the options matter.
func Tag(raw, key string) string {
	return strings.Split(TagRaw(raw, key), ",")[0]
}

// TagRaw returns the full value of key in a struct tag, options included.
func TagRaw(raw, key string) string {
	return reflect.StructTag(strings.Trim(raw, "`")).Get(key)
}

// DocLine returns the first sentence-ish line of a doc comment, flattened to one
// line with the leading slashes and the declared name stripped.
//
// Generators put this in a --help string or a generated comment, where a multi-line
// doc would break the output it is embedded in.
func DocLine(cg *ast.CommentGroup, declName string) string {
	if cg == nil {
		return ""
	}
	for _, c := range cg.List {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(c.Text, "//"), "/*"))
		if line == "" {
			continue
		}
		// Go doc convention opens with the declared name; a flag usage string reads
		// better without it ("Cache dir overrides..." -> "overrides...").
		return strings.TrimSpace(strings.TrimPrefix(line, declName))
	}
	return ""
}


// StructLiteral is one entry of a slice-of-struct declaration: its field names mapped
// to their string values. Only string-valued fields are captured, which is all a
// generator reading a declaration table needs.
type StructLiteral map[string]string

// SliceOfStructs reads `var <name> = []T{{Field: "v", ...}, ...}` from file.
//
// This is the shape a declaration table takes in Go - the list of subcommands, of
// modules, of diagnostics - and reading it from the AST rather than by matching text
// means a reformatted, reordered or re-commented table still parses.
func SliceOfStructs(file *ast.File, name string) []StructLiteral {
	var out []StructLiteral
	ast.Inspect(file, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) == 0 || vs.Names[0].Name != name || len(vs.Values) == 0 {
			return true
		}
		lit, ok := vs.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, el := range lit.Elts {
			entry, ok := el.(*ast.CompositeLit)
			if !ok {
				continue
			}
			fields := StructLiteral{}
			for _, f := range entry.Elts {
				kv, ok := f.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, kok := kv.Key.(*ast.Ident)
				val, vok := kv.Value.(*ast.BasicLit)
				if kok && vok && val.Kind == token.STRING {
					fields[key.Name] = strings.Trim(val.Value, `"`)
				}
			}
			if len(fields) > 0 {
				out = append(out, fields)
			}
		}
		return false
	})
	return out
}

// flagBinders are the flag.FlagSet methods that name a flag in their first argument.
var flagBinders = map[string]bool{
	"Bool": true, "BoolVar": true,
	"String": true, "StringVar": true,
	"Int": true, "IntVar": true,
	"Int64": true, "Int64Var": true,
	"Uint": true, "UintVar": true,
	"Float64": true, "Float64Var": true,
	"Duration": true, "DurationVar": true,
	"Func": true, "Var": true,
}

// FlagNames returns every flag name bound on a *flag.FlagSet anywhere in file.
//
// WHOLE file, every function, deliberately. The scanner this replaces read a single
// named function, which made it report a flag as nonexistent whenever the binding
// lived in a sibling function - three false positives for every real finding. A
// reader that is wrong three times out of four teaches people to ignore it.
//
// The receiver is not checked against a type: matching any x.Bool("name", ...) would
// catch slog.Bool and similar, so the receiver must be spelled `fs`, which is the
// convention every FlagSet in this repo follows.
func FlagNames(file *ast.File) []string {
	var out []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !flagBinders[sel.Sel.Name] {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "fs" {
			return true
		}
		// Var/Func and the *Var forms take the destination first, so the name is the
		// first STRING argument rather than the first argument.
		for _, arg := range call.Args {
			if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out = append(out, strings.Trim(lit.Value, `"`))
				break
			}
		}
		return true
	})
	return out
}
