package goindex

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/build"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"golang.org/x/mod/modfile"
	"golang.org/x/sync/errgroup"
	"golang.org/x/tools/go/packages"

	"github.com/egladman/magus/internal/symbols"
)

// formatVersion changes whenever a shard this indexer writes would come out different
// for the same source, so a pack written before is rebuilt whole.
const formatVersion = "goindex 4"

// scheme is the SCIP scheme of every symbol the indexer names. Keys drop it with the
// version, so a key matches the one scip-go named the same symbol by.
const scheme = "magus"

// workspaceVersion stands in for the workspace module's version in every symbol: a
// revision there would rename every symbol on every commit and stop shards built at two
// commits from resolving into each other.
const workspaceVersion = "."

const stdlibModule = "github.com/golang/go/src"

const loadMode = packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles | packages.NeedImports |
	packages.NeedModule | packages.NeedTypes | packages.NeedSyntax | packages.NeedTypesInfo

// Indexer indexes the Go packages Patterns match under Dir, one unit per package, with
// go/packages and go/types in this process; dependencies come from export data. Builds
// are the configurations besides the host's the index covers ({"GOOS": "windows"}): each
// loads only the units with a file its constraints select and the host's exclude.
// A package the host excludes whole counts.
// Index takes units from the same Indexer's last Units call. Not safe for concurrent use.
type Indexer struct {
	Dir      string
	Patterns []string
	Env      []string
	Builds   []map[string]string

	goVersion string
	listed    map[string]listedUnit
}

type listedUnit struct {
	load    string
	ignored []string
	dir     string
}

// Version is formatVersion and the toolchain's version, which names standard library
// symbols.
// It holds Builds too: a shard kept across a new build lacks its files.
func (ix *Indexer) Version() string {
	builds := make([]string, 0, len(ix.Builds))
	for _, overlay := range ix.Builds {
		builds = append(builds, formatOverlay(overlay))
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s %s", formatVersion, ix.toolchain(), strings.Join(builds, ",")))
}

func (ix *Indexer) toolchain() string {
	if ix.goVersion == "" {
		cmd := exec.Command("go", "env", "GOVERSION")
		cmd.Dir, cmd.Env = ix.Dir, ix.env(nil)
		out, err := cmd.Output()
		ix.goVersion = strings.TrimSpace(string(out))
		if err != nil || ix.goVersion == "" {
			ix.goVersion = "unknown"
		}
	}
	return ix.goVersion
}

func (ix *Indexer) env(overlay map[string]string) []string {
	env := slices.Clone(ix.Env)
	if env == nil {
		env = os.Environ()
	}
	for _, k := range sortedKeys(overlay) {
		env = append(env, fmt.Sprintf("%s=%s", k, overlay[k]))
	}
	return env
}

func (ix *Indexer) patterns() []string {
	if len(ix.Patterns) == 0 {
		return []string{"./..."}
	}
	return ix.Patterns
}

// Units lists every package Patterns match as a unit: its in-package test variant when it
// has one, which holds every file the plain package does, and its external test package
// as a unit of its own. A unit's digest covers its files' paths and bytes, the files the
// host's build constraints exclude included, so an edit to a _windows.go file on a Mac
// moves it too.
// `./...` skips a package the host excludes whole, so each of Builds lists too.
func (ix *Indexer) Units(ctx context.Context) ([]symbols.Unit, error) {
	overlays := slices.Concat([]map[string]string{nil}, ix.Builds)
	listings := make([][]*packages.Package, len(overlays))
	g, gctx := errgroup.WithContext(ctx)
	for i, overlay := range overlays {
		g.Go(func() error {
			cfg := &packages.Config{
				Context: gctx, Dir: ix.Dir, Env: ix.env(overlay), Tests: true,
				Mode: packages.NeedName | packages.NeedFiles | packages.NeedImports,
			}
			pkgs, err := packages.Load(cfg, ix.patterns()...)
			if err != nil {
				return fmt.Errorf("go list in %s: %w", ix.Dir, err)
			}
			listings[i] = pkgs
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	ix.listed = map[string]listedUnit{}
	var units []symbols.Unit
	for i, overlay := range overlays {
		for _, p := range chooseVariants(listings[i]) {
			if _, ok := ix.listed[p.PkgPath]; ok {
				continue
			}
			files := slices.Concat(p.GoFiles, goOnly(p.IgnoredFiles))
			digest, err := ix.digest(files)
			if err != nil {
				return nil, err
			}
			var deps []string
			for _, imp := range p.Imports {
				deps = append(deps, importPath(imp.ID))
			}
			slices.Sort(deps)
			units = append(units, symbols.Unit{ID: p.PkgPath, Digest: digest, Deps: slices.Compact(deps)})
			ignored := goOnly(p.IgnoredFiles)
			if overlay != nil {
				ignored = files
			}
			ix.listed[p.PkgPath] = listedUnit{load: loadPath(p), ignored: ignored, dir: packageDir(p)}
		}
	}
	slices.SortFunc(units, func(a, b symbols.Unit) int { return strings.Compare(a.ID, b.ID) })
	return units, nil
}

// chooseVariants keeps one package per package path: the in-package test variant over
// the plain package, and never the generated test main.
func chooseVariants(pkgs []*packages.Package) []*packages.Package {
	byPath := map[string]*packages.Package{}
	for _, p := range pkgs {
		if p.Name == "main" && strings.HasSuffix(p.PkgPath, ".test") {
			continue
		}
		if prev, ok := byPath[p.PkgPath]; ok && prev.ID != prev.PkgPath {
			continue
		}
		byPath[p.PkgPath] = p
	}
	out := make([]*packages.Package, 0, len(byPath))
	for _, path := range sortedKeys(byPath) {
		out = append(out, byPath[path])
	}
	return out
}

// importPath is the package path a package ID names: "a/b [a/b.test]" is a/b.
func importPath(id string) string {
	path, _, _ := strings.Cut(id, " ")
	return path
}

// loadPath is the pattern that loads p again: an external test package loads through the
// package it tests.
func loadPath(p *packages.Package) string {
	if base, ok := strings.CutSuffix(p.PkgPath, "_test"); ok && p.ID != p.PkgPath {
		return base
	}
	return p.PkgPath
}

func packageDir(p *packages.Package) string {
	if len(p.GoFiles) > 0 {
		return filepath.Dir(p.GoFiles[0])
	}
	if len(p.IgnoredFiles) > 0 {
		return filepath.Dir(p.IgnoredFiles[0])
	}
	return ""
}

func goOnly(files []string) []string {
	var out []string
	for _, f := range files {
		if strings.HasSuffix(f, ".go") {
			out = append(out, f)
		}
	}
	return out
}

func (ix *Indexer) digest(files []string) (string, error) {
	files = slices.Clone(files)
	slices.Sort(files)
	h := sha256.New()
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(data)
		rel, _ := filepath.Rel(ix.Dir, f)
		fmt.Fprintf(h, "%s\x00%x\n", filepath.ToSlash(rel), sum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Index type-checks the units asked for and returns their shards. Units must come from
// this Indexer's last Units call. A package with type errors is indexed as far as the
// checker got, as gopls does; only a failed load is an error.
func (ix *Indexer) Index(ctx context.Context, units []symbols.Unit) ([]symbols.Shard, error) {
	want := make(map[string]bool, len(units))
	for _, u := range units {
		want[u.ID] = true
	}
	shards, err := ix.indexBuild(ctx, want, nil)
	if err != nil {
		return nil, err
	}
	byUnit := make(map[string]int, len(shards))
	for i := range shards {
		byUnit[shards[i].Unit] = i
	}
	for _, overlay := range ix.Builds {
		tagged := map[string]bool{}
		for id := range want {
			if ix.selectsExcluded(id, overlay) {
				tagged[id] = true
			}
		}
		if len(tagged) == 0 {
			continue
		}
		extra, err := ix.indexBuild(ctx, tagged, overlay)
		if err != nil {
			return nil, fmt.Errorf("under %s: %w", formatOverlay(overlay), err)
		}
		for _, s := range extra {
			if i, ok := byUnit[s.Unit]; ok {
				mergeShard(&shards[i], s)
				continue
			}
			byUnit[s.Unit] = len(shards)
			shards = append(shards, s)
		}
	}
	return shards, nil
}

// selectsExcluded reports whether overlay's build configuration selects a file of unit
// that the host's excludes.
func (ix *Indexer) selectsExcluded(unit string, overlay map[string]string) bool {
	lu := ix.listed[unit]
	if len(lu.ignored) == 0 {
		return false
	}
	bctx := build.Default
	if v := overlay["GOOS"]; v != "" {
		bctx.GOOS = v
	}
	if v := overlay["GOARCH"]; v != "" {
		bctx.GOARCH = v
	}
	if bctx.GOOS == build.Default.GOOS && bctx.GOARCH == build.Default.GOARCH {
		return false
	}
	for _, f := range lu.ignored {
		if ok, err := bctx.MatchFile(filepath.Dir(f), filepath.Base(f)); err == nil && ok {
			return true
		}
	}
	return false
}

func formatOverlay(overlay map[string]string) string {
	parts := make([]string, 0, len(overlay))
	for _, k := range sortedKeys(overlay) {
		parts = append(parts, fmt.Sprintf("%s=%s", k, overlay[k]))
	}
	return strings.Join(parts, " ")
}

// mergeShard adds extra's documents for files host lacks, and its method sets for types
// host does not define. A file both configurations compile keeps host's copy: its
// occurrences are the same in each.
func mergeShard(host *symbols.Shard, extra symbols.Shard) {
	have := map[string]bool{}
	for _, d := range host.Documents {
		have[d.Path] = true
	}
	for _, d := range extra.Documents {
		if !have[d.Path] {
			host.Documents = append(host.Documents, d)
		}
	}
	slices.SortFunc(host.Documents, func(a, b symbols.Document) int { return strings.Compare(a.Path, b.Path) })
	sets := map[string]bool{}
	for _, ms := range host.MethodSets {
		sets[ms.Symbol] = true
	}
	for _, ms := range extra.MethodSets {
		if !sets[ms.Symbol] {
			host.MethodSets = append(host.MethodSets, ms)
		}
	}
}

func (ix *Indexer) indexBuild(ctx context.Context, want map[string]bool, overlay map[string]string) ([]symbols.Shard, error) {
	var loads []string
	for id := range want {
		lu, ok := ix.listed[id]
		if !ok {
			return nil, fmt.Errorf("unit %q was not listed by Units", id)
		}
		loads = append(loads, lu.load)
	}
	slices.Sort(loads)
	cfg := &packages.Config{Context: ctx, Dir: ix.Dir, Env: ix.env(overlay), Tests: true, Mode: loadMode}
	pkgs, err := packages.Load(cfg, slices.Compact(loads)...)
	if err != nil {
		return nil, fmt.Errorf("load packages in %s: %w", ix.Dir, err)
	}
	var chosen []*packages.Package
	for _, p := range chooseVariants(pkgs) {
		if want[p.PkgPath] {
			chosen = append(chosen, p)
		}
	}
	mods := ix.modules(chosen)
	shards := make([]symbols.Shard, len(chosen))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(runtime.GOMAXPROCS(0))
	for i, p := range chosen {
		g.Go(func() error {
			if err := gctx.Err(); err != nil {
				return err
			}
			shards[i] = newWalker(ix.Dir, p, mods).shard()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return shards, nil
}

// moduleTable resolves a package path to the module that provides it, for the package
// part of a symbol.
type moduleTable struct {
	goVersion string
	// paths is sorted longest first, so the first prefix match is the innermost module.
	paths     []string
	versions  map[string]string
	workspace map[string]bool
}

func (ix *Indexer) modules(pkgs []*packages.Package) *moduleTable {
	t := &moduleTable{versions: map[string]string{}, workspace: map[string]bool{}}
	t.goVersion = ix.toolchain()
	seenGoMod := map[string]bool{}
	for _, p := range pkgs {
		m := p.Module
		if m == nil {
			continue
		}
		t.workspace[m.Path] = true
		t.versions[m.Path] = workspaceVersion
		if m.GoMod == "" || seenGoMod[m.GoMod] {
			continue
		}
		seenGoMod[m.GoMod] = true
		data, err := os.ReadFile(m.GoMod)
		if err != nil {
			continue
		}
		mf, err := modfile.ParseLax(m.GoMod, data, nil)
		if err != nil {
			continue
		}
		for _, r := range mf.Require {
			if _, ok := t.versions[r.Mod.Path]; !ok {
				t.versions[r.Mod.Path] = r.Mod.Version
			}
		}
	}
	for path := range t.versions {
		t.paths = append(t.paths, path)
	}
	slices.SortFunc(t.paths, func(a, b string) int {
		if len(a) != len(b) {
			return len(b) - len(a)
		}
		return strings.Compare(a, b)
	})
	return t
}

// resolve is the module name and version a package's symbols carry. The standard library
// is named as scip-go names it; a path no module claims names itself.
func (t *moduleTable) resolve(pkgPath string) (string, string) {
	for _, m := range t.paths {
		if pkgPath == m || strings.HasPrefix(pkgPath, m+"/") {
			return m, t.versions[m]
		}
	}
	first, _, _ := strings.Cut(pkgPath, "/")
	if !strings.Contains(first, ".") {
		return stdlibModule, t.goVersion
	}
	return pkgPath, workspaceVersion
}

// walker indexes one package. Its caches are its own, so walkers run in parallel.
type walker struct {
	root    string
	pkg     *packages.Package
	mods    *moduleTable
	syms    map[types.Object]string
	owners  map[*types.Var][]*scip.Descriptor
	scanned map[*types.Package]bool
	qual    types.Qualifier
}

func newWalker(root string, p *packages.Package, mods *moduleTable) *walker {
	return &walker{
		root: root, pkg: p, mods: mods,
		syms:    map[types.Object]string{},
		owners:  map[*types.Var][]*scip.Descriptor{},
		scanned: map[*types.Package]bool{},
		qual:    packageNameQualifier(p.Types),
	}
}

// packageNameQualifier spells another package's type by its package name, cache.Limiter
// rather than its import path, as scip-go did: the graph's signature readers match a
// parameter's type against the interfaces a file names by that spelling.
func packageNameQualifier(self *types.Package) types.Qualifier {
	return func(p *types.Package) string {
		if p == self {
			return ""
		}
		return p.Name()
	}
}

func (w *walker) shard() symbols.Shard {
	s := symbols.Shard{Unit: w.pkg.PkgPath}
	if w.pkg.Types == nil || w.pkg.TypesInfo == nil {
		return s
	}
	defFile := w.packageDocFile()
	for i, f := range w.pkg.Syntax {
		if i >= len(w.pkg.CompiledGoFiles) {
			break
		}
		rel, err := filepath.Rel(w.root, w.pkg.CompiledGoFiles[i])
		if err != nil || !filepath.IsLocal(rel) {
			continue
		}
		s.Documents = append(s.Documents, w.document(filepath.ToSlash(rel), f, i == defFile))
	}
	slices.SortFunc(s.Documents, func(a, b symbols.Document) int { return strings.Compare(a.Path, b.Path) })
	s.MethodSets = w.methodSets()
	return s
}

// packageDocFile is the file that carries the package symbol's information: the first with
// a package doc comment, else the first by name. Every file's package clause defines the
// symbol, as scip-go's did, since a file belongs to the package its clause defines.
func (w *walker) packageDocFile() int {
	best := -1
	for i, f := range w.pkg.Syntax {
		if i >= len(w.pkg.CompiledGoFiles) {
			break
		}
		if f.Doc != nil {
			return i
		}
		if best < 0 || w.pkg.CompiledGoFiles[i] < w.pkg.CompiledGoFiles[best] {
			best = i
		}
	}
	return best
}

// declInfo is what the declaration around a defining identifier contributes: the node
// whose extent is the definition's body, and its doc comment.
type declInfo struct {
	node ast.Node
	doc  *ast.CommentGroup
}

func declarations(f *ast.File) map[*ast.Ident]declInfo {
	out := map[*ast.Ident]declInfo{}
	var fields func(list *ast.FieldList)
	fields = func(list *ast.FieldList) {
		if list == nil {
			return
		}
		for _, fld := range list.List {
			for _, n := range fld.Names {
				out[n] = declInfo{node: fld, doc: fld.Doc}
			}
			switch t := fld.Type.(type) {
			case *ast.StructType:
				fields(t.Fields)
			case *ast.InterfaceType:
				fields(t.Methods)
			}
		}
	}
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			out[d.Name] = declInfo{node: d, doc: d.Doc}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				node, doc := ast.Node(spec), (*ast.CommentGroup)(nil)
				if !d.Lparen.IsValid() {
					node, doc = d, d.Doc
				}
				switch sp := spec.(type) {
				case *ast.TypeSpec:
					out[sp.Name] = declInfo{node: node, doc: firstDoc(sp.Doc, doc)}
					switch t := sp.Type.(type) {
					case *ast.StructType:
						fields(t.Fields)
					case *ast.InterfaceType:
						fields(t.Methods)
					}
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						out[n] = declInfo{node: node, doc: firstDoc(sp.Doc, doc)}
					}
				}
			}
		}
	}
	return out
}

func firstDoc(docs ...*ast.CommentGroup) *ast.CommentGroup {
	for _, d := range docs {
		if d != nil {
			return d
		}
	}
	return nil
}

func (w *walker) document(rel string, f *ast.File, describesPackage bool) symbols.Document {
	doc := symbols.Document{Path: rel, Language: "go"}
	info := w.pkg.TypesInfo
	decls := declarations(f)
	pkgSym := w.packageSymbol(w.pkg.Types)
	doc.Occurrences = append(doc.Occurrences, symbols.Occurrence{Symbol: pkgSym, Range: w.span(f.Name.Pos(), f.Name.End()), Roles: int32(scip.SymbolRole_Definition)})
	if describesPackage {
		si := symbols.SymbolInfo{Symbol: pkgSym, DisplayName: w.pkg.Types.Name(), Kind: int32(scip.SymbolInformation_Package)}
		if f.Doc != nil {
			si.Documentation = []string{strings.TrimSpace(f.Doc.Text())}
		}
		doc.Symbols = append(doc.Symbols, si)
	}
	for _, d := range f.Decls {
		ast.Inspect(d, w.visit(&doc, decls, info))
	}
	for _, imp := range f.Imports {
		if pn := info.PkgNameOf(imp); pn != nil {
			doc.Occurrences = append(doc.Occurrences, symbols.Occurrence{Symbol: w.packageSymbol(pn.Imported()), Range: w.span(imp.Path.Pos(), imp.Path.End())})
		}
	}
	slices.SortStableFunc(doc.Occurrences, func(a, b symbols.Occurrence) int { return compareRange(a.Range, b.Range) })
	return doc
}

func (w *walker) visit(doc *symbols.Document, decls map[*ast.Ident]declInfo, info *types.Info) func(ast.Node) bool {
	return func(n ast.Node) bool {
		// An import's path and alias are recorded once, from f.Imports.
		if _, ok := n.(*ast.ImportSpec); ok {
			return false
		}
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		if obj := info.Defs[id]; obj != nil {
			if sym := w.symbol(obj); sym != "" {
				occ := symbols.Occurrence{Symbol: sym, Range: w.span(id.Pos(), id.End()), Roles: int32(scip.SymbolRole_Definition)}
				d := decls[id]
				if d.node != nil {
					occ.Enclosing = w.span(d.node.Pos(), d.node.End())
				}
				doc.Occurrences = append(doc.Occurrences, occ)
				doc.Symbols = append(doc.Symbols, w.symbolInfo(obj, sym, d.doc))
			}
		}
		if obj := info.Uses[id]; obj != nil {
			if sym := w.symbol(obj); sym != "" {
				doc.Occurrences = append(doc.Occurrences, symbols.Occurrence{Symbol: sym, Range: w.span(id.Pos(), id.End())})
			}
		}
		return true
	}
}

func compareRange(a, b []int32) int {
	for i := 0; i < 2 && i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

// span is SCIP's packed range for [pos, end): three elements on one line, four across
// lines. Columns are UTF-8 byte offsets.
func (w *walker) span(pos, end token.Pos) []int32 {
	fset := w.pkg.Fset
	s, e := fset.Position(pos), fset.Position(end)
	if s.Line == e.Line {
		return []int32{int32(s.Line - 1), int32(s.Column - 1), int32(e.Column - 1)}
	}
	return []int32{int32(s.Line - 1), int32(s.Column - 1), int32(e.Line - 1), int32(e.Column - 1)}
}

func (w *walker) packageSymbol(p *types.Package) string {
	return w.format(p, nil)
}

func (w *walker) format(p *types.Package, descs []*scip.Descriptor) string {
	name, version := w.mods.resolve(p.Path())
	all := append([]*scip.Descriptor{{Name: p.Path(), Suffix: scip.Descriptor_Namespace}}, descs...)
	return scip.VerboseSymbolFormatter.FormatSymbol(&scip.Symbol{
		Scheme:      scheme,
		Package:     &scip.Package{Manager: "gomod", Name: name, Version: version},
		Descriptors: all,
	})
}

// symbol is obj's global SCIP symbol, or "" for what has none: locals, parameters, type
// parameters, labels, builtins, and fields of a struct no named type declares.
func (w *walker) symbol(obj types.Object) string {
	if sym, ok := w.syms[obj]; ok {
		return sym
	}
	sym := w.computeSymbol(obj)
	w.syms[obj] = sym
	return sym
}

func (w *walker) computeSymbol(obj types.Object) string {
	if obj.Pkg() == nil {
		return ""
	}
	pkgLevel := obj.Parent() == obj.Pkg().Scope()
	switch o := obj.(type) {
	case *types.PkgName:
		return w.packageSymbol(o.Imported())
	case *types.Func:
		return w.funcSymbol(o.Origin(), pkgLevel)
	case *types.TypeName:
		if !pkgLevel {
			return ""
		}
		return w.format(o.Pkg(), []*scip.Descriptor{{Name: o.Name(), Suffix: scip.Descriptor_Type}})
	case *types.Var:
		o = o.Origin()
		if o.IsField() {
			w.scanFields(o.Pkg())
			descs, ok := w.owners[o]
			if !ok {
				return ""
			}
			return w.format(o.Pkg(), descs)
		}
		if !pkgLevel {
			return ""
		}
		return w.format(o.Pkg(), []*scip.Descriptor{{Name: o.Name(), Suffix: scip.Descriptor_Term}})
	case *types.Const:
		if !pkgLevel {
			return ""
		}
		return w.format(o.Pkg(), []*scip.Descriptor{{Name: o.Name(), Suffix: scip.Descriptor_Term}})
	}
	return ""
}

// funcSymbol is fn's symbol: a package-level function's, or a method's under its
// receiver type, where an interface method is a term as scip-go named it.
func (w *walker) funcSymbol(fn *types.Func, pkgLevel bool) string {
	recv := fn.Signature().Recv()
	if recv == nil {
		if !pkgLevel {
			return ""
		}
		return w.format(fn.Pkg(), []*scip.Descriptor{{Name: fn.Name(), Suffix: scip.Descriptor_Method}})
	}
	named := receiverNamed(recv.Type())
	if named == nil {
		return ""
	}
	suffix := scip.Descriptor_Method
	if types.IsInterface(named) {
		suffix = scip.Descriptor_Term
	}
	return w.format(fn.Pkg(), []*scip.Descriptor{
		{Name: named.Obj().Name(), Suffix: scip.Descriptor_Type},
		{Name: fn.Name(), Suffix: suffix},
	})
}

func receiverNamed(t types.Type) *types.Named {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, _ := types.Unalias(t).(*types.Named)
	if named == nil {
		return nil
	}
	return named.Origin()
}

// scanFields records the owner path of every field of p's named struct types, nested
// anonymous structs included (Outer#field.inner), and of a package-level variable's
// anonymous struct (v:field.), the descriptors scip-go gave each.
func (w *walker) scanFields(p *types.Package) {
	if w.scanned[p] {
		return
	}
	w.scanned[p] = true
	var walk func(st *types.Struct, prefix []*scip.Descriptor)
	walk = func(st *types.Struct, prefix []*scip.Descriptor) {
		for i := range st.NumFields() {
			f := st.Field(i)
			descs := append(slices.Clone(prefix), &scip.Descriptor{Name: f.Name(), Suffix: scip.Descriptor_Term})
			w.owners[f] = descs
			if inner, ok := f.Type().(*types.Struct); ok {
				walk(inner, descs)
			}
		}
	}
	scope := p.Scope()
	for _, name := range scope.Names() {
		switch obj := scope.Lookup(name).(type) {
		case *types.TypeName:
			if st, ok := obj.Type().Underlying().(*types.Struct); ok && !obj.IsAlias() {
				walk(st, []*scip.Descriptor{{Name: obj.Name(), Suffix: scip.Descriptor_Type}})
			}
		case *types.Var:
			if st, ok := obj.Type().(*types.Struct); ok {
				walk(st, []*scip.Descriptor{{Name: obj.Name(), Suffix: scip.Descriptor_Meta}})
			}
		}
	}
}

func (w *walker) symbolInfo(obj types.Object, sym string, doc *ast.CommentGroup) symbols.SymbolInfo {
	si := symbols.SymbolInfo{Symbol: sym, DisplayName: obj.Name(), Kind: int32(objectKind(obj)), Signature: w.signature(obj)}
	if doc != nil {
		if text := strings.TrimSpace(doc.Text()); text != "" {
			si.Documentation = []string{text}
		}
	}
	return si
}

func objectKind(obj types.Object) scip.SymbolInformation_Kind {
	switch o := obj.(type) {
	case *types.Func:
		if recv := o.Signature().Recv(); recv != nil {
			if types.IsInterface(recv.Type()) {
				return scip.SymbolInformation_MethodSpecification
			}
			return scip.SymbolInformation_Method
		}
		return scip.SymbolInformation_Function
	case *types.TypeName:
		if o.IsAlias() {
			return scip.SymbolInformation_TypeAlias
		}
		switch o.Type().Underlying().(type) {
		case *types.Struct:
			return scip.SymbolInformation_Struct
		case *types.Interface:
			return scip.SymbolInformation_Interface
		}
		return scip.SymbolInformation_Type
	case *types.Var:
		if o.IsField() {
			return scip.SymbolInformation_Field
		}
		return scip.SymbolInformation_Variable
	case *types.Const:
		return scip.SymbolInformation_Constant
	}
	return scip.SymbolInformation_UnspecifiedKind
}

// signature renders obj's declaration the way scip-go v0.2.7 did, which the graph's
// declaration readers (internal/graph/knowledge naming_shape.go) parse: types.ObjectString,
// a field prefixed "struct field" with its tags in backquotes, and a type's struct or
// interface gofmt-aligned one member per line, an interface naming only what it declares.
func (w *walker) signature(obj types.Object) string {
	switch o := obj.(type) {
	case *types.TypeName:
		return w.typeDeclaration(o)
	case *types.Var:
		if o.IsField() {
			return "struct " + backquoteTags(types.ObjectString(o, w.qual))
		}
	case *types.Const:
		return fmt.Sprintf("%s = %s", types.ObjectString(o, w.qual), o.Val())
	}
	return types.ObjectString(obj, w.qual)
}

// typeDeclaration is o's declaration; a type's own type parameters are left out, and an
// alias's are kept, as scip-go wrote them.
func (w *walker) typeDeclaration(o *types.TypeName) string {
	alias, ok := o.Type().(*types.Alias)
	if !ok || !o.IsAlias() {
		return fmt.Sprintf("type %s %s", o.Name(), w.expand(o.Type().Underlying()))
	}
	lhs := o.Name()
	if tps := alias.TypeParams(); tps.Len() > 0 {
		parts := make([]string, 0, tps.Len())
		for i := range tps.Len() {
			parts = append(parts, fmt.Sprintf("%s %s", tps.At(i).Obj().Name(), tps.At(i).Constraint()))
		}
		lhs = fmt.Sprintf("%s[%s]", lhs, strings.Join(parts, ", "))
	}
	switch rhs := alias.Rhs(); rhs.(type) {
	case *types.Alias, *types.Named:
		return fmt.Sprintf("type %s = %s", lhs, types.TypeString(rhs, w.qual))
	default:
		return fmt.Sprintf("type %s = %s", lhs, w.expand(rhs))
	}
}

// expand is t's type expression, a struct or interface run through gofmt with tabs as four
// spaces; anything else, or one gofmt refuses, as types.TypeString spells it.
func (w *walker) expand(t types.Type) string {
	raw := types.TypeString(t, w.qual)
	switch t.(type) {
	case *types.Struct, *types.Interface:
	default:
		return raw
	}
	src, err := format.Source(fmt.Appendf(nil, "package p\n\ntype _ %s\n", raw))
	if err != nil {
		return raw
	}
	decl := strings.TrimSpace(strings.TrimPrefix(string(src), "package p\n\n"))
	return strings.ReplaceAll(strings.TrimPrefix(decl, "type _ "), "\t", "    ")
}

// backquoteTags rewrites each double-quoted string in s, a field's struct tag, in
// backquotes; one that does not unquote stays as it was.
func backquoteTags(s string) string {
	var b strings.Builder
	for {
		start := strings.IndexByte(s, '"')
		if start < 0 {
			return b.String() + s
		}
		end := closingQuote(s, start)
		if end < 0 {
			return b.String() + s
		}
		b.WriteString(s[:start])
		if unquoted, err := strconv.Unquote(s[start : end+1]); err == nil {
			fmt.Fprintf(&b, "`%s`", unquoted)
		} else {
			b.WriteString(s[start : end+1])
		}
		s = s[end+1:]
	}
}

// closingQuote is the index of the double quote closing the one at s[open], or -1.
func closingQuote(s string, open int) int {
	for j := open + 1; j < len(s); j++ {
		switch s[j] {
		case '\\':
			j++
		case '"':
			return j
		}
	}
	return -1
}

// methodSets is every non-generic named type of the package with the methods its pointer
// type has, and every interface whose methods alone define it. Unexported method names
// carry their package path: two packages' unexported methods of one name never match.
func (w *walker) methodSets() []symbols.MethodSet {
	var out []symbols.MethodSet
	scope := w.pkg.Types.Scope()
	for _, name := range scope.Names() {
		tn, ok := scope.Lookup(name).(*types.TypeName)
		if !ok || tn.IsAlias() {
			continue
		}
		named, ok := tn.Type().(*types.Named)
		if !ok || named.TypeParams().Len() > 0 {
			continue
		}
		ms := symbols.MethodSet{Symbol: w.symbol(tn)}
		if iface, ok := named.Underlying().(*types.Interface); ok {
			if !iface.IsMethodSet() {
				continue
			}
			ms.Interface = true
			for i := range iface.NumMethods() {
				ms.Methods = append(ms.Methods, w.method(iface.Method(i)))
			}
		} else {
			set := types.NewMethodSet(types.NewPointer(named))
			for i := range set.Len() {
				if fn, ok := set.At(i).Obj().(*types.Func); ok {
					ms.Methods = append(ms.Methods, w.method(fn))
				}
			}
		}
		if len(ms.Methods) > 0 {
			out = append(out, ms)
		}
	}
	return out
}

func (w *walker) method(fn *types.Func) symbols.Method {
	name := fn.Name()
	if !fn.Exported() && fn.Pkg() != nil {
		name = fmt.Sprintf("%s.%s", fn.Pkg().Path(), name)
	}
	return symbols.Method{Name: name, Signature: types.TypeString(fn.Signature(), nil), Symbol: w.symbol(fn)}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
