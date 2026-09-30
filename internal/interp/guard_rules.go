package interp

import (
	"maps"
	"path"
	"strings"

	"github.com/egladman/magus/internal/parsecache"
	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/ast"
)

// guardRulesFilterID names guardRulesFilter in bytecode keys. Change it with
// any change to what the filter keeps, so no chunk the old filter compiled is
// served to the new one.
const guardRulesFilterID = "magus-guard-rules/2"

// guardRulesFilter limits an entry magusfile to its magus\guard registrations
// and the names those registrations reach. Descent goes through ast.Inspect, so
// a new node is visible here the day Inspect learns it.
//
// A hook loads the root magusfile on every tool call. The registrations are a
// few calls; the rest of the file imports spells and targets those calls never
// touch. Measured 2026-09-28, that compile was about 300ms of magus shell.
// The module a registration names still loads whole, closures and all.
//
// It fails closed: a statement or import is dropped only when it provably
// cannot register a rule. A program with no registration is left as parsed.
func guardRulesFilter(prog *ast.Program, lookup buzz.ImportLookup) {
	if prog == nil {
		return
	}
	roots := guardRoots(prog.Stmts)
	used := map[int]bool{}
	for i, stmt := range prog.Stmts {
		if _, ok := stmt.(*ast.ImportStmt); ok {
			continue
		}
		if roots.mentioned(stmt) {
			used[i] = true
		}
	}
	if len(used) == 0 {
		return
	}
	// Both directions. A helper the registration calls is kept because the
	// registration names it, and the call that runs that helper is kept because
	// the helper is already kept. One pass misses the call: the function
	// contains the magus\guard call, so it is marked first, and nothing it
	// says names the call site.
	for {
		free := map[string]bool{}
		freeNames(prog.Stmts, used, free)
		declared := declaredNames(prog.Stmts, used)
		added := false
		for i, stmt := range prog.Stmts {
			if used[i] {
				continue
			}
			if _, ok := stmt.(*ast.ImportStmt); ok {
				continue
			}
			if name, ok := declName(stmt); ok && free[name] {
				used[i] = true
				added = true
				continue
			}
			refs := map[string]bool{}
			freeNames([]ast.Node{stmt}, map[int]bool{0: true}, refs)
			for name := range refs {
				if declared[name] {
					used[i] = true
					added = true
					break
				}
			}
		}
		if !added {
			break
		}
	}
	free := map[string]bool{}
	freeNames(prog.Stmts, used, free)
	declared := declaredNames(prog.Stmts, used)
	unresolved := false
	for name := range free {
		if !declared[name] && !importHas(prog.Stmts, name) {
			unresolved = true
			break
		}
	}
	out := make([]ast.Node, 0, len(used)+8)
	for i, stmt := range prog.Stmts {
		if imp, ok := stmt.(*ast.ImportStmt); ok {
			if importUsed(imp, free, unresolved) || mayRegister(imp.Path, lookup, nil, map[string]bool{}) {
				out = append(out, stmt)
			}
			continue
		}
		if used[i] {
			out = append(out, stmt)
		}
	}
	prog.Stmts = out
}

// guardVerdicts are the magus\guard members a rule calls to build or keep its
// answer. Every other member, and the namespace itself passed or aliased, is
// treated as registering one, so a member added later is kept by default.
var guardVerdicts = map[string]bool{
	"allow": true, "advise": true, "deny": true, "once": true, "count": true, "binary": true,
}

// guardRootSet is how a file names the magus\guard namespace: through the
// magus module (bound as magus, an alias, or flat) or through guard itself
// (a selective or flat import).
type guardRootSet struct {
	magus map[string]bool
	guard map[string]bool
}

func guardRoots(stmts []ast.Node) guardRootSet {
	roots := guardRootSet{magus: map[string]bool{"magus": true}, guard: map[string]bool{}}
	for _, stmt := range stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok || strings.TrimPrefix(imp.Path, "buzz:") != "magus" {
			continue
		}
		switch {
		case len(imp.Only) > 0:
			for _, name := range imp.Only {
				if name == "guard" {
					roots.guard["guard"] = true
				}
			}
		case imp.Alias == "_":
			roots.guard["guard"] = true
		case imp.Alias != "":
			roots.magus[imp.Alias] = true
		}
	}
	return roots
}

// mentioned reports whether n reaches magus\guard in a way that can register
// a rule.
func (r guardRootSet) mentioned(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(node ast.Node) bool {
		if found {
			return false
		}
		switch cur := node.(type) {
		case *ast.MemberExpr:
			root, names := splitMember(cur)
			if root == "" {
				return true
			}
			if r.magus[root] && len(names) > 0 && names[0] == "guard" {
				names = names[1:]
			} else if !r.guard[root] {
				return false
			}
			found = len(names) == 0 || !guardVerdicts[names[0]]
			return false
		case *ast.IdentExpr:
			// Bare, the name is being passed or aliased, and what the alias
			// reaches is out of sight.
			found = r.guard[cur.Name] || r.magus[cur.Name]
		}
		return true
	})
	return found
}

// splitMember splits magus\guard.command into ("magus", ["guard", "command"]).
func splitMember(n ast.Node) (string, []string) {
	var names []string
	for {
		m, ok := n.(*ast.MemberExpr)
		if !ok {
			break
		}
		names = append(names, m.Name)
		n = m.Object
	}
	id, ok := n.(*ast.IdentExpr)
	if !ok {
		return "", nil
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return id.Name, names
}

// hostImport reports whether path is served by a host module or resolver
// rather than a file, and so cannot register a rule when it loads. A spell
// loads on the script surface, where a registration raises MGS1022, and a
// project/ handle reads its targets without running them.
func hostImport(p string) bool {
	p = strings.TrimPrefix(p, "buzz:")
	return p == "magus" || strings.HasPrefix(p, "magus/") ||
		strings.HasPrefix(p, "spells/") || strings.HasPrefix(p, "project/")
}

// mayRegister reports whether loading the import at p could register a rule:
// a file that reaches magus\guard, or imports one that does. An import no file
// answers is kept, since the filter cannot see what serves it. via is the
// chain of files the import is nested in; seen breaks import cycles.
func mayRegister(p string, lookup buzz.ImportLookup, via []string, seen map[string]bool) bool {
	if hostImport(p) {
		return false
	}
	file, src, ok := lookup(p, via...)
	if !ok {
		return true
	}
	if seen[file] {
		return false
	}
	seen[file] = true
	prog, err := parsecache.Shared().ParseEmbedded(string(src))
	if err != nil {
		return true
	}
	roots := guardRoots(prog.Stmts)
	nested := append(append([]string(nil), via...), file)
	for _, stmt := range prog.Stmts {
		if imp, isImport := stmt.(*ast.ImportStmt); isImport {
			if mayRegister(imp.Path, lookup, nested, seen) {
				return true
			}
			continue
		}
		if roots.mentioned(stmt) {
			return true
		}
	}
	return false
}

func declName(stmt ast.Node) (string, bool) {
	switch s := stmt.(type) {
	case *ast.FunDecl:
		return s.Name, s.Name != ""
	case *ast.DeclStmt:
		return s.Name, s.Name != ""
	case *ast.ObjectDecl:
		return s.Name, s.Name != ""
	case *ast.EnumDecl:
		return s.Name, s.Name != ""
	default:
		return "", false
	}
}

func declaredNames(stmts []ast.Node, used map[int]bool) map[string]bool {
	declared := map[string]bool{}
	for i, stmt := range stmts {
		if !used[i] {
			continue
		}
		if name, ok := declName(stmt); ok {
			declared[name] = true
		}
	}
	return declared
}

func freeNames(stmts []ast.Node, used map[int]bool, free map[string]bool) {
	for i, stmt := range stmts {
		if !used[i] {
			continue
		}
		collectNames(&scope{free: free, bound: map[string]bool{"this": true}}, stmt)
	}
}

// scope is the set of names bound at a point in the program, plus the free
// names seen under it. free is shared across clones; bound is not.
type scope struct {
	free  map[string]bool
	bound map[string]bool
}

func (sc *scope) clone() *scope {
	return &scope{free: sc.free, bound: maps.Clone(sc.bound)}
}

func (sc *scope) bind(idents ...string) {
	for _, name := range idents {
		if name != "" && name != "_" {
			sc.bound[name] = true
		}
	}
}

func (sc *scope) add(name string) {
	if name == "" || name == "_" || sc.bound[name] {
		return
	}
	sc.free[name] = true
}

// addType records the names a type annotation spells. ns\T reaches T through
// ns, so only ns is a name in scope; a built-in type word is not one at all.
func (sc *scope) addType(annot string) {
	for _, qualified := range strings.FieldsFunc(annot, func(r rune) bool {
		return r != '_' && r != '\\' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
	}) {
		name, _, _ := strings.Cut(qualified, `\`)
		if name == "" || name == "mut" || buzz.IsReservedIdent(name) || '0' <= name[0] && name[0] <= '9' {
			continue
		}
		sc.add(name)
	}
}

// collectNames records free identifiers. A node that introduces bindings
// returns false from Inspect and collects its children under the new binding,
// so a parameter is not recorded as a reference to an import. Every other node
// returns true and ast.Inspect descends.
func collectNames(sc *scope, node ast.Node) {
	if node == nil {
		return
	}
	ast.Inspect(node, func(cur ast.Node) bool {
		switch cur := cur.(type) {
		case *ast.FunDecl:
			collectFun(sc, cur.Params, cur.ParamDefaults, cur.Body, cur.ParamAnnots, cur.RetAnnot, cur.ErrAnnot, cur.YieldAnnot)
			return false
		case *ast.FunExpr:
			collectFun(sc, cur.Params, cur.ParamDefaults, cur.Body, cur.ParamAnnots, cur.RetAnnot, cur.ErrAnnot, cur.YieldAnnot)
			return false
		case *ast.BlockStmt:
			collectBlock(sc, cur.Stmts)
			return false
		case *ast.IfStmt:
			collectNames(sc, cur.Cond)
			then := sc.clone()
			then.bind(cur.BindName)
			collectNames(then, cur.Then)
			collectNames(sc, cur.Else)
			return false
		case *ast.ForStmt:
			inner := sc.clone()
			for _, stmt := range cur.Init {
				collectNames(inner, stmt)
				if d, ok := stmt.(*ast.DeclStmt); ok {
					inner.bind(d.Name)
				}
			}
			collectNames(inner, cur.Cond)
			for _, stmt := range cur.Post {
				collectNames(inner, stmt)
			}
			collectNames(inner, cur.Body)
			return false
		case *ast.ForEachStmt:
			collectNames(sc, cur.Iter)
			inner := sc.clone()
			inner.bind(cur.KeyName, cur.ValName)
			collectNames(inner, cur.Body)
			return false
		case *ast.TryStmt:
			collectNames(sc, cur.Body)
			for i := range cur.Catches {
				sc.addType(cur.Catches[i].TypeName)
				inner := sc.clone()
				inner.bind(cur.Catches[i].ErrName)
				collectNames(inner, cur.Catches[i].Body)
			}
			return false
		case *ast.DeclStmt:
			sc.addType(cur.TypeAnnot)
		case *ast.IdentExpr:
			sc.add(cur.Name)
		case *ast.EnumCaseExpr:
			sc.add(cur.Enum)
			sc.add(cur.EnumNS)
		case *ast.ObjectLit:
			sc.addType(cur.TypeName)
		case *ast.IsExpr:
			sc.addType(cur.TypeName)
		case *ast.AsExpr:
			sc.addType(cur.TypeName)
		case *ast.TypeExpr:
			sc.addType(cur.Annot)
		case *ast.ObjectDecl:
			for _, name := range cur.Conforms {
				sc.add(name)
			}
			for _, f := range cur.Fields {
				sc.addType(f.TypeAnnot)
			}
			for _, f := range cur.StaticFields {
				sc.addType(f.TypeAnnot)
			}
		}
		return true
	})
}

func collectFun(sc *scope, params []string, defaults []ast.Node, body *ast.BlockStmt, paramAnnots []string, annots ...string) {
	for _, annot := range append(annots, paramAnnots...) {
		sc.addType(annot)
	}
	inner := sc.clone()
	inner.bind(params...)
	for _, d := range defaults {
		collectNames(inner, d)
	}
	collectNames(inner, body)
}

func collectBlock(sc *scope, stmts []ast.Node) {
	inner := sc.clone()
	for _, stmt := range stmts {
		collectNames(inner, stmt)
		if name, ok := declName(stmt); ok {
			inner.bind(name)
		}
	}
}

func importHas(stmts []ast.Node, name string) bool {
	for _, stmt := range stmts {
		imp, ok := stmt.(*ast.ImportStmt)
		if !ok {
			continue
		}
		for _, bound := range importNames(imp) {
			if bound == name {
				return true
			}
		}
	}
	return false
}

func importNames(imp *ast.ImportStmt) []string {
	if len(imp.Only) > 0 {
		return imp.Only
	}
	if imp.Alias == "_" {
		return nil
	}
	if imp.Alias != "" {
		return []string{imp.Alias}
	}
	return []string{path.Base(strings.TrimPrefix(imp.Path, "buzz:"))}
}

// importUsed reports whether the kept statements reference what imp binds.
// A name nothing visible binds can come from any file import: an unaliased one
// flat-merges its exports, and an aliased one binds its object types bare.
func importUsed(imp *ast.ImportStmt, free map[string]bool, unresolved bool) bool {
	for _, name := range importNames(imp) {
		if free[name] {
			return true
		}
	}
	return unresolved && !hostImport(imp.Path)
}
