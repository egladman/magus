// Package golang is the Go prover for internal/risk: syntax-tree equivalence for Go
// sources, and placement of changed files into packages through `go list -e -test`,
// whose reverse-dependency closure narrows a scoped gate's tests.
package golang

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
	"github.com/egladman/magus/internal/risk"
)

// TestOp is the go spell op whose package arguments a scoped gate narrows.
const TestOp = "go::go-test"

// Prover proves Go changes. The zero value lists nothing; use New.
type Prover struct {
	// Directives are the comment prefixes the go spell declares as directives
	// (`go:`, `nolint`, ...): comments that change what compiles, so they compare as code.
	Directives []string
	// List returns `go list -e -test -json` output for the module rooted at dir.
	List func(ctx context.Context, dir string) ([]byte, error)
}

// New is the Prover over the real `go list`, with the go spell's directive prefixes.
func New(directives []string) Prover {
	return Prover{Directives: directives, List: GoList}
}

// Equivalent reports whether two sources of a .go path parse to the same syntax tree
// once comments and positions are ignored, and carry the same directives.
func (p Prover) Equivalent(path, old, cur string) (bool, string) {
	if !strings.EqualFold(filepath.Ext(path), ".go") || !goEquivalent(old, cur, p.Directives) {
		return false, ""
	}
	return true, "comment or format only: its comment-free syntax tree and directives equal the base's"
}

// Place loads each Go module holding a path and places the path in the package that
// compiles or embeds it. A module that fails to load, or loads with an error, cannot be
// trusted about its reverse dependencies, so its paths are unplaced.
func (p Prover) Place(ctx context.Context, root string, paths []string) (risk.Placement, error) {
	pl := risk.Placement{
		Packages: map[string]risk.PackageHit{},
		Unplaced: map[string]string{},
		Narrows:  TestOp,
	}
	loaded := map[string]*goModule{}
	failed := map[string]string{}
	for _, rel := range paths {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		dir := moduleDir(root, abs)
		if dir == "" {
			continue
		}
		m, ok := loaded[dir]
		if !ok && failed[dir] == "" {
			if m, failed[dir] = p.load(ctx, dir); m != nil {
				loaded[dir] = m
			}
		}
		modRel := relDir(root, dir)
		switch {
		case m == nil:
			pl.Unplaced[rel] = "go list failed in module " + modRel + ", so what compiles or embeds it is unknown: " + failed[dir]
		case m.poison != "":
			pl.Unplaced[rel] = "go list reported a load error in module " + modRel + ", so its reverse dependencies cannot be trusted: " + m.poison
		default:
			if hit, why, ok := m.place(abs, modRel); ok {
				pl.Packages[rel] = hit
			} else if why != "" {
				pl.Unplaced[rel] = why
			}
		}
	}
	pl.Closure = func(changed, testOnly []string) []string {
		out := map[string]bool{}
		for _, m := range loaded {
			owns := func(pkg string) bool { _, ok := m.primary[pkg]; return ok }
			if !slices.ContainsFunc(changed, owns) && !slices.ContainsFunc(testOnly, owns) {
				continue
			}
			for _, pkg := range m.closure(changed, testOnly) {
				out[pkg] = true
			}
		}
		keys := make([]string, 0, len(out))
		for k := range out {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		return keys
	}
	return pl, nil
}

func (p Prover) load(ctx context.Context, dir string) (*goModule, string) {
	if p.List == nil {
		return nil, "no go list runner"
	}
	out, err := p.List(ctx, dir)
	var pkgs []goPackage
	if err == nil {
		pkgs, err = decodeGoList(out)
	}
	if err != nil {
		return nil, err.Error()
	}
	return newGoModule(dir, pkgs), ""
}

// moduleDir is the directory of the nearest go.mod at or above abs's directory, within
// root, or "".
func moduleDir(root, abs string) string {
	dir := filepath.Dir(abs)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		if dir == root || !strings.HasPrefix(dir, root) {
			return ""
		}
		dir = filepath.Dir(dir)
	}
}

func relDir(root, dir string) string {
	r, err := filepath.Rel(root, dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(r)
}

// place finds the package that compiles or embeds abs. A reason with ok false means the
// path is Go this module cannot bound; no reason means nothing here compiles it.
func (m *goModule) place(abs, modRel string) (hit risk.PackageHit, why string, ok bool) {
	isGo := strings.EqualFold(filepath.Ext(abs), ".go")
	h, found := m.files[abs]
	if !found && isGo {
		if _, err := os.Stat(abs); err == nil {
			return risk.PackageHit{}, "compiled into no package of module " + modRel + " (testdata, or a directory go does not build)", false
		}
		// Deleted: its package's importers are what can break.
		for path, pkg := range m.primary {
			if pkg.Dir == filepath.Dir(abs) {
				h, found = goFileHit{pkg: path, role: goCompiled}, true
				break
			}
		}
		if !found {
			return risk.PackageHit{}, "deleted with its whole package, so no package is left to test its former importers against", false
		}
	}
	if !found {
		return risk.PackageHit{}, "", false
	}
	if h.role == goIgnored {
		return risk.PackageHit{}, "excluded by build constraints on this platform, so its tests cannot run here", false
	}
	if gen, ok := m.generators[h.pkg]; ok {
		return risk.PackageHit{}, h.pkg + " is reachable only from go:generate program " + gen + ", so it is generator code", false
	}
	hit = risk.PackageHit{Package: h.pkg, Module: modRel, TestOnly: h.role == goTestOnly}
	switch {
	case !isGo:
		hit.Why = "embedded by Go package " + h.pkg + " (go:embed)"
	case h.role == goTestOnly:
		hit.Why = "test file of Go package " + h.pkg + ": only that package's tests compile it"
	default:
		hit.Why = "Go package " + h.pkg + ": its tests and every package importing it"
	}
	return hit, "", true
}

// NarrowTest returns a rewrite of one `go test` argv, the `test` subcommand first, that
// keeps only the packages in keep. Flags stay as written; package patterns (`.` when
// there are none) resolve through `go list` in dir under env, and only the matches keep
// names are passed on. When none match, or the patterns cannot be listed, the argv is
// returned as written: running more than the change reaches is sound, and a target body
// that expects every call to leave its output behind (a coverage profile) still finds it.
func NarrowTest(keep []string) func(ctx context.Context, dir string, env map[string]string, args []string) []string {
	return func(ctx context.Context, dir string, env map[string]string, args []string) []string {
		if len(args) == 0 || args[0] != "test" {
			return args
		}
		flags, patterns, tail := splitTestArgs(args[1:])
		if len(patterns) == 0 {
			patterns = []string{"."}
		}
		listed, err := listImportPaths(ctx, dir, env, patterns)
		if err != nil {
			return args
		}
		var kept []string
		for _, pkg := range listed {
			if slices.Contains(keep, pkg) && !slices.Contains(kept, pkg) {
				kept = append(kept, pkg)
			}
		}
		if len(kept) == 0 {
			return args
		}
		return slices.Concat([]string{"test"}, flags, kept, tail)
	}
}

// testValueFlags are the `go test` and build flags whose value may be the next token;
// every other flag is a boolean. An unlisted value flag leaves its value to be read as
// a package pattern, which `go list` then fails on, and the argv runs as written.
var testValueFlags = []string{
	"asmflags", "bench", "benchtime", "blockprofile", "blockprofilerate", "buildmode", "C",
	"compiler", "count", "coverpkg", "covermode", "coverprofile", "cpu", "cpuprofile", "exec",
	"fuzz", "fuzzcachedir", "fuzzminimizetime", "fuzztime", "gccgoflags", "gcflags",
	"installsuffix", "ldflags", "list", "memprofile", "memprofilerate", "mod", "modfile",
	"mutexprofile", "mutexprofilefraction", "o", "outputdir", "overlay", "p", "parallel", "pgo",
	"pkgdir", "run", "shuffle", "skip", "tags", "timeout", "toolexec", "trace", "vet",
}

// splitTestArgs splits a `go test` argv into its flags, its package patterns, and
// everything from `-args` on, which is the test binary's.
func splitTestArgs(args []string) (flags, patterns, tail []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-args" || a == "--args" {
			return flags, patterns, args[i:]
		}
		if !strings.HasPrefix(a, "-") {
			patterns = append(patterns, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		name = strings.TrimPrefix(name, "test.")
		if !strings.Contains(name, "=") && slices.Contains(testValueFlags, name) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return flags, patterns, nil
}

func listImportPaths(ctx context.Context, dir string, env map[string]string, patterns []string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "go", append([]string{"list", "-e", "-f", "{{.ImportPath}}"}, patterns...)...)
	cmd.Dir = dir
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list in %s: %w", dir, err)
	}
	return strings.Fields(string(out)), nil
}

// goPackage is the part of one `go list -json` record the prover reads.
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

// GoList runs `go list -e -test` over ./... in the module rooted at dir. -test is what
// makes each package's test binary (p.test) appear with its full Deps, the only place go
// reports what a package's tests import transitively.
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
	goCompiled goFileRole = iota + 1
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
// package has no files on this platform, so it imports nothing here, and a change to its
// files lands in IgnoredGoFiles, which is unplaced on its own.
const excludedByConstraints = "build constraints exclude all Go files"

func newGoModule(dir string, pkgs []goPackage) *goModule {
	m := &goModule{
		dir:      dir,
		primary:  map[string]goPackage{},
		testDeps: map[string][]string{},
		files:    map[string]goFileHit{},
	}
	for _, p := range pkgs {
		if m.poison == "" {
			if msg := goLoadError(p); msg != "" {
				m.poison = p.ImportPath + ": " + msg
			}
		}
		switch {
		case strings.HasSuffix(p.ImportPath, ".test") && p.Name == "main":
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

// normalizeDeps drops go list's test-variant suffix ("p [q.test]" is p recompiled for
// q's tests) so every dependency is named by its import path.
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

// closure returns every package of this module whose build or tests can observe a
// change: testOnly as given, the changed packages, each package importing one
// transitively, and each package whose test binary does.
func (m *goModule) closure(changed, testOnly []string) []string {
	out := map[string]bool{}
	for _, p := range testOnly {
		if _, ok := m.primary[p]; ok {
			out[p] = true
		}
	}
	touches := func(deps []string) bool {
		return slices.ContainsFunc(deps, func(d string) bool { return slices.Contains(changed, d) })
	}
	for path, p := range m.primary {
		if slices.Contains(changed, path) || touches(p.Deps) {
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
// returns the packages only those programs reach: code whose sole purpose is producing
// generated output. A package a shipped program also links is not generator-only, and
// its effect on generated output is the drift check's to catch.
func generatorOnly(primary map[string]goPackage) map[string]string {
	gens := map[string]bool{}
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
					gens[prog] = true
				}
			}
		}
	}
	out := map[string]string{}
	if len(gens) == 0 {
		return out
	}
	reach := func(roots []string) map[string]string {
		got := map[string]string{}
		for _, r := range roots {
			got[r] = r
			for _, d := range primary[r].Deps {
				if _, ok := primary[d]; ok {
					if _, seen := got[d]; !seen {
						got[d] = r
					}
				}
			}
		}
		return got
	}
	var genRoots, otherMains []string
	for path, p := range primary {
		switch {
		case gens[path]:
			genRoots = append(genRoots, path)
		case p.Name == "main":
			otherMains = append(otherMains, path)
		}
	}
	slices.Sort(genRoots)
	shipped := reach(otherMains)
	for pkg, prog := range reach(genRoots) {
		if _, ok := shipped[pkg]; !ok {
			out[pkg] = prog
		}
	}
	return out
}

// goGenerateRuns returns the package argument of every `//go:generate go run` directive
// in file. An unreadable file yields none: the file itself is still placed by its
// package.
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
// comments and positions are ignored, and carry the same directive comments. That covers
// comment-only and gofmt-only edits. Directives (//go:build, //go:embed, cgo's preamble,
// and the rest the go spell declares) change what compiles, so they compare as code. A
// source that does not parse is never equivalent.
func goEquivalent(old, cur string, directives []string) bool {
	fo, err := parseGo(old)
	if err != nil {
		return false
	}
	fc, err := parseGo(cur)
	if err != nil {
		return false
	}
	return slices.Equal(goDirectives(fo, directives), goDirectives(fc, directives)) &&
		astEqual(reflect.ValueOf(fo), reflect.ValueOf(fc))
}

func parseGo(src string) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), "", src, parser.ParseComments|parser.SkipObjectResolution)
}

func goDirectives(f *ast.File, directives []string) []string {
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
	for _, g := range f.Comments {
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
	fileType         = reflect.TypeFor[ast.File]()
)

// astEqual compares two syntax trees field by field, skipping positions, comment fields
// and the resolver's derived fields. ast.File's Imports and Unresolved derive from
// Decls and would only compare what Decls already does.
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
		isFile := a.Type() == fileType
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
