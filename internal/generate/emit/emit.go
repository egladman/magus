// Package emit writes magus's generated artifacts: the last stage of every generator,
// after its source of truth has been read and its output rendered.
//
// "emit" is the compiler-pipeline term for this step (parse, analyze, emit), and it is
// deliberately narrower than a name like codegen: this package generates nothing, it
// WRITES what a generator produced, idempotently, and formatted when the artifact is
// Go. The reading half lives beside each source of truth.
//
// Build-time only. Nothing in the magus binary imports it and nothing should, so
// internal/ keeps it unimportable from outside the module, the right shape for code
// whose whole job is producing checked-in files.
//
// Note the directory vocabulary this fits into: a gen/ directory holds generated
// OUTPUT (there are ten of them in this repo), so generator code never lives in one.
package emit

import (
	"bytes"
	"errors"
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
	"text/template"
	"unicode"
)

// FileMode is the permission every generated file is written with. Generated output
// is source-adjacent and checked in, so it is readable and never executable.
const FileMode os.FileMode = 0o644

// File writes data to path only when the bytes differ.
//
// Skipping an identical write is not a micro-optimization: magus keys its cache on
// file content and mtime, so rewriting an unchanged generated file marks every
// downstream target dirty and re-runs work that had nothing to do. Two scribes
// bypassed this with a bare os.WriteFile before it lived here.
func File(path string, data []byte) error {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, data) {
		return nil
	}
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, data, FileMode)
}

// Go gofmts data before writing it.
//
// The format step is the guard that matters: a template that emits unparsable Go
// fails HERE, naming the file, rather than at the next build with an error pointing
// into generated output nobody wrote by hand.
func Go(path string, data []byte) error {
	formatted, err := format.Source(data)
	if err != nil {
		return fmt.Errorf("gofmt %s: %w", path, err)
	}
	return File(path, formatted)
}

// GoTemplate executes tmpl into path as gofmt'd Go source.
func GoTemplate(path string, tmpl *template.Template, data any) error {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("template %s: %w", path, err)
	}
	if err := Go(path, buf.Bytes()); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// Marker names a generated region inside an otherwise hand-maintained file.
//
// Some artifacts are mostly hand-written with one generated list in them: the shell
// completion scripts are four dialects of stable hand-written logic wrapped around a
// subcommand list that drifted. Owning the whole file would mean maintaining four
// shell templates to fix a problem that is entirely about one list.
type Marker struct {
	Begin string
	End   string
}

// CommentMarker builds a Marker from a comment prefix and a region name, e.g.
// CommentMarker("#", "subcommands") for shell.
func CommentMarker(comment, name string) Marker {
	return Marker{
		Begin: fmt.Sprintf("%s magus-utils:%s:begin", comment, name),
		End:   fmt.Sprintf("%s magus-utils:%s:end", comment, name),
	}
}

// Region swaps the lines between m.Begin and m.End for body, keeping both
// marker lines. Indentation on the marker lines is preserved as written, so a marker
// can sit inside an indented block.
//
// A missing marker is an error rather than an append: the generator cannot know where
// in a hand-written file the region belongs, and guessing would corrupt it.
func Region(src string, m Marker, body string) (string, error) {
	lines := strings.Split(src, "\n")
	begin, end := -1, -1
	for i, l := range lines {
		switch {
		case strings.Contains(l, m.Begin):
			if begin >= 0 {
				return "", fmt.Errorf("%s appears more than once", m.Begin)
			}
			begin = i
		case strings.Contains(l, m.End):
			if end >= 0 {
				return "", fmt.Errorf("%s appears more than once", m.End)
			}
			end = i
		}
	}
	switch {
	case begin < 0:
		return "", fmt.Errorf("missing marker %s", m.Begin)
	case end < 0:
		return "", fmt.Errorf("missing marker %s", m.End)
	case end < begin:
		return "", fmt.Errorf("%s appears before %s", m.End, m.Begin)
	}

	out := make([]string, 0, len(lines))
	out = append(out, lines[:begin+1]...)
	out = append(out, strings.Split(body, "\n")...)
	out = append(out, lines[end:]...)
	return strings.Join(out, "\n"), nil
}

// BuzzLibrary writes one std leaf package per Go package of the library module
// in dir, adapting its exported API to Buzz. genImport is outDir's import path.
// It writes nothing while any export lacks a binding, and names each one.
func BuzzLibrary(dir, outDir, genImport string) error {
	lib, err := loadLib(dir, genImport)
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
		if err := os.MkdirAll(filepath.Join(outDir, p.genName), 0o755); err != nil {
			return err
		}
		if err := File(filepath.Join(outDir, p.genName, "module.go"), src); err != nil {
			return err
		}
	}
	reg, err := lib.emitRegister(false)
	if err != nil {
		return err
	}
	if err := File(filepath.Join(outDir, "register.go"), reg); err != nil {
		return err
	}
	wasm, err := lib.emitRegister(true)
	if err != nil {
		return err
	}
	return File(filepath.Join(outDir, "register_wasm.go"), wasm)
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
		return lib.resolveSelector(v, lf)
	case *ast.ArrayType:
		return lib.resolveSlice(v, lf)
	}
	return nil, noBuzzForm(e)
}

func (lib *libModule) resolveSelector(v *ast.SelectorExpr, lf *libFile) (*conv, error) {
	x, _ := v.X.(*ast.Ident)
	if x == nil {
		return nil, noBuzzForm(v)
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
	return nil, noBuzzForm(v)
}

func (lib *libModule) resolveSlice(v *ast.ArrayType, lf *libFile) (*conv, error) {
	if v.Len != nil {
		return nil, noBuzzForm(v)
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

func noBuzzForm(e ast.Expr) error {
	return fmt.Errorf("type %s has no Buzz form", exprString(e))
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

// workspaceModule is the Go module the generated packages live in.
const workspaceModule = "github.com/egladman/magus"

// moduleEmitter writes one generated leaf package.
type moduleEmitter struct {
	lib     *libModule
	p       *libPkg
	body    bytes.Buffer
	imports map[string]string
	methods []string
}

func (lib *libModule) emitPackage(p *libPkg) ([]byte, error) {
	e := &moduleEmitter{lib: lib, p: p, imports: map[string]string{}}
	e.use("context", "context")
	e.use("std", "github.com/egladman/magus/std")
	e.use("convert", "github.com/egladman/magus/std/termkit/convert")
	e.use("lib", p.importPath)
	if err := e.bindings(); err != nil {
		return nil, err
	}
	return e.source()
}

// bindings writes every handle table, record codec, function, getter and
// value binding of e's package into its body and method list.
func (e *moduleEmitter) bindings() error {
	for _, t := range sortedTypes(e.p) {
		switch t.kind {
		case kindHandle, kindIface:
			fmt.Fprintf(&e.body, "// %sHandles holds the %s values a script refers to by handle.\n", t.name, t.name)
			fmt.Fprintf(&e.body, "var %sHandles = convert.NewTable[%s](%q)\n\n", t.name, e.goType(t, true), t.name)
		case kindRecord:
			if err := e.record(t); err != nil {
				return err
			}
		}
	}
	for _, fn := range e.lib.allFuncs(e.p) {
		if err := e.function(fn); err != nil {
			return fmt.Errorf("%s: %w", fn.qualified(), err)
		}
	}
	for _, t := range sortedTypes(e.p) {
		if t.kind != kindHandle {
			continue
		}
		for _, f := range t.fields {
			if isFunc(f.typ) {
				continue
			}
			if err := e.getter(t, f); err != nil {
				return fmt.Errorf("%s.%s: %w", t.name, f.name, err)
			}
		}
	}
	for _, v := range e.p.values {
		if err := e.value(v); err != nil {
			return fmt.Errorf("%s: %w", v.name, err)
		}
	}
	return nil
}

// source is the gofmt'd leaf package around e's body and methods.
func (e *moduleEmitter) source() ([]byte, error) {
	p := e.p
	var out bytes.Buffer
	out.WriteString("//go:build !wasm\n\n// Code generated by magus-utils libmodules. DO NOT EDIT.\n\n")
	genDir := strings.TrimPrefix(e.lib.genImport, workspaceModule+"/")
	rel := strings.Repeat("../", strings.Count(genDir, "/")+2)
	fmt.Fprintf(&out, "//go:generate go run %scmd/magus-utils bindings -module %s -lang buzz -out %sinternal/interp/bindings/gen/%s.go -pkg bindgen\n\n",
		rel, p.buzzName, rel, p.buzzName)
	fmt.Fprintf(&out, "package %s\n\nimport (\n", p.genName)
	for _, alias := range sortedKeys(e.imports) {
		fmt.Fprintf(&out, "\t%s %q\n", alias, e.imports[alias])
	}
	out.WriteString(")\n\n")
	doc := fmt.Sprintf("Bindings for the Go package %s, generated from its exported API. "+
		"A value with state is an int handle and a record is a map. Writers are standard error and readers standard input.", p.importPath)
	fmt.Fprintf(&out, "// Module binds %s for Buzz.\nvar Module = std.Module{\n\tName: %q,\n\tPath: %q,\n\tDoc: %q,\n",
		p.importPath, p.buzzName, p.buzzPath, doc)
	out.WriteString("\tMethods: []std.Method{\n")
	for _, m := range e.methods {
		out.WriteString(m)
	}
	out.WriteString("\t},\n}\n\n")
	out.Write(e.body.Bytes())
	src, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt the generated source: %w", err)
	}
	return src, nil
}

func (e *moduleEmitter) use(alias, importPath string) { e.imports[alias] = importPath }

// pkgAlias is the identifier generated code reaches p's library package by.
func (e *moduleEmitter) pkgAlias(p *libPkg) string {
	if p == e.p {
		return "lib"
	}
	alias := "lib" + p.genName
	e.use(alias, p.importPath)
	return alias
}

// genAlias is the identifier generated code reaches p's generated package by.
func (e *moduleEmitter) genAlias(p *libPkg) string {
	if p == e.p {
		return ""
	}
	alias := "gen" + p.genName
	e.use(alias, path.Join(e.lib.genImport, p.genName))
	return alias + "."
}

// goType spells t in generated code; a handle is held by pointer unless its
// methods take a value.
func (e *moduleEmitter) goType(t *libType, handle bool) string {
	name := fmt.Sprintf("%s.%s", e.pkgAlias(t.pkg), t.name)
	if handle && t.kind == kindHandle && t.byPointer() {
		return "*" + name
	}
	return name
}

// byPointer reports whether t's values travel as pointers: every handle but one
// whose constructors return it by value.
func (t *libType) byPointer() bool {
	for _, fn := range t.pkg.funcs {
		for _, r := range fn.results {
			if id, ok := r.(*ast.Ident); ok && id.Name == t.name {
				return false
			}
		}
	}
	return true
}

func (e *moduleEmitter) record(t *libType) error {
	gt := e.goType(t, false)
	var enc, dec bytes.Buffer
	fmt.Fprintf(&enc, "// Encode%s is a %s as a Buzz map.\nfunc Encode%s(v %s) map[string]any {\n\treturn map[string]any{\n", t.name, t.name, t.name, gt)
	fmt.Fprintf(&dec, "// Decode%s reads a %s from a Buzz map. A missing key reads as the zero value.\nfunc Decode%s(v any) (%s, error) {\n\tvar out %s\n\tm, err := convert.Map(v)\n\tif err != nil {\n\t\treturn out, err\n\t}\n", t.name, t.name, t.name, gt, gt)
	for _, f := range t.fields {
		if isFunc(f.typ) {
			continue
		}
		c, err := e.lib.resolve(f.typ, t.file)
		if err != nil {
			return err
		}
		key := lowerFirst(f.name)
		fmt.Fprintf(&enc, "\t\t%q: %s,\n", key, e.encode(c, "v."+f.name))
		fmt.Fprintf(&dec, "\tif out.%s, err = %s; err != nil {\n\t\treturn out, fmt.Errorf(\"%s: %%w\", err)\n\t}\n", f.name, e.decode(c, fmt.Sprintf("m[%q]", key)), key)
	}
	e.use("fmt", "fmt")
	enc.WriteString("\t}\n}\n\n")
	dec.WriteString("\treturn out, nil\n}\n\n")
	e.body.Write(enc.Bytes())
	e.body.Write(dec.Bytes())
	return nil
}

// encode is the expression turning the Go value src into a Buzz value.
func (e *moduleEmitter) encode(c *conv, src string) string {
	switch c.kind {
	case convPrim:
		switch c.prim {
		case "rune", "uintptr", "int64", "byte":
			return fmt.Sprintf("int(%s)", src)
		}
		return src
	case convDuration:
		e.use("time", "time")
		return fmt.Sprintf("int(%s / time.Millisecond)", src)
	case convError:
		return fmt.Sprintf("convert.ErrorText(%s)", src)
	case convBytes:
		return fmt.Sprintf("convert.BytesList(%s)", src)
	case convNamed:
		switch c.t.kind {
		case kindBasic:
			return fmt.Sprintf("%s(%s)", basicGo(c.t.basic), src)
		case kindRecord:
			return fmt.Sprintf("%sEncode%s(%s)", e.genAlias(c.t.pkg), c.t.name, src)
		default:
			return fmt.Sprintf("%s%sHandles.Put(%s)", e.genAlias(c.t.pkg), c.t.name, src)
		}
	case convSlice:
		return fmt.Sprintf("convert.EncodeList(%s, func(x %s) any { return %s })", src, e.goExpr(c.elem), e.encode(c.elem, "x"))
	}
	return src
}

// decode is the expression reading Go value of conv c from the Buzz value src,
// as (value, error).
func (e *moduleEmitter) decode(c *conv, src string) string {
	switch c.kind {
	case convPrim:
		switch c.prim {
		case "string":
			return fmt.Sprintf("convert.String(%s)", src)
		case "bool":
			return fmt.Sprintf("convert.Bool(%s)", src)
		case "float64":
			return fmt.Sprintf("convert.Float(%s)", src)
		case "int":
			return fmt.Sprintf("convert.Int(%s)", src)
		}
		return fmt.Sprintf("convert.IntAs[%s](%s)", c.prim, src)
	case convDuration:
		return fmt.Sprintf("convert.Millis(%s)", src)
	case convBytes:
		return fmt.Sprintf("convert.Bytes(%s)", src)
	case convNamed:
		switch c.t.kind {
		case kindBasic:
			if basicGo(c.t.basic) == "string" {
				return fmt.Sprintf("convert.StringAs[%s](%s)", e.goType(c.t, false), src)
			}
			return fmt.Sprintf("convert.IntAs[%s](%s)", e.goType(c.t, false), src)
		case kindRecord:
			return fmt.Sprintf("%sDecode%s(%s)", e.genAlias(c.t.pkg), c.t.name, src)
		default:
			return fmt.Sprintf("convert.Handle(%s%sHandles, %s)", e.genAlias(c.t.pkg), c.t.name, src)
		}
	case convSlice:
		return fmt.Sprintf("convert.DecodeSlice(%s, func(x any) (%s, error) { return %s })", src, e.goExpr(c.elem), e.decode(c.elem, "x"))
	}
	return src
}

// goExpr spells the Go type of conv c.
func (e *moduleEmitter) goExpr(c *conv) string {
	switch c.kind {
	case convPrim:
		return c.prim
	case convDuration:
		e.use("time", "time")
		return "time.Duration"
	case convBytes:
		return "[]byte"
	case convNamed:
		return e.goType(c.t, true)
	case convSlice:
		return "[]" + e.goExpr(c.elem)
	case convError:
		return "error"
	}
	return "any"
}

// tag is the std TypeTag a top-level argument or result of conv c takes.
func tag(c *conv) string {
	switch c.kind {
	case convPrim:
		switch c.prim {
		case "string":
			return "std.TypeString"
		case "bool":
			return "std.TypeBool"
		case "float64":
			return "std.TypeFloat"
		}
		return "std.TypeInt"
	case convDuration:
		return "std.TypeInt"
	case convBytes:
		return "std.TypeByteSlice"
	case convSlice:
		if c.elem.kind == convPrim && c.elem.prim == "string" {
			return "std.TypeStringSlice"
		}
	case convNamed:
		switch c.t.kind {
		case kindBasic:
			if basicGo(c.t.basic) == "string" {
				return "std.TypeString"
			}
			return "std.TypeInt"
		case kindRecord:
			return "std.TypeAnyMap"
		}
		return "std.TypeInt"
	}
	return "std.TypeAny"
}

// implementationParam is the Go type a top-level argument's Impl takes.
func implementationParam(t string) string {
	switch t {
	case "std.TypeString":
		return "string"
	case "std.TypeBool":
		return "bool"
	case "std.TypeFloat":
		return "float64"
	case "std.TypeInt":
		return "int"
	case "std.TypeByteSlice":
		return "[]byte"
	case "std.TypeStringSlice":
		return "[]string"
	case "std.TypeAnyMap":
		return "map[string]any"
	}
	return "any"
}

// binding is one function's Impl as it is built. Results hold no error; an
// error result sets raises.
type binding struct {
	args, params, pre, call []string
	results                 []*conv
	raises                  bool
}

// function binds fn as an Impl in the body and a std.Method entry.
func (e *moduleEmitter) function(fn *libFunc) error {
	name := snake(fn.name)
	if fn.recv != nil {
		name = fmt.Sprintf("%s_%s", snake(fn.recv.name), name)
	}
	impl := "Impl" + upperFirst(strings.ReplaceAll(fn.qualified(), ".", ""))
	b := &binding{params: []string{"ctx context.Context"}}
	e.receiver(fn, b)
	if err := e.arguments(fn, b); err != nil {
		return err
	}
	for _, r := range fn.results {
		c, err := e.lib.resolve(r, fn.file)
		if err != nil {
			return err
		}
		if c.kind == convError {
			b.raises = true
			continue
		}
		b.results = append(b.results, c)
	}
	retTag := e.implementation(fn, impl, b)
	e.method(fn, name, impl, retTag, b)
	return nil
}

// receiver reads fn's receiver from its handle, or decodes it from a map for
// a record.
func (e *moduleEmitter) receiver(fn *libFunc, b *binding) {
	if fn.recv == nil {
		return
	}
	recvName := lowerFirst(fn.recv.name)
	alias := e.genAlias(fn.recv.pkg)
	if fn.recv.kind == kindRecord {
		b.args = append(b.args, fmt.Sprintf("{Name: %q, Type: std.TypeAnyMap}", recvName))
		b.params = append(b.params, recvName+" map[string]any")
		b.pre = append(b.pre, fmt.Sprintf("recv, err := %sDecode%s(%s)\nif err != nil {\nreturn ZERO, err\n}", alias, fn.recv.name, recvName))
		return
	}
	b.args = append(b.args, fmt.Sprintf("{Name: %q, Type: std.TypeInt}", recvName))
	b.params = append(b.params, recvName+" int")
	b.pre = append(b.pre, fmt.Sprintf("recv, err := %s%sHandles.Get(%s)\nif err != nil {\nreturn ZERO, err\n}", alias, fn.recv.name, recvName))
}

// arguments adds fn's parameters. A context, writer or reader is supplied by
// the binding itself and takes no script argument.
func (e *moduleEmitter) arguments(fn *libFunc, b *binding) error {
	for _, prm := range fn.params {
		c, err := e.lib.resolve(prm.typ, fn.file)
		if err != nil {
			return err
		}
		switch c.kind {
		case convContext:
			b.call = append(b.call, "ctx")
			continue
		case convStderr:
			e.use("os", "os")
			b.call = append(b.call, "os.Stderr")
			continue
		case convStdin:
			e.use("os", "os")
			b.call = append(b.call, "os.Stdin")
			continue
		}
		t := tag(c)
		b.args = append(b.args, fmt.Sprintf("{Name: %q, Type: %s}", prm.name, t))
		goName := "a" + upperFirst(prm.name)
		b.params = append(b.params, fmt.Sprintf("%s %s", goName, implementationParam(t)))
		direct := (c.kind == convPrim && implementationParam(t) == c.prim) || c.kind == convBytes || t == "std.TypeStringSlice"
		val := "v" + upperFirst(prm.name)
		if direct {
			val = goName
		} else {
			b.pre = append(b.pre, fmt.Sprintf("%s, err := %s\nif err != nil {\nreturn ZERO, fmt.Errorf(\"%s: %%w\", err)\n}", val, e.decode(c, goName), prm.name))
			e.use("fmt", "fmt")
		}
		if prm.variadic {
			val += "..."
		}
		b.call = append(b.call, val)
	}
	return nil
}

// returns is the Go type, zero literal, std tag and return expression of b's
// results. Two or more return as a Buzz list.
func (e *moduleEmitter) returns(b *binding) (retType, zero, retTag, ret string) {
	switch len(b.results) {
	case 0:
		return "", "", "", ""
	case 1:
		retTag = tag(b.results[0])
		retType = implementationParam(retTag)
		return retType, zeroLiteral(retType), retTag, e.encodeTop(b.results[0], "r0", retType)
	}
	items := make([]string, 0, len(b.results))
	for i, c := range b.results {
		items = append(items, e.encode(c, fmt.Sprintf("r%d", i)))
	}
	return "any", "nil", "std.TypeAny", fmt.Sprintf("[]any{%s}", strings.Join(items, ", "))
}

// implementation writes fn's Impl function and returns its result's std tag.
func (e *moduleEmitter) implementation(fn *libFunc, impl string, b *binding) string {
	callee := fmt.Sprintf("%s.%s", e.pkgAlias(fn.file.pkg), fn.name)
	if fn.recv != nil {
		callee = "recv." + fn.name
	}
	invoke := fmt.Sprintf("%s(%s)", callee, strings.Join(b.call, ", "))
	retType, zero, retTag, ret := e.returns(b)
	var outs []string
	for i := range b.results {
		outs = append(outs, fmt.Sprintf("r%d", i))
	}
	if b.raises {
		outs = append(outs, "err")
	}
	var w bytes.Buffer
	fmt.Fprintf(&w, "// %s binds %s.%s.\nfunc %s(%s) ", impl, e.p.importPath, fn.qualified(), impl, strings.Join(b.params, ", "))
	zeroRet := "err"
	if retType != "" {
		fmt.Fprintf(&w, "(%s, error) {\n", retType)
		zeroRet = zero + ", err"
	} else {
		w.WriteString("error {\n")
	}
	for _, p := range b.pre {
		if retType == "" {
			p = strings.ReplaceAll(p, "return ZERO, ", "return ")
		}
		w.WriteString(strings.ReplaceAll(p, "ZERO", zero) + "\n")
	}
	switch {
	case len(outs) == 0:
		fmt.Fprintf(&w, "%s\nreturn nil\n}\n\n", invoke)
	case len(b.results) == 0:
		fmt.Fprintf(&w, "return %s\n}\n\n", invoke)
	default:
		fmt.Fprintf(&w, "%s := %s\n", strings.Join(outs, ", "), invoke)
		if b.raises {
			fmt.Fprintf(&w, "if err != nil {\nreturn %s\n}\n", zeroRet)
		}
		fmt.Fprintf(&w, "return %s, nil\n}\n\n", ret)
	}
	e.body.Write(w.Bytes())
	return retTag
}

// method adds fn's std.Method entry. A decode preamble can fail, so it raises.
func (e *moduleEmitter) method(fn *libFunc, name, impl, retTag string, b *binding) {
	var m bytes.Buffer
	fmt.Fprintf(&m, "\t\t{\n\t\t\tName: %q,\n\t\t\tDoc: %q,\n", name, methodDoc(fn))
	if len(b.args) > 0 {
		fmt.Fprintf(&m, "\t\t\tArgs: []std.Arg{%s},\n", strings.Join(b.args, ", "))
	}
	if retTag != "" {
		fmt.Fprintf(&m, "\t\t\tReturns: []std.Ret{{Type: %s}},\n", retTag)
	}
	if b.raises || len(b.pre) > 0 {
		m.WriteString("\t\t\tRaises: true,\n")
	}
	fmt.Fprintf(&m, "\t\t\tImpl: %s,\n\t\t},\n", impl)
	e.methods = append(e.methods, m.String())
}

// getter binds an exported field of a handle, which a script cannot read off
// the int it holds.
func (e *moduleEmitter) getter(t *libType, f libField) error {
	c, err := e.lib.resolve(f.typ, t.file)
	if err != nil {
		return err
	}
	retTag := tag(c)
	retType := implementationParam(retTag)
	impl := fmt.Sprintf("Impl%s%s", t.name, f.name)
	recv := lowerFirst(t.name)
	fmt.Fprintf(&e.body, "// %s reads the %s field of a %s.\nfunc %s(ctx context.Context, %s int) (%s, error) {\n", impl, f.name, t.name, impl, recv, retType)
	fmt.Fprintf(&e.body, "recv, err := %sHandles.Get(%s)\nif err != nil {\nreturn %s, err\n}\nreturn %s, nil\n}\n\n",
		t.name, recv, zeroLiteral(retType), e.encodeTop(c, "recv."+f.name, retType))
	e.methods = append(e.methods, fmt.Sprintf("\t\t{\n\t\t\tName: %q,\n\t\t\tDoc: %q,\n\t\t\tArgs: []std.Arg{{Name: %q, Type: std.TypeInt}},\n\t\t\tReturns: []std.Ret{{Type: %s}},\n\t\t\tRaises: true,\n\t\t\tImpl: %s,\n\t\t},\n",
		fmt.Sprintf("%s_%s", snake(t.name), snake(f.name)), fmt.Sprintf("The %s field of a %s.", f.name, t.name), recv, retTag, impl))
	return nil
}

// encodeTop converts a single result to the Go type its Impl returns.
func (e *moduleEmitter) encodeTop(c *conv, src, retType string) string {
	if retType == "[]string" {
		return src
	}
	if c.kind == convPrim && c.prim == retType {
		return src
	}
	if c.kind == convBytes {
		return src
	}
	return e.encode(c, src)
}

func (e *moduleEmitter) value(v *libValue) error {
	c, err := e.lib.valueConv(v)
	if err != nil {
		return err
	}
	impl := "Impl" + v.name
	src := fmt.Sprintf("%s.%s", e.pkgAlias(v.file.pkg), v.name)
	var t, goT, val string
	switch c.kind {
	case convError:
		t, goT, val = "std.TypeString", "string", src+".Error()"
	default:
		t = tag(c)
		goT = implementationParam(t)
		val = e.encodeTop(c, src, goT)
	}
	// A method rather than a std.Field: the checker declares methods, so a
	// script that names a value gets it checked.
	fmt.Fprintf(&e.body, "// %s reads %s.%s.\nfunc %s(ctx context.Context) (%s, error) { return %s, nil }\n\n", impl, e.p.importPath, v.name, impl, goT, val)
	e.methods = append(e.methods, fmt.Sprintf("\t\t{\n\t\t\tName: %q,\n\t\t\tDoc: %q,\n\t\t\tReturns: []std.Ret{{Type: %s}},\n\t\t\tImpl: %s,\n\t\t},\n",
		snake(v.name), valueDoc(v), t, impl))
	return nil
}

func zeroLiteral(goType string) string {
	switch goType {
	case "string":
		return `""`
	case "int", "float64":
		return "0"
	case "bool":
		return "false"
	}
	return "nil"
}

func basicGo(basic string) string {
	if basic == "string" {
		return "string"
	}
	return "int"
}

func methodDoc(fn *libFunc) string {
	doc := firstSentence(fn.doc)
	if doc == "" {
		doc = fmt.Sprintf("Calls %s.", fn.qualified())
	}
	return doc
}

func valueDoc(v *libValue) string {
	doc := firstSentence(v.doc)
	if doc == "" {
		doc = fmt.Sprintf("The value of %s.", v.name)
	}
	return doc
}

// firstSentence is a Go doc comment's opening sentence, on one line.
func firstSentence(doc string) string {
	doc = strings.Join(strings.Fields(doc), " ")
	if i := strings.Index(doc, ". "); i >= 0 {
		doc = doc[:i+1]
	}
	return doc
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	i := 0
	for i < len(r) && i < len(r)-1 && isUpper(r[i]) && isUpper(r[i+1]) {
		i++
	}
	if i == 0 {
		return strings.ToLower(string(r[0])) + string(r[1:])
	}
	return strings.ToLower(string(r[:i])) + string(r[i:])
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func isUpper(r rune) bool { return r >= 'A' && r <= 'Z' }

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// emitRegister lists the generated modules. The wasm build gets none: the
// library draws on a terminal, which a browser does not have.
func (lib *libModule) emitRegister(wasm bool) ([]byte, error) {
	var b bytes.Buffer
	// Named for the library, since a bare `gen` clause is one every generated
	// package would share.
	clause := path.Base(path.Dir(lib.genImport)) + path.Base(lib.genImport)
	if wasm {
		b.WriteString("//go:build wasm\n\n// Code generated by magus-utils libmodules. DO NOT EDIT.\n\n")
		fmt.Fprintf(&b, "package %s\n\nimport \"github.com/egladman/magus/std\"\n\n", clause)
		b.WriteString("// Modules is empty: the browser has no terminal to draw on.\nfunc Modules() []std.Module { return nil }\n")
		return format.Source(b.Bytes())
	}
	b.WriteString("//go:build !wasm\n\n// Code generated by magus-utils libmodules. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "package %s\n\nimport (\n\t\"github.com/egladman/magus/std\"\n", clause)
	for _, p := range lib.pkgs {
		fmt.Fprintf(&b, "\tgen%s %q\n", p.genName, path.Join(lib.genImport, p.genName))
	}
	b.WriteString(")\n\n// Modules is one Buzz module per Go package of the library.\nfunc Modules() []std.Module {\n\treturn []std.Module{\n")
	for _, p := range lib.pkgs {
		fmt.Fprintf(&b, "\t\tgen%s.Module,\n", p.genName)
	}
	b.WriteString("\t}\n}\n")
	return format.Source(b.Bytes())
}
