// Package fieldwise reports a test asserting a struct one field at a time.
//
// Two equality assertions on fields of the same value pin those fields and no
// others: when the struct grows a field, the test keeps passing without ever
// looking at it. Building the whole expected value and comparing once covers
// the new field the day it lands, and a test that must ignore a volatile field
// says so by normalizing it first.
//
// By default only a value asserted on every field it declares is reported, so
// the fix checks exactly what the assertions did. [Options.ReportPartial]
// widens that to a value asserted on some of its fields.
//
// An assertion is a testify Equal, EqualValues or Exactly (function, method or
// f-form, from assert or require), or an if whose condition compares with !=
// and whose body fails the test. The asserted side is the actual argument, or
// the left of !=; the other side counts only when that one is a constant.
//
// A value stops being the same value at a statement that may change it: an
// assignment to it, or a call handed it through a pointer. Not reported:
//
//   - a value asserted on fewer than [Options.MinFields] distinct fields;
//   - a range variable, which is a table case or a different element each
//     iteration, and any value reached through a variable index or a call;
//   - a struct from another package with unexported fields, which the test
//     cannot construct;
//   - a struct with a func, chan or non-error interface field, which no deep
//     comparison can match;
//   - an assertion comparing two fields of the value with each other.
//
// Each function literal is its own unit, so a subtest is judged apart from the
// test that runs it.
package fieldwise

import (
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "%s is asserted one field at a time (%s): build the whole expected %s and compare it " +
	"once, normalizing volatile fields first, so a field added to %s cannot slip past this test"

const defaultMinFields = 2

const (
	testifyAssert  = "github.com/stretchr/testify/assert"
	testifyRequire = "github.com/stretchr/testify/require"
)

var equalities = map[string]bool{
	"Equal": true, "Equalf": true,
	"EqualValues": true, "EqualValuesf": true,
	"Exactly": true, "Exactlyf": true,
}

var failures = map[string]bool{
	"Error": true, "Errorf": true, "Fatal": true, "Fatalf": true, "Fail": true, "FailNow": true,
}

// Options configures the analyzer returned by [New]. The zero value reports a
// value asserted on two or more fields.
type Options struct {
	// MinFields is the fewest distinct fields of one value a unit must assert
	// before it is reported; zero means 2.
	MinFields int `json:"min-fields"`

	// ReportPartial also reports a value asserted on only some of its fields.
	// Its fix compares fields the test never looked at, so it can fail on a
	// value the test was content with until a volatile field is normalized:
	// a different kind of edit from folding assertions that already name
	// every field.
	ReportPartial bool `json:"report-partial"`

	// Hint is appended to every diagnostic.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts, erroring on a MinFields below 2
// other than zero: one field asserted alone is not field-by-field.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.MinFields == 0 {
		opts.MinFields = defaultMinFields
	}
	if opts.MinFields < 2 {
		return nil, errors.New("fieldwise: min-fields must be at least 2")
	}
	return &analysis.Analyzer{
		Name: "fieldwise",
		Doc:  "report tests asserting a struct one field at a time instead of comparing it whole",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	for _, f := range pass.Files {
		if !source.IsTest(pass, f) {
			continue
		}
		c := &checker{pass: pass, opts: opts, ranged: map[types.Object]bool{}}
		ast.Inspect(f, func(n ast.Node) bool {
			if r, ok := n.(*ast.RangeStmt); ok {
				for _, e := range []ast.Expr{r.Key, r.Value} {
					if id, ok := e.(*ast.Ident); ok {
						if obj := pass.TypesInfo.ObjectOf(id); obj != nil {
							c.ranged[obj] = true
						}
					}
				}
			}
			return true
		})
		ast.Inspect(f, func(n ast.Node) bool {
			switch fn := n.(type) {
			case *ast.FuncDecl:
				if fn.Body != nil {
					c.unit(fn.Body)
				}
			case *ast.FuncLit:
				c.unit(fn.Body)
			}
			return true
		})
	}
	return nil
}

type checker struct {
	pass   *analysis.Pass
	opts   Options
	ranged map[types.Object]bool
}

// value identifies one state of one struct value: the expression spelling it,
// the variable it hangs from, and how many statements that may change it the
// unit has passed.
type value struct {
	root *types.Var
	expr string
	gen  int
}

type group struct {
	pos    token.Pos
	typ    types.Type
	fields []string
}

func (c *checker) unit(body *ast.BlockStmt) {
	gens := map[*types.Var]int{}
	groups := map[value]*group{}
	var order []value

	record := func(pos token.Pos, operand, other ast.Expr) {
		sel, ok := c.field(operand)
		if !ok {
			return
		}
		if o, ok := c.field(other); ok {
			if c.render(o.X) == c.render(sel.X) {
				return
			}
		}
		root, ok := c.root(sel.X)
		if !ok || c.ranged[root] {
			return
		}
		typ := c.pass.TypesInfo.TypeOf(sel.X)
		if !c.comparable(typ) {
			return
		}
		key := value{root: root, expr: c.render(sel.X), gen: gens[root]}
		g, ok := groups[key]
		if !ok {
			g = &group{pos: pos, typ: typ}
			groups[key] = g
			order = append(order, key)
		}
		if !slices.Contains(g.fields, sel.Sel.Name) {
			g.fields = append(g.fields, sel.Sel.Name)
		}
	}
	invalidate := func(e ast.Expr) {
		if root, ok := c.root(e); ok {
			gens[root]++
		}
	}

	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			fn := c.callee(n)
			if fn != nil && fn.Pkg() != nil {
				switch fn.Pkg().Path() {
				case testifyAssert, testifyRequire:
					if actual, expected, ok := c.equality(fn, n); ok {
						c.orient(n.Pos(), actual, expected, record)
					}
					// An argument may still be a call that changes the value.
					return true
				case "testing":
					return false
				}
			}
			if sel, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr); ok && c.mutator(fn) {
				invalidate(sel.X)
			}
			for _, arg := range n.Args {
				if c.shared(arg) {
					invalidate(arg)
				}
			}
		case *ast.AssignStmt:
			for _, lhs := range n.Lhs {
				invalidate(lhs)
			}
		case *ast.IncDecStmt:
			invalidate(n.X)
		case *ast.IfStmt:
			if c.fails(n.Body) {
				for _, cmp := range c.inequalities(n.Cond) {
					c.orient(cmp.Pos(), c.resolve(n.Init, cmp.X), c.resolve(n.Init, cmp.Y), record)
				}
			}
		}
		return true
	})

	for _, key := range order {
		g := groups[key]
		if len(g.fields) < c.opts.MinFields {
			continue
		}
		if !c.opts.ReportPartial && !covers(g.typ, g.fields) {
			continue
		}
		named := types.TypeString(deref(g.typ), types.RelativeTo(c.pass.Pkg))
		msg := fmt.Sprintf(message, key.expr, strings.Join(g.fields, ", "), named, named)
		c.pass.Reportf(g.pos, "%s", source.Hint(msg, c.opts.Hint))
	}
}

// orient records actual, or expected when actual is a constant and expected
// is the field: assert.Equal(t, got.Name, "x") swaps the arguments.
func (c *checker) orient(pos token.Pos, actual, expected ast.Expr, record func(token.Pos, ast.Expr, ast.Expr)) {
	if _, ok := c.field(actual); !ok && c.constant(actual) {
		actual, expected = expected, actual
	}
	record(pos, actual, expected)
}

func (c *checker) constant(e ast.Expr) bool {
	tv, ok := c.pass.TypesInfo.Types[e]
	return ok && (tv.Value != nil || tv.IsNil())
}

func (c *checker) callee(call *ast.CallExpr) *types.Func {
	var id *ast.Ident
	switch fun := ast.Unparen(call.Fun).(type) {
	case *ast.Ident:
		id = fun
	case *ast.SelectorExpr:
		id = fun.Sel
	default:
		return nil
	}
	fn, _ := c.pass.TypesInfo.Uses[id].(*types.Func)
	return fn
}

// equality returns the actual and expected arguments of a testify equality,
// found by parameter name so the function and method forms read alike.
func (c *checker) equality(fn *types.Func, call *ast.CallExpr) (actual, expected ast.Expr, ok bool) {
	if !equalities[fn.Name()] {
		return nil, nil, false
	}
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil {
		return nil, nil, false
	}
	for i := range sig.Params().Len() {
		if i >= len(call.Args) {
			break
		}
		switch sig.Params().At(i).Name() {
		case "actual":
			actual = call.Args[i]
		case "expected":
			expected = call.Args[i]
		}
	}
	return actual, expected, actual != nil && expected != nil
}

// fails reports whether body calls a testing failure method outside any
// function literal.
func (c *checker) fails(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			if fn := c.callee(n); fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == "testing" && failures[fn.Name()] {
				found = true
			}
		}
		return true
	})
	return found
}

// inequalities returns the != comparisons a condition fails on, through ||.
func (c *checker) inequalities(cond ast.Expr) []*ast.BinaryExpr {
	b, ok := ast.Unparen(cond).(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	switch b.Op {
	case token.NEQ:
		return []*ast.BinaryExpr{b}
	case token.LOR:
		return append(c.inequalities(b.X), c.inequalities(b.Y)...)
	}
	return nil
}

// resolve follows an identifier the if statement's own init defined, so
// `if got, want := v.Name, "x"; got != want` asserts v.Name.
func (c *checker) resolve(init ast.Stmt, e ast.Expr) ast.Expr {
	id, ok := ast.Unparen(e).(*ast.Ident)
	as, isAssign := init.(*ast.AssignStmt)
	if !ok || !isAssign || as.Tok != token.DEFINE || len(as.Lhs) != len(as.Rhs) {
		return e
	}
	obj := c.pass.TypesInfo.ObjectOf(id)
	for i, lhs := range as.Lhs {
		if l, ok := lhs.(*ast.Ident); ok && obj != nil && c.pass.TypesInfo.Defs[l] == obj {
			return as.Rhs[i]
		}
	}
	return e
}

// field returns e as a struct field selection.
func (c *checker) field(e ast.Expr) (*ast.SelectorExpr, bool) {
	sel, ok := ast.Unparen(e).(*ast.SelectorExpr)
	if !ok {
		return nil, false
	}
	s := c.pass.TypesInfo.Selections[sel]
	return sel, s != nil && s.Kind() == types.FieldVal
}

// root returns the variable e hangs from, through field selections, pointer
// dereferences and constant indexes. Anything else, a call or a variable
// index, may name a different value each time it is evaluated.
func (c *checker) root(e ast.Expr) (*types.Var, bool) {
	for {
		switch x := ast.Unparen(e).(type) {
		case *ast.Ident:
			v, ok := c.pass.TypesInfo.ObjectOf(x).(*types.Var)
			return v, ok
		case *ast.SelectorExpr:
			if s := c.pass.TypesInfo.Selections[x]; s == nil {
				v, ok := c.pass.TypesInfo.Uses[x.Sel].(*types.Var)
				return v, ok
			} else if s.Kind() != types.FieldVal {
				return nil, false
			}
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != token.AND {
				return nil, false
			}
			e = x.X
		case *ast.IndexExpr:
			if !c.constant(x.Index) {
				return nil, false
			}
			e = x.X
		default:
			return nil, false
		}
	}
}

// mutator reports whether calling fn may change its receiver: a pointer
// receiver, or a method reached through an interface.
func (c *checker) mutator(fn *types.Func) bool {
	if fn == nil {
		return false
	}
	sig, _ := fn.Type().(*types.Signature)
	if sig == nil || sig.Recv() == nil {
		return false
	}
	switch sig.Recv().Type().Underlying().(type) {
	case *types.Pointer, *types.Interface:
		return true
	}
	return false
}

// shared reports whether a call handed arg can change what arg reaches.
func (c *checker) shared(arg ast.Expr) bool {
	if u, ok := ast.Unparen(arg).(*ast.UnaryExpr); ok && u.Op == token.AND {
		return true
	}
	switch c.pass.TypesInfo.TypeOf(arg).(type) {
	case *types.Pointer:
		return true
	}
	return false
}

// comparable reports whether a test could build a whole t to compare against:
// a struct, every field of which it can set and a deep comparison can match.
func (c *checker) comparable(t types.Type) bool {
	if t == nil {
		return false
	}
	t = deref(t)
	if _, ok := t.(*types.TypeParam); ok {
		return false
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok || st.NumFields() == 0 {
		return false
	}
	for f := range st.Fields() {
		if !f.Exported() && f.Pkg() != c.pass.Pkg {
			return false
		}
		switch ft := f.Type().Underlying().(type) {
		case *types.Signature, *types.Chan:
			return false
		case *types.Basic:
			if ft.Kind() == types.UnsafePointer {
				return false
			}
		case *types.Interface:
			if !ft.Empty() && !types.Identical(f.Type(), types.Universe.Lookup("error").Type()) {
				return false
			}
		}
	}
	return true
}

// covers reports whether fields names every field t declares, so one
// comparison of the whole value checks exactly what the assertions do.
func covers(t types.Type, fields []string) bool {
	st, _ := deref(t).Underlying().(*types.Struct)
	for f := range st.Fields() {
		if !slices.Contains(fields, f.Name()) {
			return false
		}
	}
	return true
}

func deref(t types.Type) types.Type {
	if p, ok := t.Underlying().(*types.Pointer); ok {
		return p.Elem()
	}
	return t
}

func (c *checker) render(e ast.Expr) string {
	return types.ExprString(ast.Unparen(e))
}
