package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/internal/generate/emit"
	json "github.com/egladman/magus/internal/json"
)

// publicGoPackages is the one list of packages whose exported Go API is locked. A
// package marked optional is skipped while it does not exist in the tree.
var publicGoPackages = []struct {
	path     string
	optional bool
}{
	{path: "github.com/egladman/magus"},
	{path: "github.com/egladman/magus/types"},
	{path: "github.com/egladman/magus/libs/testkit"},
	{path: "github.com/egladman/magus/libs/testkit/fakespell", optional: true},
	{path: "github.com/egladman/magus/types/gen/mocks"},
}

// runGoAPI writes the exported Go API of the public packages as a sorted .lock, in
// the same one-line-per-element shape as api.lock: a removed or changed line is a
// break for every program that imports magus as a library.
func runGoAPI(args []string) error {
	fs := flag.NewFlagSet("goapi", flag.ContinueOnError)
	out := fs.String("out", "", "write the Go API lock to this path (default: stdout)")
	dir := fs.String("dir", ".", "module root the packages are listed from")
	if err := fs.Parse(args); err != nil {
		return err
	}
	pkgs, err := loadGoPackages(*dir)
	if err != nil {
		return err
	}
	data := []byte(strings.Join(renderGoAPI(pkgs), "\n") + "\n")
	if *out == "" {
		_, err := os.Stdout.Write(data)
		return err
	}
	return emit.File(*out, data)
}

// listedPackage is the part of `go list -json` this tool reads.
type listedPackage struct {
	ImportPath string
	Dir        string
	GoFiles    []string
	Export     string
	DepOnly    bool
	Error      *struct{ Err string }
}

// loadGoPackages type-checks each public package from source, resolving its imports
// from the compiler's export data so the whole dependency tree is not re-checked.
// The files are those of the host platform, so an exported declaration behind a
// platform build tag would make the lock differ per machine.
func loadGoPackages(dir string) ([]*types.Package, error) {
	paths := make([]string, len(publicGoPackages))
	for i, p := range publicGoPackages {
		paths[i] = p.path
	}
	cmd := exec.Command("go", append([]string{"list", "-e", "-export", "-deps",
		"-json=ImportPath,Dir,GoFiles,Export,DepOnly,Error"}, paths...)...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list: %s: %w", strings.TrimSpace(stderr.String()), err)
	}

	exports := map[string]string{}
	listed := map[string]listedPackage{}
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		var p listedPackage
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode go list output: %w", err)
		}
		exports[p.ImportPath] = p.Export
		listed[p.ImportPath] = p
	}

	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file := exports[path]
		if file == "" {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})

	var pkgs []*types.Package
	for _, want := range publicGoPackages {
		p, ok := listed[want.path]
		if !ok || p.Error != nil || len(p.GoFiles) == 0 {
			if want.optional {
				continue
			}
			reason := "not listed"
			if ok && p.Error != nil {
				reason = p.Error.Err
			} else if ok {
				reason = "no Go files"
			}
			return nil, fmt.Errorf("public package %s: %s", want.path, reason)
		}
		files := make([]*ast.File, 0, len(p.GoFiles))
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", want.path, err)
			}
			files = append(files, f)
		}
		conf := types.Config{Importer: imp}
		pkg, err := conf.Check(want.path, fset, files, nil)
		if err != nil {
			return nil, fmt.Errorf("type-check %s: %w", want.path, err)
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// goAPILine is one lock line. id is the sort key: the package path, then the
// symbol, then the member, so a type is followed by its fields and methods.
// renderGoAPI returns the sorted lock lines for pkgs. An alias to a type that no
// package in pkgs declares is expanded with that type's fields and methods, since
// otherwise the lock would pin the alias but not what a caller can do with it.
//
// A line is the symbol path, a space, then the declaration. The space sorts below
// every character a path holds, so a type is followed by its members and the plain
// byte order the lockfiles-are-sorted lint demands is also the reading order.
func renderGoAPI(pkgs []*types.Package) []string {
	public := map[string]bool{}
	for _, p := range pkgs {
		public[p.Path()] = true
	}
	var lines []string
	for _, pkg := range pkgs {
		r := goAPIRenderer{pkg: pkg, public: public}
		lines = append(lines, r.declarations()...)
	}
	slices.Sort(lines)
	return lines
}

type goAPIRenderer struct {
	pkg    *types.Package
	public map[string]bool
}

// qualify spells a type from the package being rendered bare and every other
// package by its full import path, which is unambiguous where a name is not.
func (r goAPIRenderer) qualify(p *types.Package) string {
	if p == r.pkg {
		return ""
	}
	return p.Path()
}

func (r goAPIRenderer) typeString(t types.Type) string {
	return types.TypeString(t, r.qualify)
}

func (r goAPIRenderer) line(id, format string, args ...any) string {
	return id + " " + fmt.Sprintf(format, args...)
}

func (r goAPIRenderer) declarations() []string {
	var lines []string
	scope := r.pkg.Scope()
	for _, name := range scope.Names() {
		obj := scope.Lookup(name)
		if !obj.Exported() {
			continue
		}
		id := r.pkg.Path() + "." + name
		switch o := obj.(type) {
		case *types.Const:
			lines = append(lines, r.line(id, "const %s = %s", r.typeString(o.Type()), o.Val().ExactString()))
		case *types.Var:
			lines = append(lines, r.line(id, "var %s", r.typeString(o.Type())))
		case *types.Func:
			lines = append(lines, r.line(id, "%s", r.typeString(o.Type())))
		case *types.TypeName:
			lines = append(lines, r.typeDeclaration(id, o)...)
		}
	}
	return lines
}

func (r goAPIRenderer) typeDeclaration(id string, tn *types.TypeName) []string {
	if alias, ok := tn.Type().(*types.Alias); ok {
		head := r.line(id, "type%s = %s", r.typeParams(alias.TypeParams()), r.typeString(alias.Rhs()))
		lines := []string{head}
		if named, ok := types.Unalias(alias).(*types.Named); ok && named.Obj().Pkg() != nil && !r.public[named.Obj().Pkg().Path()] {
			lines = append(lines, r.members(id, named)...)
		}
		return lines
	}
	named, ok := tn.Type().(*types.Named)
	if !ok {
		return nil
	}
	var kind string
	switch u := named.Underlying().(type) {
	case *types.Struct:
		kind = "struct"
	case *types.Interface:
		kind = "interface"
	default:
		kind = r.typeString(u)
	}
	head := r.line(id, "type%s %s", r.typeParams(named.TypeParams()), kind)
	return append([]string{head}, r.members(id, named)...)
}

// members lists the exported fields, embedded types and methods of named under id.
// Methods come from the method set of *named, so promoted ones are included and a
// value-receiver method is told apart from a pointer-receiver one.
func (r goAPIRenderer) members(id string, named *types.Named) []string {
	var lines []string
	switch u := named.Underlying().(type) {
	case *types.Struct:
		for i := range u.NumFields() {
			f := u.Field(i)
			if !f.Exported() {
				continue
			}
			kind := "field"
			if f.Embedded() {
				kind = "embedded"
			}
			text := fmt.Sprintf("%s %s", kind, r.typeString(f.Type()))
			if tag := u.Tag(i); tag != "" {
				text += " " + strconv.Quote(tag)
			}
			lines = append(lines, r.line(id+"."+f.Name(), "%s", text))
		}
	case *types.Interface:
		for i := range u.NumExplicitMethods() {
			m := u.ExplicitMethod(i)
			if m.Exported() {
				lines = append(lines, r.line(id+"."+m.Name(), "imethod %s", r.typeString(m.Type())))
			}
		}
		for i := range u.NumEmbeddeds() {
			lines = append(lines, r.line(id+".embeds", "%s", r.typeString(u.EmbeddedType(i))))
		}
		return lines
	}
	if types.IsInterface(named) {
		return lines
	}
	ptrSet := types.NewMethodSet(types.NewPointer(named))
	valSet := types.NewMethodSet(named)
	for i := range ptrSet.Len() {
		fn, ok := ptrSet.At(i).Obj().(*types.Func)
		if !ok || !fn.Exported() {
			continue
		}
		kind := "method"
		if valSet.Lookup(fn.Pkg(), fn.Name()) == nil {
			kind = "method(ptr)"
		}
		lines = append(lines, r.line(id+"."+fn.Name(), "%s %s", kind, r.typeString(fn.Type())))
	}
	return lines
}

func (r goAPIRenderer) typeParams(list *types.TypeParamList) string {
	if list.Len() == 0 {
		return ""
	}
	parts := make([]string, list.Len())
	for i := range list.Len() {
		tp := list.At(i)
		parts[i] = tp.Obj().Name() + " " + r.typeString(tp.Constraint())
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
