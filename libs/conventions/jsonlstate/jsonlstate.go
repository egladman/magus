package jsonlstate

import (
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/gitdiff"
	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

const message = "this rewrites a whole JSON document as local state: keep an " +
	"append-only .jsonl store, one object per line, the last line per key " +
	"winning, compacted by rename"

// Options configures the analyzer returned by [New]. At least one of
// StateRoots, StateSegments and StateFields must name where state lives.
type Options struct {
	// Base is the revision a change is measured against, such as origin/main.
	// Only a write on a line changed since its merge base with HEAD is
	// reported.
	Base string `json:"base"`

	// StateRoots are functions returning a state directory, as
	// "<import path>.<name>".
	StateRoots []string `json:"state-roots"`

	// StateSegments are path segments that mark state in a string constant,
	// such as ".magus".
	StateSegments []string `json:"state-segments"`

	// StateFields are struct fields holding a state path, as
	// "<import path>.<type>.<field>".
	StateFields []string `json:"state-fields"`

	// Marshalers are functions encoding a whole value to bytes, as
	// "<import path>.<name>", beyond encoding/json's.
	Marshalers []string `json:"marshalers"`

	// Writers are functions writing data to a path, as "<import path>.<name>"
	// with the path first and the data second, beyond os.WriteFile.
	Writers []string `json:"writers"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts. It reports a write, or an
// os.Rename into place, that publishes the bytes of a JSON marshal at a state
// path. Rewriting the document erases a concurrent writer's update.
//
// New errors when no base or state path is named or a name is malformed.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Base == "" {
		return nil, errors.New("jsonlstate: no base revision to measure a change against")
	}
	if len(opts.StateRoots) == 0 && len(opts.StateSegments) == 0 &&
		len(opts.StateFields) == 0 {
		return nil, errors.New("jsonlstate: no state-roots, state-segments or " +
			"state-fields, so no path is state")
	}
	for _, name := range slices.Concat(opts.StateRoots, opts.Marshalers, opts.Writers) {
		if i := strings.LastIndex(name, "."); i <= 0 || i == len(name)-1 {
			return nil, fmt.Errorf("jsonlstate: %q is not <import path>.<name>", name)
		}
	}
	for _, name := range opts.StateFields {
		if strings.Count(name, ".") < 2 || strings.HasSuffix(name, ".") {
			return nil, fmt.Errorf(
				"jsonlstate: state field %q is not <import path>.<type>.<field>", name)
		}
	}
	if slices.Contains(opts.StateSegments, "") {
		return nil, errors.New("jsonlstate: an empty state segment matches every path")
	}
	return newWith(opts, gitdiff.New(opts.Base)), nil
}

func newWith(opts Options, changes changeSource) *analysis.Analyzer {
	return &analysis.Analyzer{
		Name: "jsonlstate",
		Doc:  "report a whole JSON document written as local state",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts, changes) },
	}
}

type changeSource interface {
	// Lines returns the changed lines of the working copy.
	Lines(path string) ([]gitdiff.Span, error)
}

func run(pass *analysis.Pass, opts Options, changes changeSource) error {
	// Only type-checked files: an ignored file has no types to resolve a call.
	for _, f := range pass.Files {
		if source.IsTest(pass, f) {
			continue
		}
		changed, err := changes.Lines(source.Name(pass, f))
		if err != nil {
			return fmt.Errorf("jsonlstate: %w", err)
		}
		if len(changed) == 0 {
			continue
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				w := &walker{
					pass: pass, opts: opts, changed: changed,
					doc: map[types.Object]bool{}, state: map[types.Object]bool{},
				}
				w.function(fn.Body)
			}
		}
	}
	return nil
}

func (w *walker) report(call *ast.CallExpr) {
	from := w.pass.Fset.Position(call.Pos()).Line
	to := w.pass.Fset.Position(call.End()).Line
	if gitdiff.Overlaps(w.changed, from, to) {
		w.pass.Reportf(call.Pos(), "%s", source.Hint(message, w.opts.Hint))
	}
}

type walker struct {
	pass *analysis.Pass
	opts Options
	// changed holds this file's lines changed since the base.
	changed []gitdiff.Span
	// doc holds variables assigned a marshal result; state, a state path.
	doc   map[types.Object]bool
	state map[types.Object]bool
}

func (w *walker) function(body *ast.BlockStmt) {
	// A closure can read a variable assigned further down, so settle
	// assignments before judging a call.
	for range 3 {
		ast.Inspect(body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				w.assign(n.Lhs, n.Rhs)
			case *ast.ValueSpec:
				lhs := make([]ast.Expr, len(n.Names))
				for i, name := range n.Names {
					lhs[i] = name
				}
				w.assign(lhs, n.Values)
			}
			return true
		})
	}
	var published bool
	var renames []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch name := w.callee(call); {
		case name == "os.Rename" && len(call.Args) == 2:
			renames = append(renames, call)
		case w.isWriter(name) && len(call.Args) >= 2 && w.isDocument(call.Args[1]):
			published = true
			if w.isState(call.Args[0]) {
				w.report(call)
			}
		case name == "(*os.File).Write" && len(call.Args) == 1 &&
			w.isDocument(call.Args[0]):
			published = true
		}
		return true
	})
	// The renamed temp file was written beside the rename; its bytes never
	// reach the call.
	if !published {
		return
	}
	for _, call := range renames {
		if w.isState(call.Args[1]) {
			w.report(call)
		}
	}
}

func (w *walker) assign(lhs, rhs []ast.Expr) {
	switch {
	case len(lhs) == len(rhs):
		for i := range lhs {
			w.note(lhs[i], w.isMarshal(rhs[i]), w.isState(rhs[i]))
		}
	case len(rhs) == 1:
		// b, err := json.Marshal(v): the value is the first result.
		w.note(lhs[0], w.isMarshal(rhs[0]), w.isState(rhs[0]))
	}
}

func (w *walker) note(lhs ast.Expr, doc, state bool) {
	id, ok := lhs.(*ast.Ident)
	if !ok || id.Name == "_" {
		return
	}
	obj := w.pass.TypesInfo.ObjectOf(id)
	if obj == nil {
		return
	}
	if doc {
		w.doc[obj] = true
	}
	if state {
		w.state[obj] = true
	}
}

// callee is "<import path>.<name>" for a function and types.Func.FullName for
// a method; "" for a conversion or a function value.
func (w *walker) callee(call *ast.CallExpr) string {
	fn, ok := typeutil.Callee(w.pass.TypesInfo, call).(*types.Func)
	if !ok {
		return ""
	}
	if fn.Pkg() != nil && fn.Type().(*types.Signature).Recv() == nil {
		return fn.Pkg().Path() + "." + fn.Name()
	}
	return fn.FullName()
}

func (w *walker) isWriter(name string) bool {
	return name == "os.WriteFile" || slices.Contains(w.opts.Writers, name)
}

func (w *walker) isMarshal(e ast.Expr) bool {
	call, ok := ast.Unparen(e).(*ast.CallExpr)
	if !ok {
		return false
	}
	name := w.callee(call)
	return name == "encoding/json.Marshal" ||
		name == "encoding/json.MarshalIndent" ||
		slices.Contains(w.opts.Marshalers, name)
}

// isDocument excludes an append onto marshal bytes: a line added to a store is
// the entry, not the document.
func (w *walker) isDocument(e ast.Expr) bool {
	e = ast.Unparen(e)
	if id, ok := e.(*ast.Ident); ok {
		return w.doc[w.pass.TypesInfo.ObjectOf(id)]
	}
	return w.isMarshal(e)
}

// isState reports whether the path e calls a state root, spells a state
// segment, reads a state field or reads a variable holding one.
func (w *walker) isState(e ast.Expr) bool {
	var found bool
	ast.Inspect(e, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.CallExpr:
			found = slices.Contains(w.opts.StateRoots, w.callee(n))
		case *ast.Ident:
			found = w.state[w.pass.TypesInfo.ObjectOf(n)]
		case *ast.SelectorExpr:
			found = w.isStateField(n)
		}
		if x, ok := n.(ast.Expr); ok && !found {
			if tv := w.pass.TypesInfo.Types[x]; tv.Value != nil {
				found = w.hasSegment(tv.Value.ExactString())
			}
		}
		return !found
	})
	return found
}

func (w *walker) isStateField(sel *ast.SelectorExpr) bool {
	v, ok := w.pass.TypesInfo.Uses[sel.Sel].(*types.Var)
	if !ok || !v.IsField() {
		return false
	}
	t := w.pass.TypesInfo.TypeOf(sel.X)
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return false
	}
	return slices.Contains(w.opts.StateFields,
		named.Obj().Pkg().Path()+"."+named.Obj().Name()+"."+v.Name())
}

// hasSegment matches whole segments, so ".magus" does not match
// ".magus-base-cache".
func (w *walker) hasSegment(quoted string) bool {
	for part := range strings.SplitSeq(strings.Trim(quoted, `"`), "/") {
		if slices.Contains(w.opts.StateSegments, part) {
			return true
		}
	}
	return false
}
