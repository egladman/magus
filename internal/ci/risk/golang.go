package risk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
)

// goPackage is the part of one `go list -json` record the classifier reads.
type goPackage struct {
	ImportPath      string    `json:"ImportPath"`
	Name            string    `json:"Name"`
	Dir             string    `json:"Dir"`
	ForTest         string    `json:"ForTest"`
	Deps            []string  `json:"Deps"`
	GoFiles         []string  `json:"GoFiles"`
	CgoFiles        []string  `json:"CgoFiles"`
	TestGoFiles     []string  `json:"TestGoFiles"`
	XTestGoFiles    []string  `json:"XTestGoFiles"`
	IgnoredGoFiles  []string  `json:"IgnoredGoFiles"`
	EmbedFiles      []string  `json:"EmbedFiles"`
	TestEmbedFiles  []string  `json:"TestEmbedFiles"`
	XTestEmbedFiles []string  `json:"XTestEmbedFiles"`
	Incomplete      bool      `json:"Incomplete"`
	Error           *goError  `json:"Error"`
	DepsErrors      []goError `json:"DepsErrors"`
}

type goError struct {
	Err string `json:"Err"`
}

const goListFields = "ImportPath,Name,Dir,ForTest,Deps,GoFiles,CgoFiles,TestGoFiles,XTestGoFiles," +
	"IgnoredGoFiles,EmbedFiles,TestEmbedFiles,XTestEmbedFiles,Incomplete,Error,DepsErrors"

// GoList runs `go list -e -test` over ./... in the module rooted at dir. -test is
// what makes each package's test binary (p.test) appear with its full Deps, which
// is the only place go reports what a package's tests import transitively.
func GoList(ctx context.Context, dir string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-test", "-json="+goListFields, "./...")
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %w: %s", dir, err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func decodeGoList(out []byte) ([]goPackage, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []goPackage
	for {
		var p goPackage
		err := dec.Decode(&p)
		if errors.Is(err, io.EOF) {
			return pkgs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode go list: %w", err)
		}
		pkgs = append(pkgs, p)
	}
}

// goFileRole is where a file sits in a module's build.
type goFileRole int

const (
	goUnknown goFileRole = iota
	goCompiled
	goTestOnly
	goIgnored
)

type goFileHit struct {
	pkg  string
	role goFileRole
}

// goModule indexes one module's `go list -test` output.
type goModule struct {
	dir     string
	primary map[string]goPackage
	// testDeps maps a package to its test binary's transitive imports.
	testDeps map[string][]string
	files    map[string]goFileHit
	// poison is a load error that makes reverse dependencies untrustworthy.
	poison string
	// generators maps a package reachable only from go:generate programs to the
	// program that reaches it.
	generators map[string]string
}

// excludedByConstraints is the one load error that does not poison a module: the
// package has no files on this platform, so it imports nothing here, and a change
// to its files lands in IgnoredGoFiles, which is classified full on its own.
const excludedByConstraints = "build constraints exclude all Go files"

func newGoModule(dir string, pkgs []goPackage) *goModule {
	m := &goModule{
		dir:        dir,
		primary:    map[string]goPackage{},
		testDeps:   map[string][]string{},
		files:      map[string]goFileHit{},
		generators: map[string]string{},
	}
	for _, p := range pkgs {
		isTestMain := strings.HasSuffix(p.ImportPath, ".test") && p.Name == "main"
		if m.poison == "" {
			if msg := goLoadError(p); msg != "" {
				m.poison = p.ImportPath + ": " + msg
			}
		}
		switch {
		case isTestMain:
			m.testDeps[strings.TrimSuffix(p.ImportPath, ".test")] = normalizeDeps(p.Deps)
		case p.ForTest == "":
			p.Deps = normalizeDeps(p.Deps)
			m.primary[p.ImportPath] = p
			m.index(p)
		}
	}
	m.generators = generatorOnly(m.primary)
	return m
}

func goLoadError(p goPackage) string {
	if p.Error != nil && !strings.Contains(p.Error.Err, excludedByConstraints) {
		return p.Error.Err
	}
	for _, e := range p.DepsErrors {
		if !strings.Contains(e.Err, excludedByConstraints) {
			return e.Err
		}
	}
	if p.Incomplete && p.Error == nil && len(p.DepsErrors) == 0 {
		return "incomplete package"
	}
	return ""
}

func (m *goModule) index(p goPackage) {
	add := func(names []string, role goFileRole) {
		for _, n := range names {
			m.files[filepath.Join(p.Dir, n)] = goFileHit{pkg: p.ImportPath, role: role}
		}
	}
	add(p.GoFiles, goCompiled)
	add(p.CgoFiles, goCompiled)
	add(p.EmbedFiles, goCompiled)
	add(p.TestGoFiles, goTestOnly)
	add(p.XTestGoFiles, goTestOnly)
	add(p.TestEmbedFiles, goTestOnly)
	add(p.XTestEmbedFiles, goTestOnly)
	add(p.IgnoredGoFiles, goIgnored)
}

// normalizeDeps drops go list's test-variant suffix ("p [q.test]" is p recompiled
// for q's tests) so every dependency is named by its import path.
func normalizeDeps(deps []string) []string {
	out := make([]string, 0, len(deps))
	for _, d := range deps {
		if i := strings.Index(d, " ["); i >= 0 {
			d = d[:i]
		}
		out = append(out, d)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// closure returns every package whose build or tests can observe a change: the
// test-only packages as given, the changed packages, each package importing one
// transitively, and each package whose test binary does.
func (m *goModule) closure(changed, testOnly map[string]bool) []string {
	out := map[string]bool{}
	for p := range testOnly {
		out[p] = true
	}
	touches := func(deps []string) bool {
		return slices.ContainsFunc(deps, func(d string) bool { return changed[d] })
	}
	for path, p := range m.primary {
		if changed[path] || touches(p.Deps) {
			out[path] = true
		}
	}
	for path, deps := range m.testDeps {
		if touches(deps) {
			out[path] = true
		}
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// generatorOnly finds the programs go:generate directives run (`go run <pkg>`) and
// returns the packages only those programs reach: code whose sole purpose is
// producing generated output. A package a shipped program also links is not
// generator-only, and its effect on generated output is the drift check's to catch.
func generatorOnly(primary map[string]goPackage) map[string]string {
	gens := map[string]string{}
	byDir := map[string]string{}
	for path, p := range primary {
		byDir[p.Dir] = path
	}
	for _, p := range primary {
		for _, f := range slices.Concat(p.GoFiles, p.CgoFiles) {
			for _, target := range goGenerateRuns(filepath.Join(p.Dir, f)) {
				var prog string
				switch {
				case strings.HasSuffix(target, ".go"):
					prog = byDir[filepath.Dir(filepath.Join(p.Dir, target))]
				case strings.HasPrefix(target, "."):
					prog = byDir[filepath.Clean(filepath.Join(p.Dir, target))]
				default:
					prog = target
				}
				if gp, ok := primary[prog]; ok && gp.Name == "main" {
					gens[prog] = prog
				}
			}
		}
	}
	if len(gens) == 0 {
		return map[string]string{}
	}
	reach := func(roots []string) map[string]string {
		out := map[string]string{}
		for _, r := range roots {
			out[r] = r
			for _, d := range primary[r].Deps {
				if _, ok := primary[d]; ok {
					if _, seen := out[d]; !seen {
						out[d] = r
					}
				}
			}
		}
		return out
	}
	var genRoots, otherMains []string
	for path, p := range primary {
		switch {
		case gens[path] != "":
			genRoots = append(genRoots, path)
		case p.Name == "main":
			otherMains = append(otherMains, path)
		}
	}
	slices.Sort(genRoots)
	shipped := reach(otherMains)
	out := map[string]string{}
	for pkg, prog := range reach(genRoots) {
		if _, ok := shipped[pkg]; !ok {
			out[pkg] = prog
		}
	}
	return out
}

// goGenerateRuns returns the package argument of every `//go:generate go run`
// directive in file. An unreadable file yields none: the file itself is still
// classified by its package.
func goGenerateRuns(file string) []string {
	b, err := os.ReadFile(file)
	if err != nil || !bytes.Contains(b, []byte("//go:generate")) {
		return nil
	}
	var out []string
	for line := range strings.SplitSeq(string(b), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "//go:generate ")
		if !ok {
			continue
		}
		f := strings.Fields(rest)
		if len(f) < 3 || f[0] != "go" || f[1] != "run" {
			continue
		}
		for _, a := range f[2:] {
			if !strings.HasPrefix(a, "-") {
				out = append(out, a)
				break
			}
		}
	}
	return out
}

// goEquivalent reports whether two Go sources parse to the same syntax tree once
// comments and positions are ignored, and carry the same directive comments. That
// covers comment-only and gofmt-only edits. Directives (//go:build, //go:embed,
// cgo's preamble, and the rest the go spell declares) change what compiles, so
// they compare as code. A source that does not parse is never equivalent.
func goEquivalent(old, cur string, directives []string) bool {
	fo, co, err := parseGo(old)
	if err != nil {
		return false
	}
	fc, cc, err := parseGo(cur)
	if err != nil {
		return false
	}
	return slices.Equal(goDirectives(fo, co, directives), goDirectives(fc, cc, directives)) &&
		astEqual(reflect.ValueOf(fo), reflect.ValueOf(fc))
}

func parseGo(src string) (*ast.File, []*ast.CommentGroup, error) {
	f, err := parser.ParseFile(token.NewFileSet(), "", src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}
	return f, f.Comments, nil
}

func goDirectives(f *ast.File, groups []*ast.CommentGroup, directives []string) []string {
	var out []string
	for _, d := range f.Decls {
		gd, ok := d.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		for _, s := range gd.Specs {
			imp, ok := s.(*ast.ImportSpec)
			if !ok || imp.Path.Value != `"C"` {
				continue
			}
			// cgo reads the comment above `import "C"` as C source; unparenthesized, the
			// parser files it on the declaration rather than the spec.
			for _, doc := range []*ast.CommentGroup{gd.Doc, imp.Doc} {
				if doc != nil {
					out = append(out, "cgo:"+doc.Text())
				}
			}
		}
	}
	for _, g := range groups {
		for _, c := range g.List {
			body, ok := strings.CutPrefix(c.Text, "//")
			if !ok {
				continue
			}
			if slices.ContainsFunc(directives, func(d string) bool { return strings.HasPrefix(body, d) }) {
				out = append(out, strings.TrimRight(c.Text, " \t"))
			}
		}
	}
	return out
}

var (
	posType          = reflect.TypeFor[token.Pos]()
	commentGroupType = reflect.TypeFor[*ast.CommentGroup]()
	commentListType  = reflect.TypeFor[[]*ast.CommentGroup]()
	identListType    = reflect.TypeFor[[]*ast.Ident]()
	importListType   = reflect.TypeFor[[]*ast.ImportSpec]()
)

// astEqual compares two syntax trees field by field, skipping positions, comment
// fields and the resolver's derived fields. ast.File's Imports and Unresolved are
// derived from Decls and would only compare what Decls already does.
func astEqual(a, b reflect.Value) bool {
	if a.Kind() != b.Kind() {
		return false
	}
	switch a.Kind() {
	case reflect.Pointer, reflect.Interface:
		if a.IsNil() || b.IsNil() {
			return a.IsNil() == b.IsNil()
		}
		if a.Kind() == reflect.Interface && a.Elem().Type() != b.Elem().Type() {
			return false
		}
		return astEqual(a.Elem(), b.Elem())
	case reflect.Struct:
		isFile := a.Type() == reflect.TypeFor[ast.File]()
		for i := range a.NumField() {
			f := a.Type().Field(i)
			switch f.Type {
			case posType, commentGroupType, commentListType:
				continue
			}
			// The resolver's fields are named by type string: SkipObjectResolution
			// leaves them nil, and the types themselves are deprecated.
			if s := f.Type.String(); s == "*ast.Object" || s == "*ast.Scope" {
				continue
			}
			if isFile && (f.Type == identListType || f.Type == importListType) {
				continue
			}
			if !f.IsExported() {
				continue
			}
			if !astEqual(a.Field(i), b.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if a.Len() != b.Len() {
			return false
		}
		for i := range a.Len() {
			if !astEqual(a.Index(i), b.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Map:
		return a.Len() == 0 && b.Len() == 0
	}
	return a.Interface() == b.Interface()
}
