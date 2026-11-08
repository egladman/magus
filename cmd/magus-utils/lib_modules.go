package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"github.com/egladman/magus/internal/generate/emit"
)

// runLibModules writes one std leaf package per Go package of a library
// module, adapting its exported API to Buzz. The Buzz import path is the
// module's last path element, then the package's directory under it.
func runLibModules(args []string) error {
	fs := flag.NewFlagSet("libmodules", flag.ExitOnError)
	dir := fs.String("dir", "", "the library module's directory, holding its go.mod")
	outDir := fs.String("outdir", "", "directory the generated leaf packages go under")
	genImport := fs.String("import", "", "Go import path of -outdir")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dir == "" || *outDir == "" || *genImport == "" {
		return errors.New("usage: magus-utils libmodules -dir <module> -outdir <dir> -import <path>")
	}
	lib, err := loadLib(*dir, *genImport)
	if err != nil {
		return err
	}
	if gaps := lib.gaps(); len(gaps) > 0 {
		return fmt.Errorf("no Buzz binding for: %s", strings.Join(gaps, ", "))
	}
	for _, p := range lib.pkgs {
		src, err := lib.emitPackage(p)
		if err != nil {
			return fmt.Errorf("%s: %w", p.importPath, err)
		}
		if err := os.MkdirAll(filepath.Join(*outDir, p.genName), 0o755); err != nil {
			return err
		}
		if err := emit.File(filepath.Join(*outDir, p.genName, "module.go"), src); err != nil {
			return err
		}
	}
	reg, err := lib.emitRegister(false)
	if err != nil {
		return err
	}
	if err := emit.File(filepath.Join(*outDir, "register.go"), reg); err != nil {
		return err
	}
	wasm, err := lib.emitRegister(true)
	if err != nil {
		return err
	}
	return emit.File(filepath.Join(*outDir, "register_wasm.go"), wasm)
}

// libModule is one parsed library: every non-internal package in it.
type libModule struct {
	modPath   string
	base      string
	genImport string
	pkgs      []*libPkg
	byPath    map[string]*libPkg
}

type libPkg struct {
	importPath string
	name       string
	// buzzName is what a script calls the module by; buzzPath its import line.
	buzzName, buzzPath string
	// genName is the generated leaf package's directory and name.
	genName string
	doc     string
	types   map[string]*libType
	funcs   []*libFunc
	values  []*libValue
}

type typeKind int

const (
	kindRecord typeKind = iota + 1
	kindHandle
	kindBasic
	kindIface
)

type libType struct {
	pkg    *libPkg
	name   string
	kind   typeKind
	basic  string
	fields []libField
	// methods are interface methods, for an iface type.
	methods []*libFunc
	file    *libFile
}

type libField struct {
	name string
	typ  ast.Expr
}

type libFile struct {
	pkg     *libPkg
	imports map[string]string
}

type libFunc struct {
	name    string
	recv    *libType
	params  []libParam
	results []ast.Expr
	doc     string
	file    *libFile
}

type libParam struct {
	name     string
	typ      ast.Expr
	variadic bool
}

type libValue struct {
	name string
	typ  ast.Expr
	doc  string
	file *libFile
	// typeName is the named type of an untyped const in a typed group.
	typeName string
	// lit is the token of an untyped const's literal value.
	lit token.Token
}

func loadLib(dir, genImport string) (*libModule, error) {
	gomod, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	modPath := ""
	for _, line := range strings.Split(string(gomod), "\n") {
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			modPath = strings.TrimSpace(rest)
		}
	}
	if modPath == "" {
		return nil, fmt.Errorf("%s/go.mod names no module", dir)
	}
	lib := &libModule{modPath: modPath, base: path.Base(modPath), genImport: genImport, byPath: map[string]*libPkg{}}
	err = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel == "internal" || strings.HasPrefix(rel, "internal/") || strings.Contains(rel, "testdata") {
			return filepath.SkipDir
		}
		pkg, err := lib.parseDir(p, rel)
		if errors.Is(err, errNoGoFiles) {
			return nil
		}
		if err != nil {
			return err
		}
		lib.pkgs = append(lib.pkgs, pkg)
		lib.byPath[pkg.importPath] = pkg
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lib, nil
}

// errNoGoFiles reports a directory with no Go source, so no package.
var errNoGoFiles = errors.New("no Go files")

func (lib *libModule) parseDir(dir, rel string) (*libPkg, error) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []*ast.File
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, errNoGoFiles
	}
	p := &libPkg{importPath: lib.modPath, name: files[0].Name.Name, types: map[string]*libType{}}
	p.buzzName, p.buzzPath, p.genName = lib.base, lib.base, lib.base
	if rel != "." {
		p.importPath += "/" + rel
		p.buzzName = path.Base(rel)
		p.buzzPath = path.Join(lib.base, rel)
		p.genName = p.buzzName
	}
	for _, f := range files {
		lf := &libFile{pkg: p, imports: map[string]string{}}
		for _, im := range f.Imports {
			ip := strings.Trim(im.Path.Value, `"`)
			name := path.Base(ip)
			if im.Name != nil {
				name = im.Name.Name
			}
			lf.imports[name] = ip
		}
		if f.Doc != nil && p.doc == "" {
			p.doc = f.Doc.Text()
		}
		for _, decl := range f.Decls {
			p.collect(decl, lf)
		}
	}
	return p, nil
}

func (p *libPkg) collect(decl ast.Decl, lf *libFile) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if !d.Name.IsExported() {
			return
		}
		fn := &libFunc{name: d.Name.Name, doc: d.Doc.Text(), file: lf}
		fn.params, fn.results = signature(d.Type)
		if d.Recv != nil {
			name := recvName(d.Recv.List[0].Type)
			if !ast.IsExported(name) {
				return
			}
			fn.recv = p.typeNamed(name)
		}
		p.funcs = append(p.funcs, fn)
	case *ast.GenDecl:
		p.collectGen(d, lf)
	}
}

func (p *libPkg) collectGen(d *ast.GenDecl, lf *libFile) {
	lastType := ""
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			t := p.typeNamed(s.Name.Name)
			t.file = lf
			p.describeType(t, s.Type, lf)
		case *ast.ValueSpec:
			if s.Type != nil {
				lastType = exprString(s.Type)
			} else if len(s.Values) > 0 {
				lastType = ""
			}
			for i, n := range s.Names {
				if !n.IsExported() {
					continue
				}
				v := &libValue{name: n.Name, typ: s.Type, file: lf, typeName: lastType}
				switch {
				case s.Doc != nil:
					v.doc = s.Doc.Text()
				case d.Doc != nil:
					v.doc = d.Doc.Text()
				}
				if s.Type == nil && len(s.Values) > i {
					if d.Tok == token.VAR {
						v.typ = varType(s.Values[i])
					} else if lit, ok := s.Values[i].(*ast.BasicLit); ok {
						v.lit = lit.Kind
					}
				}
				p.values = append(p.values, v)
			}
		}
	}
}

// varType reads an untyped var's type off its initializer.
func varType(e ast.Expr) ast.Expr {
	switch v := e.(type) {
	case *ast.CompositeLit:
		return v.Type
	case *ast.CallExpr:
		if sel, ok := v.Fun.(*ast.SelectorExpr); ok && exprString(sel) == "errors.New" {
			return ast.NewIdent("error")
		}
	}
	return nil
}

func (p *libPkg) typeNamed(name string) *libType {
	t, ok := p.types[name]
	if !ok {
		t = &libType{pkg: p, name: name}
		p.types[name] = t
	}
	return t
}

// describeType sorts a struct into a record, when every field is exported, or
// a handle, when it keeps state a script must not see.
func (p *libPkg) describeType(t *libType, e ast.Expr, lf *libFile) {
	switch v := e.(type) {
	case *ast.StructType:
		t.kind = kindRecord
		for _, f := range v.Fields.List {
			if len(f.Names) == 0 {
				t.kind = kindHandle
				continue
			}
			for _, n := range f.Names {
				if !n.IsExported() {
					t.kind = kindHandle
					continue
				}
				t.fields = append(t.fields, libField{name: n.Name, typ: f.Type})
			}
		}
	case *ast.InterfaceType:
		t.kind = kindIface
		for _, m := range v.Methods.List {
			ft, ok := m.Type.(*ast.FuncType)
			if !ok || len(m.Names) == 0 {
				continue
			}
			fn := &libFunc{name: m.Names[0].Name, recv: t, doc: m.Doc.Text(), file: lf}
			fn.params, fn.results = signature(ft)
			t.methods = append(t.methods, fn)
		}
	case *ast.Ident:
		t.kind = kindBasic
		t.basic = v.Name
	}
}

func recvName(e ast.Expr) string {
	if s, ok := e.(*ast.StarExpr); ok {
		e = s.X
	}
	if id, ok := e.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

func signature(ft *ast.FuncType) ([]libParam, []ast.Expr) {
	var params []libParam
	for _, f := range ft.Params.List {
		typ, variadic := f.Type, false
		if el, ok := typ.(*ast.Ellipsis); ok {
			typ, variadic = &ast.ArrayType{Elt: el.Elt}, true
		}
		if len(f.Names) == 0 {
			params = append(params, libParam{name: fmt.Sprintf("arg%d", len(params)), typ: typ, variadic: variadic})
		}
		for _, n := range f.Names {
			params = append(params, libParam{name: n.Name, typ: typ, variadic: variadic})
		}
	}
	var results []ast.Expr
	if ft.Results != nil {
		for _, f := range ft.Results.List {
			for range max(len(f.Names), 1) {
				results = append(results, f.Type)
			}
		}
	}
	return params, results
}

func exprString(e ast.Expr) string {
	var b bytes.Buffer
	_ = format.Node(&b, token.NewFileSet(), e)
	return b.String()
}

// conv is how one Go type crosses into Buzz.
type conv struct {
	kind convKind
	// t is the library type, for a named conv.
	t    *libType
	elem *conv
	// prim is the Go basic type of a prim conv.
	prim string
}

type convKind int

const (
	convPrim convKind = iota + 1
	convDuration
	convContext
	convStderr
	convStdin
	convError
	convNamed
	convSlice
	convBytes
)

// resolve maps a type expression in lf's file to its conv, or explains why it
// cannot cross.
func (lib *libModule) resolve(e ast.Expr, lf *libFile) (*conv, error) {
	switch v := e.(type) {
	case *ast.Ident:
		switch v.Name {
		case "string", "int", "bool", "float64", "rune", "uintptr", "int64", "byte":
			return &conv{kind: convPrim, prim: v.Name}, nil
		case "error":
			return &conv{kind: convError}, nil
		}
		if t, ok := lf.pkg.types[v.Name]; ok {
			return lib.named(t)
		}
	case *ast.StarExpr:
		if exprString(v.X) == "os.File" {
			return &conv{kind: convStdin}, nil
		}
		c, err := lib.resolve(v.X, lf)
		if err != nil {
			return nil, err
		}
		if c.kind == convNamed && c.t.kind == kindHandle {
			return c, nil
		}
	case *ast.SelectorExpr:
		x, _ := v.X.(*ast.Ident)
		if x == nil {
			break
		}
		switch exprString(v) {
		case "time.Duration":
			return &conv{kind: convDuration}, nil
		case "context.Context":
			return &conv{kind: convContext}, nil
		case "io.Writer":
			return &conv{kind: convStderr}, nil
		}
		if p, ok := lib.byPath[lf.imports[x.Name]]; ok {
			if t, ok := p.types[v.Sel.Name]; ok {
				return lib.named(t)
			}
		}
	case *ast.ArrayType:
		if v.Len != nil {
			break
		}
		if id, ok := v.Elt.(*ast.Ident); ok && id.Name == "byte" {
			return &conv{kind: convBytes}, nil
		}
		el, err := lib.resolve(v.Elt, lf)
		if err != nil {
			return nil, err
		}
		return &conv{kind: convSlice, elem: el}, nil
	}
	return nil, fmt.Errorf("type %s has no Buzz form", exprString(e))
}

func (lib *libModule) named(t *libType) (*conv, error) {
	if t.kind == 0 {
		return nil, fmt.Errorf("type %s.%s has no Buzz form", t.pkg.name, t.name)
	}
	return &conv{kind: convNamed, t: t}, nil
}

// gaps lists every exported function, method, type or value with no binding.
// A function-typed record field is not one: a script cannot hand Go a callback
// through a map, so the field stays unset.
func (lib *libModule) gaps() []string {
	var gaps []string
	for _, p := range lib.pkgs {
		for _, t := range sortedTypes(p) {
			if t.kind == 0 {
				gaps = append(gaps, fmt.Sprintf("%s.%s: a type of this shape has no Buzz form", p.importPath, t.name))
			}
			if t.kind != kindRecord && t.kind != kindHandle {
				continue
			}
			for _, f := range t.fields {
				if _, err := lib.resolve(f.typ, t.file); err != nil && !isFunc(f.typ) {
					gaps = append(gaps, fmt.Sprintf("%s.%s.%s: %v", p.importPath, t.name, f.name, err))
				}
			}
		}
		for _, fn := range lib.allFuncs(p) {
			if err := lib.checkFunc(fn); err != nil {
				gaps = append(gaps, fmt.Sprintf("%s.%s: %v", p.importPath, fn.qualified(), err))
			}
		}
		for _, v := range p.values {
			if _, err := lib.valueConv(v); err != nil {
				gaps = append(gaps, fmt.Sprintf("%s.%s: %v", p.importPath, v.name, err))
			}
		}
	}
	return gaps
}

func isFunc(e ast.Expr) bool {
	_, ok := e.(*ast.FuncType)
	return ok
}

func (fn *libFunc) qualified() string {
	if fn.recv != nil {
		return fmt.Sprintf("%s.%s", fn.recv.name, fn.name)
	}
	return fn.name
}

func (lib *libModule) allFuncs(p *libPkg) []*libFunc {
	fns := slices.Clone(p.funcs)
	for _, t := range sortedTypes(p) {
		fns = append(fns, t.methods...)
	}
	return fns
}

func sortedTypes(p *libPkg) []*libType {
	ts := make([]*libType, 0, len(p.types))
	for _, t := range p.types {
		ts = append(ts, t)
	}
	slices.SortFunc(ts, func(a, b *libType) int { return strings.Compare(a.name, b.name) })
	return ts
}

func (lib *libModule) checkFunc(fn *libFunc) error {
	for _, prm := range fn.params {
		c, err := lib.resolve(prm.typ, fn.file)
		if err != nil {
			return fmt.Errorf("parameter %s: %w", prm.name, err)
		}
		if c.kind == convError {
			return fmt.Errorf("parameter %s: an error cannot be passed in", prm.name)
		}
	}
	for i, r := range fn.results {
		c, err := lib.resolve(r, fn.file)
		if err != nil {
			return fmt.Errorf("result %d: %w", i, err)
		}
		if c.kind == convError && i != len(fn.results)-1 {
			return fmt.Errorf("result %d: an error that is not last", i)
		}
		if c.kind == convContext || c.kind == convStderr || c.kind == convStdin {
			return fmt.Errorf("result %d: %s cannot be returned to Buzz", i, exprString(r))
		}
	}
	return nil
}

func (lib *libModule) valueConv(v *libValue) (*conv, error) {
	if v.typ != nil {
		return lib.resolve(v.typ, v.file)
	}
	if v.typeName != "" {
		return lib.resolve(ast.NewIdent(v.typeName), v.file)
	}
	switch v.lit {
	case token.STRING:
		return &conv{kind: convPrim, prim: "string"}, nil
	case token.INT:
		return &conv{kind: convPrim, prim: "int"}, nil
	}
	return nil, errors.New("an untyped value that is not a string or int literal")
}

// snake turns a Go identifier into a std method name: SGRBold to sgr_bold.
func snake(s string) string {
	var b strings.Builder
	r := []rune(s)
	for i, c := range r {
		if unicode.IsUpper(c) {
			lowerNext := i+1 < len(r) && unicode.IsLower(r[i+1])
			if i > 0 && (unicode.IsLower(r[i-1]) || unicode.IsDigit(r[i-1]) || lowerNext) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(c))
			continue
		}
		b.WriteRune(c)
	}
	return b.String()
}
