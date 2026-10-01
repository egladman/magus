package magus

import (
	"go/ast"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// The scanners below are what conventions_test.go holds this repository to: source-level
// agreements across the tree that no single file owns and no runtime signal reveals. They
// are unexported and nothing outside the tests calls them; they live in a production file
// so the suite that runs them pairs with the code it owns. The regular expressions compile
// on first use, so linking this package costs every binary nothing at init.

// commentRefRe matches a `pkg.Symbol` reference in a Go comment.
//
// Casing is deliberately not a filter. A lowercase helper renamed out from under its
// comment is the exact rot the check exists to catch, so requiring an internal capital
// would make that class permanently invisible to buy tidier output. The noise a loose
// match admits is handled by SCORING it, where a reader can weigh it.
var commentRefRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`\b([a-z][a-z0-9]*)\.([A-Za-z_][A-Za-z0-9_]*)\b`)
})

// buzzNameRe matches a Buzz member as source spells it, with the backslash Buzz uses to
// qualify: `vcs\commit`, and the namespaced `magus\secret.read`.
var buzzNameRe = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`\b([a-z][a-z0-9]*)\\([A-Za-z_][A-Za-z0-9_]*)(\.[A-Za-z_][A-Za-z0-9_]*)?`)
})

// commentRefFailScore is where a scored reference stops being a suspicion and becomes a
// finding. Both rename signatures in symbolIndex.score are worth exactly this on their
// own, so the threshold says: fail when something looks like a rename, never on smell
// alone.
const commentRefFailScore = 5

// commentRef is one `pkg.Symbol` this module does not declare, with the evidence that it
// is a rotted reference rather than prose.
type commentRef struct {
	file  string
	line  int
	pkg   string
	sym   string
	score int
	why   string
}

// symbolIndex is what this module declares, indexed the ways a COMMENT cites it rather
// than the way the compiler resolves it. Prose writes "internal/diff.PatchDigest" and
// "magus.diagnoseDrift"; neither is a Go selector, and both name something real.
type symbolIndex struct {
	byPkg    map[string]map[string]bool // package name and directory name alike
	exported map[string]string          // exported symbol -> its one package, "" when several
	host     map[string]bool            // dotted names the Buzz surface exposes
	files    map[string]bool            // every base filename in the tree
}

func newSymbolIndex() symbolIndex {
	return symbolIndex{
		byPkg:    map[string]map[string]bool{},
		exported: map[string]string{},
		host:     map[string]bool{},
		files:    map[string]bool{},
	}
}

// resolves reports whether this module accounts for the reference at all.
func (ix symbolIndex) resolves(pkg, sym string) bool {
	if ix.host[pkg+"."+sym] || ix.files[pkg+"."+sym] {
		return true
	}
	syms, ours := ix.byPkg[pkg]
	return !ours || syms[sym]
}

// score rates how much an unresolved reference reads like a rename rather than prose,
// and names the evidence. Zero means no evidence at all, which is not reported.
//
// The two rules worth commentRefFailScore each describe one of the two ways a reference
// rots: the symbol MOVED, so its name is still real under exactly one other qualifier, or
// it was RENAMED in place, so its successor sits in the package it names.
//
// Everything else that matched is scored lower and printed rather than enforced. That
// tier is the point of scoring instead of asserting: a lowercase near-miss is worth a
// reader's glance and is not worth a red gate, and deciding which it is needs judgement
// the check does not have.
//
// Both rules were MEASURED loose before they were narrowed, and what narrowed them is
// worth keeping: this is one module with thousands of symbols, so almost any short name
// exists somewhere. Bare `exists elsewhere` accused types.Run and spells.Project, where
// the name is declared in a dozen packages and proves nothing; UNIQUENESS is the part
// that carries the signal. Bare `within two edits` accused cache.immutable of being
// mutable, because two edits is ordinary English distance.
//
// Both then need the same second narrowing, for the same reason: a single exported word
// is a word everyone uses. yaml.Decoder is gopkg.in/yaml.v3's and json.Marshaler is the
// standard library's, and each was accused of belonging to the one local package that
// happens to declare that name. Demanding a COMPOSED name is what separates an identifier
// somebody wrote here from a word two modules were always going to share. It costs the
// single-word renames, which land in the printed tier instead.
//
// Absence from a known Buzz module is deliberately NOT a rule, though it sounds like the
// sharpest one available. MEASURED: it fired on yaml.v3 (a Go import path), std.buzz (a
// filename), vcs.changed_files (an MCP tool name) and magus.inputs (a magusfile key), all
// sharing one identifier space with the module surface. No surface here is knowably
// exhaustive, so "the module lacks it" cannot mean what it appears to.
func (ix symbolIndex) score(pkg, sym string) (int, string) {
	composed := isMultiwordExported(sym)
	if owner := ix.exported[sym]; composed && owner != "" && owner != pkg {
		return commentRefFailScore, "only package " + owner + " declares it, so the qualifier rotted"
	}
	if near, ok := ix.nearest(pkg, sym); ok {
		if composed {
			return commentRefFailScore, "the package declares " + near + ", close enough to be a rename"
		}
		return 3, "the package declares " + near + ", but a one-word near-miss is usually prose"
	}
	if strings.ToLower(sym) != sym {
		return 2, "reads like an identifier, but nothing in the module matches it"
	}
	return 0, ""
}

// externalImports names the packages a file binds from outside this module.
//
// A comment in that file citing one of those names means THAT package: yaml.Decoder in a
// file importing gopkg.in/yaml.v3 is yaml.v3's, however loudly a local package called
// yaml disagrees. Nothing here can check another module's symbols, so those references
// are not judged at all.
func externalImports(f *ast.File) map[string]bool {
	out := map[string]bool{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || strings.HasPrefix(path, "github.com/egladman/magus") {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		name, _, _ = strings.Cut(name, ".") // gopkg.in/yaml.v3 imports as yaml
		if imp.Name != nil {
			name = imp.Name.Name
		}
		out[name] = true
	}
	return out
}

// isMultiwordExported reports whether sym can only be a Go identifier: exported, and
// carrying a second word. One exported word (Merge, Marshaler) is also an English word;
// two (PatchDigest, TrackDependencyWait) is a name somebody composed.
func isMultiwordExported(sym string) bool {
	return ast.IsExported(sym) && strings.ToLower(sym[1:]) != sym[1:]
}

// nearest is a symbol in pkg close enough to sym to be its successor: the same name but
// for case, or within two edits. The length floor keeps short words out, where two edits
// reach most of the dictionary.
func (ix symbolIndex) nearest(pkg, sym string) (string, bool) {
	for cand := range ix.byPkg[pkg] {
		if strings.EqualFold(cand, sym) {
			return cand, true
		}
		if len(sym) >= 6 && len(cand) >= 6 && editDistance(cand, sym) <= 2 {
			return cand, true
		}
	}
	return "", false
}

// editDistance is Levenshtein over bytes, one row at a time.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(min(cur[j-1]+1, prev[j]+1), prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// add records one file's declarations under both its package name and its directory.
func (ix symbolIndex) add(f *ast.File, dir string) {
	keys := []string{f.Name.Name}
	if dir != f.Name.Name {
		keys = append(keys, dir)
	}
	for _, k := range keys {
		if ix.byPkg[k] == nil {
			ix.byPkg[k] = map[string]bool{}
		}
	}
	declare := func(name string) {
		for _, k := range keys {
			ix.byPkg[k][name] = true
		}
		// A name several packages declare is recorded as ambiguous, because the qualifier
		// is only checkable against a symbol that has exactly one home. main is never a
		// home: every command declares Run, and none of them is THE Run.
		if ast.IsExported(name) && f.Name.Name != "main" {
			if owner, seen := ix.exported[name]; !seen {
				ix.exported[name] = f.Name.Name
			} else if owner != f.Name.Name {
				ix.exported[name] = ""
			}
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.FuncDecl:
			declare(node.Name.Name)
		case *ast.TypeSpec:
			declare(node.Name.Name)
		case *ast.ValueSpec:
			for _, name := range node.Names {
				declare(name.Name)
			}
		case *ast.Field:
			// Struct fields and interface methods. Prose cites config.TargetTimeout the
			// same way it cites a function, and the compiler is not what is reading it.
			for _, name := range node.Names {
				declare(name.Name)
			}
		case *ast.BasicLit:
			if node.Kind == token.STRING {
				if lit, err := strconv.Unquote(node.Value); err == nil {
					ix.addBuzzName(lit)
				}
			}
		}
		return true
	})
}

// addBuzzName records the host surface a string literal spells.
//
// Buzz is the other language a comment in this module cites, and the generated signature
// table (internal/langservice/manifest_data.go) writes every member of it. Reading those
// literals is magus's own answer to what Buzz exposes, rather than a guess assembled from
// registration calls.
//
// Comments cite these with a dot where source uses a backslash, so both spellings land in
// one index. A namespace member is cited by its tail alone (`magus\secret.read` reads as
// secret.read), which is why that form is recorded twice.
func (ix symbolIndex) addBuzzName(lit string) {
	if commentRefRe().FindString(lit) == lit {
		ix.host[lit] = true // a binding registered under its own dotted name
		return
	}
	for _, m := range buzzNameRe().FindAllStringSubmatch(lit, -1) {
		module, member, namespaced := m[1], m[2], m[3]
		ix.host[module+"."+member] = true
		if namespaced != "" {
			ix.host[member+namespaced] = true
		}
	}
}

// storeMechanicsSites returns "<path>:<func>" for every function in f that renames a file
// it also creates or writes (a hand-rolled atomic write) or constructs an flock: the
// persistence mechanics internal/file is meant to be the one copy of.
func storeMechanicsSites(path string, f *ast.File) []string {
	local := map[string]string{"os": "", "github.com/gofrs/flock": ""}
	for _, imp := range f.Imports {
		p, _ := strconv.Unquote(imp.Path.Value)
		if _, ok := local[p]; !ok {
			continue
		}
		name := filepath.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		local[p] = name
	}
	var sites []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var writes, renames, flocks bool
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			switch {
			case pkg.Name == local["os"] && slices.Contains([]string{"CreateTemp", "Create", "OpenFile", "WriteFile"}, sel.Sel.Name):
				writes = true
			case pkg.Name == local["os"] && sel.Sel.Name == "Rename":
				renames = true
			case pkg.Name == local["github.com/gofrs/flock"] && sel.Sel.Name == "New":
				flocks = true
			}
			return true
		})
		if (writes && renames) || flocks {
			name := fn.Name.Name
			if fn.Recv != nil && len(fn.Recv.List) == 1 {
				recv := fn.Recv.List[0].Type
				if star, ok := recv.(*ast.StarExpr); ok {
					recv = star.X
				}
				if idx, ok := recv.(*ast.IndexExpr); ok {
					recv = idx.X
				}
				if id, ok := recv.(*ast.Ident); ok {
					name = id.Name + "." + name
				}
			}
			sites = append(sites, path+":"+name)
		}
	}
	return sites
}
