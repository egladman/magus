package thin

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Helper is one candidate function and its use sites.
type Helper struct {
	Decl *ast.FuncDecl
	File *ast.File
	Func *types.Func

	// Expr is what the body computes: the returned expression, or the call a
	// body of one call statement makes.
	Expr ast.Expr

	// Returns is false for a body of one call statement.
	Returns bool

	// Guarded is true when a nil check returning nil comes before the return,
	// which a site must keep, so no edit inlines it.
	Guarded bool

	// Sites are the references in the type-checked files, in source order. A
	// private name is visible only in its package, and golangci-lint lints a
	// package's test variant, so these are every site.
	Sites []site

	// Elsewhere counts references in files this platform's build excludes,
	// which no edit can reach.
	Elsewhere int
}

// site is one reference to a helper.
type site struct {
	Ident *ast.Ident
	File  *ast.File

	// Call is the call the reference names, nil when the function is used as
	// a value.
	Call *ast.CallExpr

	// Parent is the node holding Call, or Ident for a value use.
	Parent ast.Node
}

// Uses is the number of references.
func (h *Helper) Uses() int { return len(h.Sites) + h.Elsewhere }

// ValueUse reports whether a site uses the function as a value rather than
// calling it.
func (h *Helper) ValueUse() bool {
	for _, s := range h.Sites {
		if s.Call == nil {
			return true
		}
	}
	return false
}

// find returns private package-level functions of pass with a one-statement
// body, or a nil check and one return, that keep accepts. It skips methods,
// generics, init, main, //go: and literals. A twin in an excluded file is a
// build-constrained pair.
func find(pass *analysis.Pass, ignored []*ast.File, keep func(*Helper) bool) []*Helper {
	siblings := map[string]bool{}
	for _, f := range ignored {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil {
				siblings[fd.Name.Name] = true
			}
		}
	}
	var out []*Helper
	byFunc := map[*types.Func]*Helper{}
	for _, f := range pass.Files {
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || !candidate(fd) || siblings[fd.Name.Name] {
				continue
			}
			fn, ok := pass.TypesInfo.Defs[fd.Name].(*types.Func)
			if !ok {
				continue
			}
			h := &Helper{Decl: fd, File: f, Func: fn}
			if !shape(h) || holdsFuncLit(fd.Body) || !keep(h) {
				continue
			}
			out = append(out, h)
			byFunc[fn] = h
		}
	}
	if len(out) == 0 {
		return nil
	}
	for _, f := range pass.Files {
		collectSites(pass, f, byFunc)
	}
	names := map[string]*Helper{}
	for _, h := range out {
		names[h.Func.Name()] = h
	}
	for _, f := range ignored {
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				if h := names[id.Name]; h != nil {
					h.Elsewhere++
				}
			}
			return true
		})
	}
	return out
}

func candidate(fd *ast.FuncDecl) bool {
	name := fd.Name.Name
	if fd.Recv != nil || fd.Body == nil || ast.IsExported(name) || name == "_" || name == "init" || name == "main" {
		return false
	}
	if fd.Type.TypeParams != nil && len(fd.Type.TypeParams.List) > 0 {
		return false
	}
	if fd.Doc != nil {
		for _, c := range fd.Doc.List {
			if strings.HasPrefix(c.Text, "//go:") {
				return false
			}
		}
	}
	return true
}

// shape fills Expr, Returns and Guarded from a one-statement body. So does
// `v := E; return v`, or a nil check returning nil and then a return.
func shape(h *Helper) bool {
	list := h.Decl.Body.List
	switch len(list) {
	case 1:
		switch s := list[0].(type) {
		case *ast.ReturnStmt:
			if len(s.Results) != 1 {
				return false
			}
			h.Expr, h.Returns = s.Results[0], true
			return true
		case *ast.ExprStmt:
			call, ok := s.X.(*ast.CallExpr)
			if !ok {
				return false
			}
			h.Expr = call
			return true
		}
	case 2:
		ret, ok := list[1].(*ast.ReturnStmt)
		if !ok || len(ret.Results) != 1 {
			return false
		}
		if e, ok := boundThenReturned(list[0], ret.Results[0]); ok {
			h.Expr, h.Returns = e, true
			return true
		}
		if nilGuard(list[0]) {
			h.Expr, h.Returns, h.Guarded = ret.Results[0], true, true
			return true
		}
	}
	return false
}

func boundThenReturned(first ast.Stmt, result ast.Expr) (ast.Expr, bool) {
	as, ok := first.(*ast.AssignStmt)
	if !ok || as.Tok != token.DEFINE || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
		return nil, false
	}
	l, ok := as.Lhs[0].(*ast.Ident)
	r, ok2 := result.(*ast.Ident)
	if !ok || !ok2 || l.Name != r.Name {
		return nil, false
	}
	return as.Rhs[0], true
}

func nilGuard(s ast.Stmt) bool {
	is, ok := s.(*ast.IfStmt)
	if !ok || is.Init != nil || is.Else != nil || len(is.Body.List) != 1 {
		return false
	}
	cond, ok := is.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.EQL || !isNil(cond.Y) {
		return false
	}
	ret, ok := is.Body.List[0].(*ast.ReturnStmt)
	return ok && len(ret.Results) == 1 && isNil(ret.Results[0])
}

func isNil(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

func holdsFuncLit(n ast.Node) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			found = true
		}
		return !found
	})
	return found
}

func collectSites(pass *analysis.Pass, f *ast.File, byFunc map[*types.Func]*Helper) {
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		id, ok := n.(*ast.Ident)
		if !ok {
			return true
		}
		fn, ok := pass.TypesInfo.Uses[id].(*types.Func)
		if !ok {
			return true
		}
		h := byFunc[fn]
		if h == nil {
			return true
		}
		s := site{Ident: id, File: f, Parent: parent(stack, 1)}
		if call, ok := s.Parent.(*ast.CallExpr); ok && ast.Unparen(call.Fun) == id {
			s.Call = call
			s.Parent = parent(stack, 2)
		}
		h.Sites = append(h.Sites, s)
		return true
	})
}

// parent returns the node up levels above the top of stack, skipping parens.
func parent(stack []ast.Node, up int) ast.Node {
	i := len(stack) - 1
	for ; up > 0 && i > 0; up-- {
		i--
		for i > 0 {
			if _, ok := stack[i].(*ast.ParenExpr); !ok {
				break
			}
			i--
		}
	}
	return stack[i]
}
