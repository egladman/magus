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
	gotypes "go/types"
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

// publicGoPackages is the one list of packages whose exported Go API is locked.
var publicGoPackages = []string{
	"github.com/egladman/magus",
	"github.com/egladman/magus/types",
	"github.com/egladman/magus/libs/testkit",
	"github.com/egladman/magus/libs/testkit/register",
	"github.com/egladman/magus/types/gen/mocks",
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
func loadGoPackages(dir string) ([]*gotypes.Package, error) {
	cmd := exec.Command("go", append([]string{"list", "-e", "-export", "-deps",
		"-json=ImportPath,Dir,GoFiles,Export,DepOnly,Error"}, publicGoPackages...)...)
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

	var pkgs []*gotypes.Package
	for _, want := range publicGoPackages {
		p, ok := listed[want]
		if !ok || p.Error != nil || len(p.GoFiles) == 0 {
			reason := "not listed"
			if ok && p.Error != nil {
				reason = p.Error.Err
			} else if ok {
				reason = "no Go files"
			}
			return nil, fmt.Errorf("public package %s: %s", want, reason)
		}
		files := make([]*ast.File, 0, len(p.GoFiles))
		for _, name := range p.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(p.Dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				return nil, fmt.Errorf("parse %s: %w", want, err)
			}
			files = append(files, f)
		}
		conf := gotypes.Config{Importer: imp}
		pkg, err := conf.Check(want, fset, files, nil)
		if err != nil {
			return nil, fmt.Errorf("type-check %s: %w", want, err)
		}
		pkgs = append(pkgs, pkg)
	}
	return pkgs, nil
}

// renderGoAPI returns the sorted lock lines for pkgs. An alias to a type that no
// package in pkgs declares is expanded with that type's fields and methods, since
// otherwise the lock would pin the alias but not what a caller can do with it.
//
// A line is the symbol path, a space, then the declaration. The space sorts below
// every character a path holds, so a type is followed by its members and the plain
// byte order the lockfiles-are-sorted lint demands is also the reading order.
func renderGoAPI(pkgs []*gotypes.Package) []string {
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

// goAPIRenderer renders one package's exported declarations as lock lines. public
// holds the path of every locked package: a type from any other package is
// expanded in place, since nothing else pins it.
type goAPIRenderer struct {
	pkg    *gotypes.Package
	public map[string]bool
}

// qualify spells a type from the package being rendered bare and every other
// package by its full import path, which is unambiguous where a name is not.
func (r goAPIRenderer) qualify(p *gotypes.Package) string {
	if p == r.pkg {
		return ""
	}
	return p.Path()
}

// typeString spells t with qualify, the form the lock pins.
func (r goAPIRenderer) typeString(t gotypes.Type) string {
	return gotypes.TypeString(t, r.qualify)
}

// lockLine joins a sort key to its declaration text: the symbol path, then each
// member beneath it.
func lockLine(id, format string, args ...any) string {
	return id + " " + fmt.Sprintf(format, args...)
}

// declarations lists a line for every exported constant, variable, function and
// type in the package, in scope order.
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
		case *gotypes.Const:
			lines = append(lines, lockLine(id, "const %s = %s", r.typeString(o.Type()), o.Val().ExactString()))
		case *gotypes.Var:
			lines = append(lines, lockLine(id, "var %s", r.typeString(o.Type())))
		case *gotypes.Func:
			lines = append(lines, lockLine(id, "%s", r.typeString(o.Type())))
		case *gotypes.TypeName:
			lines = append(lines, r.typeDeclaration(id, o)...)
		}
	}
	return lines
}

// typeDeclaration returns the head line of tn followed by its members. An alias
// to a type declared outside the locked packages carries that type's members too.
func (r goAPIRenderer) typeDeclaration(id string, tn *gotypes.TypeName) []string {
	if alias, ok := tn.Type().(*gotypes.Alias); ok {
		head := lockLine(id, "type%s = %s", r.typeParams(alias.TypeParams()), r.typeString(alias.Rhs()))
		lines := []string{head}
		if named, ok := gotypes.Unalias(alias).(*gotypes.Named); ok && named.Obj().Pkg() != nil && !r.public[named.Obj().Pkg().Path()] {
			lines = append(lines, r.members(id, named)...)
		}
		return lines
	}
	named, ok := tn.Type().(*gotypes.Named)
	if !ok {
		return nil
	}
	var kind string
	switch u := named.Underlying().(type) {
	case *gotypes.Struct:
		kind = "struct"
	case *gotypes.Interface:
		kind = "interface"
	default:
		kind = r.typeString(u)
	}
	head := lockLine(id, "type%s %s", r.typeParams(named.TypeParams()), kind)
	return append([]string{head}, r.members(id, named)...)
}

// members lists the exported fields, embedded types and methods of named under id.
// Methods come from the method set of *named, so promoted ones are included and a
// value-receiver method is told apart from a pointer-receiver one.
func (r goAPIRenderer) members(id string, named *gotypes.Named) []string {
	var lines []string
	switch u := named.Underlying().(type) {
	case *gotypes.Struct:
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
			lines = append(lines, lockLine(id+"."+f.Name(), "%s", text))
		}
	case *gotypes.Interface:
		for i := range u.NumExplicitMethods() {
			m := u.ExplicitMethod(i)
			if m.Exported() {
				lines = append(lines, lockLine(id+"."+m.Name(), "imethod %s", r.typeString(m.Type())))
			}
		}
		for i := range u.NumEmbeddeds() {
			lines = append(lines, lockLine(id+".embeds", "%s", r.typeString(u.EmbeddedType(i))))
		}
		return lines
	}
	if gotypes.IsInterface(named) {
		return lines
	}
	ptrSet := gotypes.NewMethodSet(gotypes.NewPointer(named))
	valSet := gotypes.NewMethodSet(named)
	for i := range ptrSet.Len() {
		fn, ok := ptrSet.At(i).Obj().(*gotypes.Func)
		if !ok || !fn.Exported() {
			continue
		}
		kind := "method"
		if valSet.Lookup(fn.Pkg(), fn.Name()) == nil {
			kind = "method(ptr)"
		}
		lines = append(lines, lockLine(id+"."+fn.Name(), "%s %s", kind, r.typeString(fn.Type())))
	}
	return lines
}

// typeParams spells a type parameter list as "[K comparable, V any]", or "" for
// a type with none.
func (r goAPIRenderer) typeParams(list *gotypes.TypeParamList) string {
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
