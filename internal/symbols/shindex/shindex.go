package shindex

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/scip-code/scip/bindings/go/scip"
	"mvdan.cc/sh/v3/syntax"

	"github.com/egladman/magus/internal/symbols"
)

// formatVersion changes whenever a shard this indexer writes would come out different
// for the same source, so a pack written before is rebuilt whole.
const formatVersion = "shindex 3"

// scheme is the SCIP scheme of every symbol the indexer names, the Go indexer's too.
const scheme = "magus"

const language = "shell"

// Indexer indexes the shell scripts (*.sh, *.bash) under Dir in this process with
// mvdan.cc/sh, one unit per file. It records functions, the variables a script gives
// global scope, the locals a function declares, and the files a script sources.
// Symbols are named by each file's path relative to WorkspaceRoot, so two projects that
// source one library agree on its symbols. Index takes units from the same Indexer's last
// Units call. Not safe for concurrent use.
type Indexer struct {
	Dir           string
	WorkspaceRoot string

	units map[string]*script // the project's files, by unit ID
}

// Version is formatVersion and the version of the parser it was built with.
func (ix *Indexer) Version() string {
	return fmt.Sprintf("%s mvdan.cc/sh %s", formatVersion, parserVersion())
}

func parserVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "(no build info)"
	}
	for _, dep := range info.Deps {
		if dep.Path == "mvdan.cc/sh/v3" {
			if dep.Replace != nil {
				return dep.Replace.Version
			}
			return dep.Version
		}
	}
	return "(not in build info)"
}

// Units parses every script under Dir and lists each as a unit, ID its slash path
// relative to Dir, whose deps are the project's files it sources. A unit's digest covers
// its own bytes and the names every script it sources, directly or through another,
// defines: a name resolves through those, so one gained or lost there re-indexes it.
func (ix *Indexer) Units(ctx context.Context) ([]symbols.Unit, error) {
	if ix.WorkspaceRoot == "" {
		return nil, fmt.Errorf("shindex: no workspace root to name %s's symbols against", ix.Dir)
	}
	dir, err := filepath.Abs(ix.Dir)
	if err != nil {
		return nil, err
	}
	root, err := filepath.Abs(ix.WorkspaceRoot)
	if err != nil {
		return nil, err
	}
	if _, ok := relative(root, dir); !ok {
		return nil, fmt.Errorf("shindex: %s is outside workspace root %s", dir, root)
	}
	rels, err := discover(dir)
	if err != nil {
		return nil, fmt.Errorf("discover shell scripts: %w", err)
	}
	r := &reader{root: root, scripts: map[string]*script{}}
	ix.units = make(map[string]*script, len(rels))
	for _, rel := range rels {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s := r.load(filepath.Join(dir, filepath.FromSlash(rel)))
		s.unit = rel
		ix.units[rel] = s
	}
	units := make([]symbols.Unit, 0, len(rels))
	for _, rel := range rels {
		units = append(units, ix.units[rel].describe())
	}
	return units, nil
}

// describe is s as a unit: its digest and the project's files it sources.
func (s *script) describe() symbols.Unit {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%x\n", s.unit, sha256.Sum256(s.data))
	var deps []string
	for _, src := range s.sourced {
		if src.target.unit != "" && src.target != s {
			deps = append(deps, src.target.unit)
		}
	}
	for _, t := range s.closure()[1:] {
		fmt.Fprintf(h, "sources %s\x00%s\n", t.rel, strings.Join(t.surface(), "\x00"))
	}
	slices.Sort(deps)
	return symbols.Unit{ID: s.unit, Digest: hex.EncodeToString(h.Sum(nil)), Deps: slices.Compact(deps)}
}

// Index returns each unit's shard: the script's document, or, for a script that did not
// parse, a shard with no document and a gap naming the file.
func (ix *Indexer) Index(ctx context.Context, units []symbols.Unit) ([]symbols.Shard, error) {
	shards := make([]symbols.Shard, 0, len(units))
	for _, u := range units {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, ok := ix.units[u.ID]
		if !ok {
			return nil, fmt.Errorf("unit %q was not listed by Units", u.ID)
		}
		shard := symbols.Shard{Unit: u.ID}
		if s.err != nil {
			shard.Gaps = []string{s.err.Error()}
		} else {
			shard.Documents = []symbols.Document{newWalker(s).document()}
		}
		shards = append(shards, shard)
	}
	return shards, nil
}

// skippedDir reports whether discovery leaves a directory below the root out:
// dot-directories, fixtures and dependency trees, and a nested project, which its own
// magusfile.buzz says is indexed on its own.
func skippedDir(path, name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch name {
	case "testdata", "node_modules", "vendor":
		return true
	}
	_, err := os.Stat(filepath.Join(path, "magusfile.buzz"))
	return err == nil
}

func isScript(name string) bool {
	ext := filepath.Ext(name)
	return ext == ".sh" || ext == ".bash"
}

// discover returns the scripts under root as sorted slash paths relative to it.
func discover(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skippedDir(path, d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !isScript(d.Name()) {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	slices.Sort(files)
	return files, err
}

// relative is abs as a slash path relative to root, and false when it lies outside.
func relative(root, abs string) (string, bool) {
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// reader parses each workspace script once, the ones a project's scripts source from
// outside it included.
type reader struct {
	root    string
	scripts map[string]*script
}

// script is one parsed file and what it defines.
type script struct {
	rel  string // workspace-relative slash path, the namespace of its symbols
	abs  string
	unit string // the unit ID when the file is one of the project's, else ""
	data []byte
	file *syntax.File
	err  error // why it was not read or did not parse

	funcs map[string]bool
	// decisions is each function's decision points, by name.
	decisions map[string]int
	vars      map[string]*varDef
	// locals holds the names each function declares local.
	locals  map[*syntax.FuncDecl]map[string]bool
	sourced []sourced
}

// varDef is a global variable's first assignment in a file, the one that defines it.
type varDef struct {
	at      *syntax.Lit
	variant string // the declaring builtin (export, readonly, declare), "" for a bare assignment
	stmt    *syntax.Stmt
}

// sourced is one `source` or `.` whose path resolves to a workspace script.
type sourced struct {
	word   *syntax.Word
	target *script
}

// load reads and parses the script at abs once. A read or parse failure is kept on the
// script, never returned: it is a gap, and the rest of the project still indexes.
func (r *reader) load(abs string) *script {
	if s, ok := r.scripts[abs]; ok {
		return s
	}
	rel, _ := relative(r.root, abs)
	s := &script{rel: rel, abs: abs, funcs: map[string]bool{}, decisions: map[string]int{}, vars: map[string]*varDef{}, locals: map[*syntax.FuncDecl]map[string]bool{}}
	r.scripts[abs] = s
	s.data, s.err = os.ReadFile(abs)
	if s.err != nil {
		s.err = fmt.Errorf("%s: %w", rel, s.err)
		return s
	}
	parser := syntax.NewParser(syntax.KeepComments(true), syntax.Variant(syntax.LangBash))
	s.file, s.err = parser.Parse(bytes.NewReader(s.data), rel)
	if s.err != nil {
		return s
	}
	r.collect(s)
	return s
}

// collect records what s defines and the scripts it sources.
func (r *reader) collect(s *script) {
	var stack []syntax.Node
	syntax.Walk(s.file, func(n syntax.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		fn := enclosingFunc(stack)
		stack = append(stack, n)
		switch n := n.(type) {
		case *syntax.FuncDecl:
			if n.Name != nil {
				s.funcs[n.Name.Value] = true
				s.decisions[n.Name.Value] = countDecisions(n)
			}
			s.locals[n] = map[string]bool{}
		case *syntax.CallExpr:
			for _, a := range n.Assigns {
				s.collectAssign(a, fn, "", parentStmt(stack))
			}
			if w := sourcePath(n); w != nil {
				if t := r.resolveSource(s, w); t != nil {
					s.sourced = append(s.sourced, sourced{word: w, target: t})
				}
			}
		case *syntax.DeclClause:
			local := isLocal(n, fn)
			for _, a := range n.Args {
				if a.Name == nil {
					continue
				}
				if local {
					s.locals[fn][a.Name.Value] = true
					continue
				}
				s.collectAssign(a, fn, n.Variant.Value, parentStmt(stack))
			}
		case *syntax.WordIter:
			s.collectAssign(&syntax.Assign{Name: n.Name}, fn, "", parentStmt(stack))
		}
		return true
	})
}

// countDecisions counts the branch points in fn's body, nested ones too.
func countDecisions(fn *syntax.FuncDecl) int {
	total := 0
	syntax.Walk(fn.Body, func(n syntax.Node) bool {
		total += decisionWeight(n)
		return true
	})
	return total
}

func decisionWeight(n syntax.Node) int {
	switch n := n.(type) {
	case *syntax.WhileClause, *syntax.ForClause:
		return 1
	case *syntax.IfClause:
		return boolInt(n.Cond != nil)
	case *syntax.CaseClause:
		return len(n.Items)
	}
	return logicalWeight(n)
}

func logicalWeight(n syntax.Node) int {
	switch n := n.(type) {
	case *syntax.BinaryCmd:
		return boolInt(n.Op == syntax.AndStmt || n.Op == syntax.OrStmt)
	case *syntax.BinaryTest:
		return boolInt(n.Op == syntax.AndTest || n.Op == syntax.OrTest)
	}
	return 0
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *script) collectAssign(a *syntax.Assign, fn *syntax.FuncDecl, variant string, stmt *syntax.Stmt) {
	if a.Name == nil {
		return
	}
	name := a.Name.Value
	if fn != nil && s.locals[fn][name] {
		return
	}
	if _, ok := s.vars[name]; !ok {
		s.vars[name] = &varDef{at: a.Name, variant: variant, stmt: stmt}
	}
}

// isLocal reports whether a declaration gives its names function scope: `local`, and a
// `declare` or `typeset` inside a function without -g.
func isLocal(d *syntax.DeclClause, fn *syntax.FuncDecl) bool {
	switch d.Variant.Value {
	case "local":
		return fn != nil
	case "declare", "typeset":
		if fn == nil {
			return false
		}
		for _, a := range d.Args {
			if a.Name == nil && a.Value != nil && strings.HasPrefix(a.Value.Lit(), "-") && strings.Contains(a.Value.Lit(), "g") {
				return false
			}
		}
		return true
	}
	return false
}

func enclosingFunc(stack []syntax.Node) *syntax.FuncDecl {
	for i := len(stack) - 1; i >= 0; i-- {
		if fn, ok := stack[i].(*syntax.FuncDecl); ok {
			return fn
		}
	}
	return nil
}

// parentStmt is the statement the node on top of stack belongs to.
func parentStmt(stack []syntax.Node) *syntax.Stmt {
	for i := len(stack) - 1; i >= 0; i-- {
		if st, ok := stack[i].(*syntax.Stmt); ok {
			return st
		}
	}
	return nil
}

// sourcePath is the path word of a `source path` or `. path` call, or nil.
func sourcePath(c *syntax.CallExpr) *syntax.Word {
	if len(c.Args) < 2 {
		return nil
	}
	switch c.Args[0].Lit() {
	case "source", ".":
		return c.Args[1]
	}
	return nil
}

// resolveSource finds the workspace script a source path names: a literal relative path
// against the sourcing file's directory, and `"$(dirname "$0")/rest"`, an expansion then
// "/rest", as rest under that directory or the nearest one above holding it. Anything
// else is not followed.
func (r *reader) resolveSource(from *script, w *syntax.Word) *script {
	prefix, rest, ok := splitSourcePath(w)
	if !ok {
		return nil
	}
	dir := filepath.Dir(from.abs)
	if !prefix {
		return r.existing(filepath.Join(dir, filepath.FromSlash(rest)))
	}
	for d := dir; ; d = filepath.Dir(d) {
		if _, ok := relative(r.root, d); !ok {
			return nil
		}
		if t := r.existing(filepath.Join(d, filepath.FromSlash(rest))); t != nil {
			return t
		}
		if d == r.root || filepath.Dir(d) == d {
			return nil
		}
	}
}

func (r *reader) existing(abs string) *script {
	if _, ok := relative(r.root, abs); !ok {
		return nil
	}
	if st, err := os.Stat(abs); err != nil || !st.Mode().IsRegular() {
		return nil
	}
	return r.load(abs)
}

// splitSourcePath reads a source path word as an optional leading expansion and the
// literal that follows it. ok is false for a path with an expansion after its literal
// part, an absolute literal path, or no literal at all.
func splitSourcePath(w *syntax.Word) (prefix bool, rest string, ok bool) {
	var lit strings.Builder
	for _, part := range flatten(w.Parts) {
		switch p := part.(type) {
		case *syntax.Lit:
			lit.WriteString(p.Value)
		case *syntax.SglQuoted:
			lit.WriteString(p.Value)
		default:
			if lit.Len() > 0 {
				return false, "", false
			}
			prefix = true
		}
	}
	rest = lit.String()
	if rest == "" {
		return false, "", false
	}
	if prefix {
		rel, found := strings.CutPrefix(rest, "/")
		return true, rel, found && rel != ""
	}
	return false, rest, !strings.HasPrefix(rest, "/")
}

// flatten spreads a double-quoted part's contents into the word's parts.
func flatten(parts []syntax.WordPart) []syntax.WordPart {
	var out []syntax.WordPart
	for _, p := range parts {
		if dq, ok := p.(*syntax.DblQuoted); ok {
			out = append(out, flatten(dq.Parts)...)
			continue
		}
		out = append(out, p)
	}
	return out
}

// closure is s and every script it sources, directly or through another, in the order
// a name resolves through them: breadth first, each once.
func (s *script) closure() []*script {
	out := []*script{s}
	seen := map[*script]bool{s: true}
	for i := 0; i < len(out); i++ {
		for _, src := range out[i].sourced {
			if !seen[src.target] {
				seen[src.target] = true
				out = append(out, src.target)
			}
		}
	}
	return out
}

// surface is the sorted names s defines: what a script sourcing it can resolve there.
func (s *script) surface() []string {
	out := make([]string, 0, len(s.funcs)+len(s.vars))
	for name := range s.funcs {
		out = append(out, name+"()")
	}
	for name := range s.vars {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

func symbolName(rel string, descs ...*scip.Descriptor) string {
	return scip.VerboseSymbolFormatter.FormatSymbol(&scip.Symbol{
		Scheme:      scheme,
		Package:     &scip.Package{Manager: "shell"},
		Descriptors: append([]*scip.Descriptor{{Name: rel, Suffix: scip.Descriptor_Namespace}}, descs...),
	})
}

func funcSymbol(rel, name string) string {
	return symbolName(rel, &scip.Descriptor{Name: name, Suffix: scip.Descriptor_Method})
}

func varSymbol(rel, name string) string {
	return symbolName(rel, &scip.Descriptor{Name: name, Suffix: scip.Descriptor_Term})
}
