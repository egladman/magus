// Subcommand `output` generates schema/gen/outputs.go: a committed, pure-data
// descriptor of magus's CLI output types (the `*Output` structs in the types
// package) so downstream work can drive `-o json` / `-o template` field
// discovery and a knowledge-graph node kind from one generated source. It parses
// the types package with go/ast and never links into the binary.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func runOutput(args []string) error {
	fs := flag.NewFlagSet("output", flag.ExitOnError)
	typesDir := fs.String("types", "./types", "Path to the types package directory")
	outPath := fs.String("out", "./schema/gen/outputs.go", "Generated outputs descriptor path")
	if err := fs.Parse(args); err != nil {
		return err
	}

	structs, err := parseTypeStructs(*typesDir)
	if err != nil {
		return err
	}

	src, count, err := renderOutputs(structs)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, src, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *outPath, err)
	}
	fmt.Fprintf(os.Stderr, "output: wrote %d types to %s\n", count, *outPath)
	return nil
}

// parseTypeStructs parses every non-test .go file in dir into one map of struct
// name -> its declaration, so a type and the types it references can be resolved
// even when they live in different files of the package.
func parseTypeStructs(dir string) (map[string]*ast.StructType, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read types dir %s: %w", dir, err)
	}
	fset := token.NewFileSet()
	structs := map[string]*ast.StructType{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", name, err)
		}
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				structs[ts.Name.Name] = st
			}
		}
	}
	return structs, nil
}

// renderOutputs builds the descriptor: every `*Output` struct (Root=true) plus
// every struct type reachable from one transitively (Root=false), emitted in
// sorted key order and run through go/format.
func renderOutputs(structs map[string]*ast.StructType) ([]byte, int, error) {
	// Roots are the top-level *Output types a command emits. Collect the closure
	// of struct types reachable from any root via BFS.
	collected := map[string]bool{}
	var queue []string
	for name := range structs {
		if strings.HasSuffix(name, "Output") {
			collected[name] = true
			queue = append(queue, name)
		}
	}
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		st := structs[name]
		for _, field := range st.Fields.List {
			ref := referencedTypeName(field.Type)
			if ref == "" || collected[ref] {
				continue
			}
			if _, ok := structs[ref]; ok {
				collected[ref] = true
				queue = append(queue, ref)
			}
		}
	}

	names := make([]string, 0, len(collected))
	for name := range collected {
		names = append(names, name)
	}
	sort.Strings(names)

	var b bytes.Buffer
	b.WriteString(outputsHeader)
	for _, name := range names {
		emitOutputType(&b, name, structs[name])
	}
	b.WriteString("}\n")

	formatted, err := format.Source(b.Bytes())
	if err != nil {
		return nil, 0, fmt.Errorf("gofmt outputs.go: %w", err)
	}
	return formatted, len(names), nil
}

// emitOutputType writes one OutputTypes map entry for the named struct.
func emitOutputType(b *bytes.Buffer, name string, st *ast.StructType) {
	fmt.Fprintf(b, "\t%q: {\n", name)
	fmt.Fprintf(b, "\t\tName: %q,\n", name)
	fmt.Fprintf(b, "\t\tRoot: %t,\n", strings.HasSuffix(name, "Output"))

	type outField struct{ json, goName, typ, doc string }
	var fields []outField
	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue // embedded field
		}
		jsonTag := ""
		if field.Tag != nil {
			jsonTag = lookupTag(strings.Trim(field.Tag.Value, "`"), "json")
		}
		if jsonTag == "-" {
			continue // json omits it
		}
		typ := renderType(field.Type)
		doc := firstDocLine(field.Doc)
		for _, id := range field.Names {
			if !ast.IsExported(id.Name) {
				continue
			}
			jsonName := jsonTag
			if jsonName == "" {
				jsonName = id.Name // encoding/json default
			}
			fields = append(fields, outField{jsonName, id.Name, typ, doc})
		}
	}

	if len(fields) == 0 {
		b.WriteString("\t},\n")
		return
	}
	b.WriteString("\t\tFields: []OutputField{\n")
	for _, f := range fields {
		fmt.Fprintf(b, "\t\t\t{JSON: %q, Go: %q, Type: %q, Doc: %q},\n", f.json, f.goName, f.typ, f.doc)
	}
	b.WriteString("\t\t},\n\t},\n")
}

// referencedTypeName unwraps a field type to the single named type it references
// (through []T, *T, map[K]V value, and pkg.Sel), for deciding which local struct
// types to describe transitively. Returns "" for types with no such name.
func referencedTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return referencedTypeName(t.X)
	case *ast.ArrayType:
		return referencedTypeName(t.Elt)
	case *ast.MapType:
		return referencedTypeName(t.Value)
	case *ast.SelectorExpr:
		return t.Sel.Name
	default:
		return ""
	}
}

// renderType renders a field's Go type back to source text (e.g. "[]ProjectEntry",
// "map[string]int", "*time.Time", "any"). Best-effort for unexpected forms; never
// panics, since the result is stored as a quoted string.
func renderType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + renderType(t.X)
	case *ast.ArrayType:
		if t.Len != nil {
			return "[" + renderType(t.Len) + "]" + renderType(t.Elt)
		}
		return "[]" + renderType(t.Elt)
	case *ast.MapType:
		return "map[" + renderType(t.Key) + "]" + renderType(t.Value)
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			return x.Name + "." + t.Sel.Name
		}
		return renderType(t.X) + "." + t.Sel.Name
	case *ast.InterfaceType:
		if t.Methods == nil || len(t.Methods.List) == 0 {
			return "any"
		}
		return "interface{...}"
	case *ast.BasicLit:
		return t.Value
	default:
		return fmt.Sprintf("unsupported(%T)", expr)
	}
}

const outputsHeader = `// Code generated by magus-utils output; DO NOT EDIT.
package gen

// OutputField describes one field of a magus CLI output type.
type OutputField struct {
	JSON string // json-tag key = the -o json / -o template field name; falls back to the Go field name when there is no json tag
	Go   string // exported Go field name
	Type string // rendered Go type (e.g. "string", "[]ProjectEntry", "map[string]int")
	Doc  string // first line of the field's doc comment, "" if none
}

// OutputType is one output struct: a top-level *Output type a command emits
// (Root=true) or a struct one references transitively (Root=false).
type OutputType struct {
	Name   string // Go type name
	Root   bool   // true for a top-level *Output type a command emits
	Fields []OutputField
}

// OutputTypes is the generated inventory of magus's CLI output types, keyed by
// Go type name. Regenerate with: go generate ./cmd/magus/...
var OutputTypes = map[string]OutputType{
`
