package main

//go:generate go run . contract -src ../../internal/spell/contract.go -harness ../../spells/harness.go -out ../../internal/interp/bindings/gen/decode/contract.go

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"github.com/egladman/magus/types"
)

// contractFunc is one field of internal/spell.Contract: the mgs_ function a spell exports
// and the Buzz return type the checker must see on it.
type contractFunc struct {
	Name    string
	Field   string
	Returns string
	// Params is the Buzz parameter list of a func-typed field, "" for a load-time function.
	Params   string
	Required bool
	Handler  bool
	Doc      string
	// Decoded is the Go type the generated decoder returns: the field's type, with a
	// handler field holding what each handler returned.
	Decoded reflect.Type
}

// runContract renders the spell contract's signature table into the decode package
// (-out) and its reference page (-docs), from the Contract struct in internal/spell.
//
// It PARSES that file rather than importing internal/spell: internal/spell imports the
// decode package this writes, so linking it here would let a stale decode file stop the
// generator that repairs it from building.
func runContract(args []string) error {
	fs := flag.NewFlagSet("contract", flag.ExitOnError)
	srcPath := fs.String("src", "", "path to internal/spell/contract.go")
	harnessPath := fs.String("harness", "", "path to spells/harness.go, whose HarnessContract becomes decode.HarnessFuncs")
	outPath := fs.String("out", "", "output Go file path for the signature table")
	docsPath := fs.String("docs", "", "output Markdown path for the reference page")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *srcPath == "" || *outPath == "" && *docsPath == "" || *outPath != "" && *harnessPath == "" {
		return fmt.Errorf("usage: magus-utils contract -src <contract.go> [-harness <harness.go> -out <contract.go>] [-docs <page.md>]")
	}
	funcs, err := readContract(*srcPath, "Contract")
	if err != nil {
		return err
	}
	if *outPath != "" {
		harness, err := readContract(*harnessPath, "HarnessContract")
		if err != nil {
			return err
		}
		src, err := renderContractTable(funcs, harness)
		if err != nil {
			return err
		}
		if err := emit.File(*outPath, src); err != nil {
			return err
		}
	}
	if *docsPath != "" {
		if err := emit.File(*docsPath, renderContractDocs(funcs)); err != nil {
			return err
		}
	}
	return nil
}

// readContract parses the struct named structName out of path and resolves each field's
// Go type to the Buzz type its mgs_ function returns. A func-typed field is a function
// called with arguments: its parameters become Params and its one result the return.
func readContract(path, structName string) ([]contractFunc, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	imports := map[string]string{}
	for _, imp := range file.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		name := p[strings.LastIndex(p, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		imports[name] = p
	}
	st, err := contractStruct(file, structName)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	var out []contractFunc
	seen := map[string]bool{}
	for _, field := range st.Fields.List {
		pos := fset.Position(field.Pos())
		if len(field.Names) != 1 {
			return nil, fmt.Errorf("%s: Contract fields are declared one per line", pos)
		}
		fieldName := field.Names[0].Name
		if field.Tag == nil {
			return nil, fmt.Errorf("%s: Contract.%s has no mgs tag", pos, fieldName)
		}
		tag, _ := strconv.Unquote(field.Tag.Value)
		name, opts, _ := strings.Cut(reflect.StructTag(tag).Get("mgs"), ",")
		if !strings.HasPrefix(name, "mgs_") {
			return nil, fmt.Errorf("%s: Contract.%s: mgs tag %q does not name an mgs_ function", pos, fieldName, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("%s: Contract.%s: %s is declared twice", pos, fieldName, name)
		}
		seen[name] = true
		fn := contractFunc{Name: name, Field: fieldName, Doc: strings.Join(strings.Fields(field.Doc.Text()), " ")}
		handler := false
		for _, opt := range strings.Split(opts, ",") {
			switch opt {
			case "":
			case "required":
				fn.Required = true
			case "handler":
				handler = true
			default:
				return nil, fmt.Errorf("%s: Contract.%s: unknown mgs tag option %q", pos, fieldName, opt)
			}
		}
		typeExpr := field.Type
		if ft, ok := typeExpr.(*ast.FuncType); ok {
			if handler {
				return nil, fmt.Errorf("%s: %s.%s: a func field cannot be a handler", pos, structName, fieldName)
			}
			if typeExpr, fn.Params, err = contractFuncSignature(ft, imports); err != nil {
				return nil, fmt.Errorf("%s: %s.%s: %w", pos, structName, fieldName, err)
			}
		}
		rt, err := contractFieldType(typeExpr, imports)
		if err != nil {
			return nil, fmt.Errorf("%s: Contract.%s: %w", pos, fieldName, err)
		}
		fn.Returns, err = contractReturn(rt, handler)
		if err != nil {
			return nil, fmt.Errorf("%s: Contract.%s: %w", pos, fieldName, err)
		}
		fn.Handler, fn.Decoded = handler, rt
		out = append(out, fn)
	}
	return out, nil
}

func contractStruct(file *ast.File, name string) (*ast.StructType, error) {
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return nil, fmt.Errorf("type %s is not a struct", name)
			}
			return st, nil
		}
	}
	return nil, fmt.Errorf("no %s struct", name)
}

// contractFuncSignature renders a func field's parameters as the Buzz parameter list its
// export declares, and returns the expression of its one result. Parameter names are
// part of the declaration, so each must be named.
func contractFuncSignature(ft *ast.FuncType, imports map[string]string) (ast.Expr, string, error) {
	if ft.Results == nil || len(ft.Results.List) != 1 || len(ft.Results.List[0].Names) > 1 {
		return nil, "", fmt.Errorf("a func field returns exactly one value")
	}
	var params []string
	for _, p := range ft.Params.List {
		if len(p.Names) == 0 {
			return nil, "", fmt.Errorf("a func field names each parameter")
		}
		rt, err := contractFieldType(p.Type, imports)
		if err != nil {
			return nil, "", err
		}
		buzzType, _, err := buzzgen.FieldType(rt, mirrorOptions())
		if err != nil {
			return nil, "", err
		}
		for _, n := range p.Names {
			params = append(params, n.Name+": "+buzzType)
		}
	}
	return ft.Results.List[0].Type, strings.Join(params, ", "), nil
}

// contractFieldType turns a field's type expression into the reflect.Type it names, so
// buzzgen can name it exactly as the mirrors do. A named type must be a registered
// boundary type or enum; the registry is what maps the Go name to the Buzz one.
func contractFieldType(expr ast.Expr, imports map[string]string) (reflect.Type, error) {
	switch x := expr.(type) {
	case *ast.Ident:
		switch x.Name {
		case "string":
			return reflect.TypeFor[string](), nil
		case "bool":
			return reflect.TypeFor[bool](), nil
		case "int":
			return reflect.TypeFor[int](), nil
		case "float64":
			return reflect.TypeFor[float64](), nil
		}
		return nil, fmt.Errorf("unsupported type %s", x.Name)
	case *ast.StarExpr:
		elem, err := contractFieldType(x.X, imports)
		if err != nil {
			return nil, err
		}
		return reflect.PointerTo(elem), nil
	case *ast.ArrayType:
		if x.Len != nil {
			return nil, fmt.Errorf("an array has no Buzz type, use a slice")
		}
		elem, err := contractFieldType(x.Elt, imports)
		if err != nil {
			return nil, err
		}
		return reflect.SliceOf(elem), nil
	case *ast.MapType:
		key, err := contractFieldType(x.Key, imports)
		if err != nil {
			return nil, err
		}
		val, err := contractFieldType(x.Value, imports)
		if err != nil {
			return nil, err
		}
		return reflect.MapOf(key, val), nil
	case *ast.SelectorExpr:
		pkg, ok := x.X.(*ast.Ident)
		if !ok {
			return nil, fmt.Errorf("unsupported type expression")
		}
		pkgPath := imports[pkg.Name]
		for _, entry := range boundaryTypes {
			if entry.Type.PkgPath() == pkgPath && entry.Type.Name() == x.Sel.Name {
				return entry.Type, nil
			}
		}
		for _, en := range boundaryEnums {
			if en.Type.PkgPath() == pkgPath && en.Type.Name() == x.Sel.Name {
				return en.Type, nil
			}
		}
		return nil, fmt.Errorf("%s.%s is not a registered boundary type (cmd/magus-utils/boundary_types.go)", pkg.Name, x.Sel.Name)
	}
	return nil, fmt.Errorf("unsupported type expression %T", expr)
}

// contractReturn names the Buzz return type of the function a field answers for. A
// pointer is a function a spell may omit, so it returns the element type; a handler map
// holds functions from Target to the element type.
func contractReturn(rt reflect.Type, handler bool) (string, error) {
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if !handler {
		name, _, err := buzzgen.FieldType(rt, mirrorOptions())
		return name, err
	}
	if rt.Kind() != reflect.Map || rt.Key().Kind() != reflect.String {
		return "", fmt.Errorf("a handler field is a map from op name to what the handler returns, not %s", rt)
	}
	elem, _, err := buzzgen.FieldType(rt.Elem(), mirrorOptions())
	if err != nil {
		return "", err
	}
	return "{str: fun(" + buzzName(reflect.TypeFor[types.Target]()) + ") " + elem + "}", nil
}

func renderContractTable(funcs, harness []contractFunc) ([]byte, error) {
	entries, err := inboundBoundaryTypes()
	if err != nil {
		return nil, err
	}
	e := newDecodeEmitter(entries)
	var decoders bytes.Buffer
	for _, table := range []struct {
		prefix string
		funcs  []contractFunc
	}{{"decodeContract", funcs}, {"decodeHarnessFunc", harness}} {
		for _, fn := range table.funcs {
			fmt.Fprintf(&decoders, "func %s%s(v vm.Value) (%s, error) {\n", table.prefix, fn.Field, e.goType(fn.Decoded))
			fmt.Fprintf(&decoders, "\tvar out %s\n", e.goType(fn.Decoded))
			if err := e.assign(&decoders, "v", strconv.Quote(fn.Name), "out", fn.Field, fn.Decoded, "\t"); err != nil {
				return nil, fmt.Errorf("%s: %w", fn.Name, err)
			}
			fmt.Fprintln(&decoders, "\treturn out, nil")
			fmt.Fprintln(&decoders, "}")
			fmt.Fprintln(&decoders)
		}
	}
	if len(e.enums) > 0 {
		return nil, fmt.Errorf("a Contract field reaches an enum directly, which the contract decoders do not render")
	}

	var b bytes.Buffer
	fmt.Fprintln(&b, "// Code generated by magus-utils contract. DO NOT EDIT.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "package decode")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "import (")
	fmt.Fprintln(&b, `	"fmt"`)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `	vm "github.com/egladman/magus/libs/gopherbuzz/vm"`)
	if e.pkgs["spells"] {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/spells"`)
	}
	if e.pkgs["types"] {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/types"`)
	}
	fmt.Fprintln(&b, ")")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "// ContractFunc is one load-time function of the spell contract, generated from a field")
	fmt.Fprintln(&b, "// of internal/spell.Contract.")
	fmt.Fprintln(&b, "type ContractFunc struct {")
	fmt.Fprintln(&b, "\t// Name is the mgs_ function a spell exports.")
	fmt.Fprintln(&b, "\tName string")
	fmt.Fprintln(&b, "\t// Field is the Contract field its value decodes into.")
	fmt.Fprintln(&b, "\tField string")
	fmt.Fprintln(&b, "\t// Returns is the return annotation the function must declare, as Buzz source spells")
	fmt.Fprintln(&b, "\t// it. A spell may space it differently.")
	fmt.Fprintln(&b, "\tReturns string")
	fmt.Fprintln(&b, "\t// Params is the parameter list the function must declare. A load-time function takes none.")
	fmt.Fprintln(&b, "\tParams string")
	fmt.Fprintln(&b, "\t// Required marks the function a module must export to be a spell at all.")
	fmt.Fprintln(&b, "\tRequired bool")
	fmt.Fprintln(&b, "\t// Handler marks a {str: fun(Target) T} map: the caller invokes each handler once with a")
	fmt.Fprintln(&b, "\t// null Target and passes Decode the map of what they returned.")
	fmt.Fprintln(&b, "\tHandler bool")
	fmt.Fprintln(&b, "\t// Decode reads the function's result strictly into the Go type of the Contract field,")
	fmt.Fprintln(&b, "\t// naming the field path of any mismatch.")
	fmt.Fprintln(&b, "\tDecode func(v vm.Value) (any, error)")
	fmt.Fprintln(&b, "}")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "// ContractFuncs is every load-time function of the spell contract, in the order")
	fmt.Fprintln(&b, "// internal/spell.Contract declares them.")
	fmt.Fprintln(&b, "var ContractFuncs = []ContractFunc{")
	for _, fn := range funcs {
		fmt.Fprintf(&b, "\t{Name: %q, Field: %q, Returns: %q, Required: %t, Handler: %t, Decode: func(v vm.Value) (any, error) { return decodeContract%s(v) }},\n",
			fn.Name, fn.Field, fn.Returns, fn.Required, fn.Handler, fn.Field)
	}
	fmt.Fprintln(&b, "}")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "// HarnessFuncs is every function of the harness contract, in the order")
	fmt.Fprintln(&b, "// spells.HarnessContract declares them. Required marks the one a harness must export.")
	fmt.Fprintln(&b, "var HarnessFuncs = []ContractFunc{")
	for _, fn := range harness {
		fmt.Fprintf(&b, "\t{Name: %q, Field: %q, Returns: %q, Params: %q, Required: %t, Decode: func(v vm.Value) (any, error) { return decodeHarnessFunc%s(v) }},\n",
			fn.Name, fn.Field, fn.Returns, fn.Params, fn.Required, fn.Field)
	}
	fmt.Fprintln(&b, "}")
	fmt.Fprintln(&b)
	b.Write(decoders.Bytes())
	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, gofmtError(err, b.String())
	}
	return out, nil
}

func renderContractDocs(funcs []contractFunc) []byte {
	var b bytes.Buffer
	fmt.Fprintln(&b, "---")
	fmt.Fprintln(&b, "title: Spell contract")
	fmt.Fprintln(&b, "generated_from: internal/spell/contract.go")
	fmt.Fprintln(&b, "description: Every mgs_ function a spell may export, with the return type magus checks it against. Generated from the spell contract.")
	fmt.Fprintln(&b, "tags: [spells, contract, mgs_, reference, MGS1051]")
	fmt.Fprintln(&b, "---")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "# Spell contract")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "A spell answers magus through exported `mgs_` functions. magus calls each one once, with no arguments, when it loads the spell, before any target runs. Each function must declare exactly the return type below; a different one, `> any`, or an `mgs_` name this page does not list is [MGS1051](codes/magusfile/MGS1051.md).")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Only `mgs_getName` is required. Leaving any other function out declares nothing for it.")
	for _, fn := range funcs {
		fmt.Fprintln(&b)
		fmt.Fprintf(&b, "## %s\n", fn.Name)
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, "```buzz")
		fmt.Fprintf(&b, "export fun %s() > %s\n", fn.Name, fn.Returns)
		fmt.Fprintln(&b, "```")
		fmt.Fprintln(&b)
		if fn.Required {
			fmt.Fprint(&b, "Required. ")
		}
		fmt.Fprintln(&b, fn.Doc)
	}
	return b.Bytes()
}
