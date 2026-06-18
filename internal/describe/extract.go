// Package describe extracts a magusfile's target dependency graph statically,
// without evaluating any target body. Every `export fun` is a node, its leading
// doc comment is the node's description, and the `magus.needs(magus.target.<mode>(…))`
// handles in its body are its edges. Because it reads
// the source rather than running it, it is deterministic and sees *both* arms of
// a runtime branch (e.g. the `container` charm toggle on `build`) — a runtime
// trace would only ever see the arm taken.
//
// It is a static-analysis helper for tooling, not part of the execution core:
// `magus describe graph` renders it and reports cycles (via Cycle), including the
// `-o markdown` MAGUS.md doc. Run-time cycle enforcement is a separate concern
// owned by the dispatch pool. Node and dependency names are normalized with the
// same kebab-case normalizer the run path registers targets under, so a node and
// an edge that name the same target always reconcile.
//
// Extraction is built on the Buzz parser (gopherbuzz/ast): the source is parsed
// to an AST once and walked, rather than scanned with regexes. The parser already
// knows comments, string literals, and brace nesting, so a dependency token in a
// help string or trailing comment can't fake an edge. A source that fails to parse
// yields an empty graph (best-effort, never errors).
package describe

import (
	"regexp"
	"slices"
	"strings"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/ast"
	"github.com/egladman/magus/types"
)

// Extract parses a Buzz magusfile's source into its target nodes (best-effort, never
// errors — a source that fails to parse yields an empty graph). Named Extract, not
// Build, to signal static-source extraction and to not collide with depgraph.Build,
// which constructs a different graph. Pass the concatenated contents of all of a
// project's magusfile sources (load order).
//
// Each node's Dependencies are the resolved dependency target names — exact edges
// first (magus.target.literal, in source order), then the names matched by each
// glob/regex edge (magus.target.glob/regex); self-edges and duplicates are dropped.
// CrossDependencies hold cross-project edges (from project imports). Charms are the
// has_charm names the body reads, sorted.
func Extract(source string) []types.TargetGraphNode {
	prog, err := buzz.ParseEmbedded(source)
	if err != nil || prog == nil {
		return nil
	}
	// Node and dependency names both go through the run path's kebab-case
	// normalizer, so a node and an edge that name the same target reconcile.
	norm := types.DefaultTargetNameNormalizer.NormalizeTargetName

	// First pass over the top-level statements: collect every exported target name
	// (so a glob or regex edge can resolve against a target defined later), the spell
	// handles in scope (so the op-call walk keeps only real spell calls, not host
	// calls), and the project-import aliases (so an <alias>.<target> reference resolves
	// to a cross-project edge).
	var names []string
	funcs := map[string]*ast.FunDecl{} // every fun decl (exported targets + plain helpers), for helper-following
	spellHandles := map[string]bool{}
	projectAliases := map[string]string{} // alias -> project path (as written after "project/")
	for _, stmt := range prog.Stmts {
		switch s := stmt.(type) {
		case *ast.FunDecl:
			funcs[s.Name] = s
			if s.IsExported {
				names = append(names, norm(s.Name))
			}
		case *ast.ImportStmt:
			if h, ok := spellHandle(s); ok {
				spellHandles[h] = true
			}
			if alias, path, ok := projectImport(s); ok {
				projectAliases[alias] = path
			}
		}
	}

	// Second pass: build each node by walking its body, resolving every edge straight
	// into its Dependencies — exact edges (magus.target.literal) by name, glob and
	// regex edges by matching the names collected above.
	var nodes []types.TargetGraphNode
	for _, stmt := range prog.Stmts {
		fn, ok := stmt.(*ast.FunDecl)
		if !ok || !fn.IsExported {
			continue
		}
		node := types.TargetGraphNode{Name: norm(fn.Name), Doc: docSentence(fn.Doc)}
		var spellHits []spellHit
		// A target attributes the ops/charms/edges of the same-file helper functions
		// it calls, not only those in its own body. walk follows a bare call into a
		// local helper (cycle-guarded by visited), so a target that factors its spell
		// calls into a helper — e.g. image_build → build_variant → docker[...] —
		// keeps them attributed rather than silently dropping them.
		visited := map[string]bool{fn.Name: true}
		var walk func(body *ast.BlockStmt)
		walk = func(body *ast.BlockStmt) {
			if body == nil {
				return
			}
			inspect(body, func(n ast.Node) bool {
				switch e := n.(type) {
				case *ast.CallExpr:
					// Dependency handles: magus.target.literal/glob/regex("...").
					if mode, arg, ok := targetHandleCall(e); ok {
						switch mode {
						case "literal":
							node.Dependencies = appendUniq(node.Dependencies, norm(arg))
						case "glob":
							node.Dependencies = appendMatching(node.Dependencies, names, node.Name, globRe(arg))
						case "regex":
							// An invalid pattern is best-effort skipped (no edges).
							if re, cerr := regexp.Compile(arg); cerr == nil {
								node.Dependencies = appendMatching(node.Dependencies, names, node.Name, re)
							}
						}
					}
					if name, ok := charmCall(e); ok {
						node.Charms = appendUniq(node.Charms, name)
					}
					// Dotted spell op: handle.op(...), where handle is an imported spell.
					if me, ok := e.Callee.(*ast.MemberExpr); ok {
						if id, ok := me.Object.(*ast.IdentExpr); ok && spellHandles[id.Name] {
							spellHits = append(spellHits, spellHit{ast.NodePos(me), id.Name, me.Name})
						}
					}
					// Bare call into a same-file helper: follow it once so its ops,
					// charms, and edges attribute to this target.
					if id, ok := e.Callee.(*ast.IdentExpr); ok {
						if h := funcs[id.Name]; h != nil && !visited[id.Name] {
							visited[id.Name] = true
							walk(h.Body)
						}
					}
				case *ast.IndexExpr:
					// Bracket spell op: handle["op"], where handle is an imported spell.
					if id, ok := e.Object.(*ast.IdentExpr); ok && spellHandles[id.Name] {
						if lit, ok := e.Index.(*ast.StringLit); ok {
							spellHits = append(spellHits, spellHit{ast.NodePos(e), id.Name, lit.Val})
						}
					}
				case *ast.MemberExpr:
					// Cross-project edge: <alias>.<target>, where <alias> came from an
					// `import "project/<path>"`. The project path is left as written; the
					// caller resolves it later.
					if id, ok := e.Object.(*ast.IdentExpr); ok {
						if proj, ok := projectAliases[id.Name]; ok {
							ref := types.CrossTargetRef{Project: proj, Target: norm(e.Name)}
							if !slices.Contains(node.CrossDependencies, ref) {
								node.CrossDependencies = append(node.CrossDependencies, ref)
							}
						}
					}
				}
				return true
			})
		}
		walk(fn.Body)
		slices.Sort(node.Charms)
		node.Spells = groupSpellOps(spellHits)
		nodes = append(nodes, node)
	}
	return nodes
}

// spellHit is one spell op call found in a target body, tagged with its source
// position so ops can be reported in call order.
type spellHit struct {
	pos       ast.Pos
	spell, op string
}

// targetHandleCall recognizes a magus.target.<mode>("arg") call and returns its mode
// ("literal"/"glob"/"regex") and the literal string argument. ok is false for any
// other call.
func targetHandleCall(e *ast.CallExpr) (mode, arg string, ok bool) {
	me, ok := e.Callee.(*ast.MemberExpr)
	if !ok {
		return "", "", false
	}
	switch me.Name {
	case "literal", "glob", "regex":
	default:
		return "", "", false
	}
	inner, ok := me.Object.(*ast.MemberExpr)
	if !ok || inner.Name != "target" {
		return "", "", false
	}
	root, ok := inner.Object.(*ast.IdentExpr)
	if !ok || root.Name != "magus" {
		return "", "", false
	}
	if len(e.Args) == 0 {
		return "", "", false
	}
	lit, ok := e.Args[0].(*ast.StringLit)
	if !ok {
		return "", "", false
	}
	return me.Name, lit.Val, true
}

// charmCall recognizes a magus.has_charm("name") call and returns the charm name.
func charmCall(e *ast.CallExpr) (string, bool) {
	me, ok := e.Callee.(*ast.MemberExpr)
	if !ok || me.Name != "has_charm" {
		return "", false
	}
	id, ok := me.Object.(*ast.IdentExpr)
	if !ok || id.Name != "magus" {
		return "", false
	}
	if len(e.Args) == 0 {
		return "", false
	}
	lit, ok := e.Args[0].(*ast.StringLit)
	if !ok {
		return "", false
	}
	return lit.Val, true
}

// spellHandle returns the handle a spell import binds, and ok=true when the import
// is a spell. Built-in spells are `import "magus/spell/<name>"` (bound under the
// basename); workspace spells are `import "spells/<...>" as <alias>` (bound under
// the alias). An explicit alias wins over the basename.
func spellHandle(s *ast.ImportStmt) (string, bool) {
	switch {
	case strings.HasPrefix(s.Path, "magus/spell/"):
		if s.Alias != "" && s.Alias != "_" {
			return s.Alias, true
		}
		return lastPathSegment(s.Path), true
	case strings.HasPrefix(s.Path, "spells/") && s.Alias != "" && s.Alias != "_":
		return s.Alias, true
	}
	return "", false
}

// projectImport returns the alias and project path of an `import "project/<path>"`
// statement. The alias defaults to the path's last segment.
func projectImport(s *ast.ImportStmt) (alias, path string, ok bool) {
	const prefix = "project/"
	if !strings.HasPrefix(s.Path, prefix) {
		return "", "", false
	}
	path = strings.TrimPrefix(s.Path, prefix)
	if path == "" {
		return "", "", false
	}
	alias = s.Alias
	if alias == "" || alias == "_" {
		alias = lastPathSegment(path)
	}
	return alias, path, true
}

// groupSpellOps groups the spell-op hits by spell. Spells appear in first-call
// order, ops in first-call order within each spell, both deduped — so a `lint` that
// fans out golangci-lint/go-vet/govulncheck plus markdownlint reads as the toolchain
// it drives. Returns nil when the body calls no spell.
func groupSpellOps(hits []spellHit) []types.TargetSpellUse {
	if len(hits) == 0 {
		return nil
	}
	slices.SortStableFunc(hits, func(a, b spellHit) int {
		if a.pos.Line != b.pos.Line {
			return a.pos.Line - b.pos.Line
		}
		return a.pos.Col - b.pos.Col
	})
	var uses []types.TargetSpellUse
	idx := map[string]int{} // spell -> index into uses, to group ops under one entry
	for _, h := range hits {
		i, ok := idx[h.spell]
		if !ok {
			i = len(uses)
			idx[h.spell] = i
			uses = append(uses, types.TargetSpellUse{Spell: h.spell})
		}
		uses[i].Ops = appendUniq(uses[i].Ops, h.op)
	}
	return uses
}

// inspect walks the AST rooted at n in depth-first order, calling fn for every node.
// fn returns false to stop descending into that node's children (go/ast.Inspect
// style). Concrete-pointer children are nil-checked before recursion so a nil
// *BlockStmt isn't wrapped into a non-nil ast.Node interface.
func inspect(n ast.Node, fn func(ast.Node) bool) {
	if n == nil || !fn(n) {
		return
	}
	switch s := n.(type) {
	case *ast.DeclStmt:
		inspect(s.Value, fn)
	case *ast.AssignStmt:
		inspect(s.Target, fn)
		inspect(s.Value, fn)
	case *ast.ReturnStmt:
		inspect(s.Value, fn)
	case *ast.ExprStmt:
		inspect(s.Expr, fn)
	case *ast.BlockStmt:
		for _, c := range s.Stmts {
			inspect(c, fn)
		}
	case *ast.IfStmt:
		inspect(s.Cond, fn)
		if s.Then != nil {
			inspect(s.Then, fn)
		}
		inspect(s.Else, fn)
	case *ast.WhileStmt:
		inspect(s.Cond, fn)
		if s.Body != nil {
			inspect(s.Body, fn)
		}
	case *ast.ForStmt:
		inspect(s.Init, fn)
		inspect(s.Cond, fn)
		inspect(s.Post, fn)
		if s.Body != nil {
			inspect(s.Body, fn)
		}
	case *ast.ForEachStmt:
		inspect(s.Iter, fn)
		if s.Body != nil {
			inspect(s.Body, fn)
		}
	case *ast.FunDecl:
		if s.Body != nil {
			inspect(s.Body, fn)
		}
	case *ast.TestDecl:
		if s.Body != nil {
			inspect(s.Body, fn)
		}
	case *ast.ObjectDecl:
		for i := range s.Fields {
			inspect(s.Fields[i].Default, fn)
		}
		for _, m := range s.Methods {
			inspect(m, fn)
		}
	case *ast.BinaryExpr:
		inspect(s.Left, fn)
		inspect(s.Right, fn)
	case *ast.UnaryExpr:
		inspect(s.Operand, fn)
	case *ast.CallExpr:
		inspect(s.Callee, fn)
		for _, a := range s.Args {
			inspect(a, fn)
		}
	case *ast.MemberExpr:
		inspect(s.Object, fn)
	case *ast.IndexExpr:
		inspect(s.Object, fn)
		inspect(s.Index, fn)
	case *ast.ForceExpr:
		inspect(s.Operand, fn)
	case *ast.FunExpr:
		if s.Body != nil {
			inspect(s.Body, fn)
		}
	case *ast.MapExpr:
		for _, k := range s.Keys {
			inspect(k, fn)
		}
		for _, v := range s.Values {
			inspect(v, fn)
		}
	case *ast.ListExpr:
		for _, it := range s.Items {
			inspect(it, fn)
		}
	case *ast.ObjectLit:
		for _, v := range s.Values {
			inspect(v, fn)
		}
	case *ast.InterpExpr:
		for i := range s.Parts {
			inspect(s.Parts[i].Expr, fn)
		}
	case *ast.DoStmt:
		if s.Body != nil {
			inspect(s.Body, fn)
		}
		inspect(s.Cond, fn)
	case *ast.RangeExpr:
		inspect(s.Lo, fn)
		inspect(s.Hi, fn)
	case *ast.IsExpr:
		inspect(s.Expr, fn)
	case *ast.AsExpr:
		inspect(s.Expr, fn)
	case *ast.CatchExpr:
		inspect(s.Expr, fn)
		inspect(s.Default, fn)
	case *ast.TryStmt:
		if s.Body != nil {
			inspect(s.Body, fn)
		}
		if s.Catch != nil {
			inspect(s.Catch, fn)
		}
	case *ast.ThrowStmt:
		inspect(s.Value, fn)
	case *ast.YieldExpr:
		inspect(s.Value, fn)
	case *ast.FiberExpr:
		if s.Call != nil {
			inspect(s.Call, fn)
		}
	case *ast.ResumeExpr:
		inspect(s.Fiber, fn)
	case *ast.ResolveExpr:
		inspect(s.Fiber, fn)
	}
}

// appendMatching appends every name (other than self) that re matches, deduped,
// in names order.
func appendMatching(deps, names []string, self string, re *regexp.Regexp) []string {
	for _, n := range names {
		if n != self && re.MatchString(n) {
			deps = appendUniq(deps, n)
		}
	}
	return deps
}

// Cycle returns a dependency cycle as a path of node names ending where it began
// (e.g. ["a","b","a"]), or nil if the graph is acyclic. Edges to undeclared
// targets are ignored here — that is a separate "unknown target" error the run
// path already raises.
//
// Scope is intra-project only: it walks each node's Dependencies (same-magusfile
// edges) and deliberately ignores CrossDependencies, since callers pass one
// project's nodes at a time and the foreign target is never in this map. A
// cross-project cycle is therefore invisible here by design — it is caught at the
// project granularity by the depgraph (types.Graph, via doctor) and at run time by
// the cross-project dispatch coordinator, not by this function.
func Cycle(nodes []types.TargetGraphNode) []string {
	deps := make(map[string][]string, len(nodes))
	for _, n := range nodes {
		deps[n.Name] = n.Dependencies
	}
	// Classic 3-color DFS: white (unvisited) is the implicit 0 zero-value, grey is
	// on the current DFS stack (a back-edge to grey is a cycle), black is fully done.
	const (
		grey  = 1
		black = 2
	)
	state := map[string]int{}
	var stack []string
	var visit func(n string) []string
	visit = func(n string) []string {
		state[n] = grey
		stack = append(stack, n)
		for _, d := range deps[n] {
			if _, declared := deps[d]; !declared {
				continue
			}
			switch state[d] {
			case grey:
				// Back-edge: the cycle is the stack suffix from d, closed with d.
				for i, s := range stack {
					if s == d {
						return append(append([]string(nil), stack[i:]...), d)
					}
				}
			case 0:
				if c := visit(d); c != nil {
					return c
				}
			}
		}
		state[n] = black
		stack = stack[:len(stack)-1]
		return nil
	}
	for _, n := range nodes {
		if state[n.Name] == 0 {
			if c := visit(n.Name); c != nil {
				return c
			}
		}
	}
	return nil
}

// docSentence reduces a FunDecl's doc-comment block (the parser's contiguous
// leading-comment text, one comment per line, `//` already stripped) to the first
// sentence of its prose. Divider lines (the `── … ──` rules) and blank lines are
// dropped so a section divider directly above a function never bleeds in.
func docSentence(doc string) string {
	if doc == "" {
		return ""
	}
	var prose []string
	for _, line := range strings.Split(doc, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.Contains(s, "──") {
			continue
		}
		prose = append(prose, s)
	}
	return firstSentence(strings.Join(prose, " "))
}

// firstSentence returns s up to and including the first period that ends a
// sentence (followed by a space or end of string), godoc-style. A period inside a
// token like `extra.fs.watch` is not a sentence end, so it is left intact. Byte
// iteration is safe: `.` and space are ASCII and never appear as a UTF-8
// continuation byte, so a multibyte rune can't be mis-split.
func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	for i := 0; i < len(s); i++ {
		if s[i] == '.' && (i == len(s)-1 || s[i+1] == ' ') {
			return s[:i+1]
		}
	}
	return s
}

// globRe compiles a target-name glob (only `*` is special) to an anchored regexp.
func globRe(pattern string) *regexp.Regexp {
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile("^" + strings.Join(parts, ".*") + "$")
}

func appendUniq(s []string, v string) []string {
	if slices.Contains(s, v) {
		return s
	}
	return append(s, v)
}

// lastPathSegment returns the text after the final '/', or the whole string if
// none — the default alias for an `import "project/<path>"` (basename of the path).
func lastPathSegment(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}
