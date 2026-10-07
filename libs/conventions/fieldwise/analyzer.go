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
// the left of !=; the other side counts when that one is a constant or a field
// of a table case. When both sides are fields of two values, the pair counts
// only if the two values have identical types, since nothing else says which
// one is under test.
//
// A value stops being the same value at a statement that may change it: an
// assignment to it or through it, or a call handed it as a pointer, slice or
// map, including one reached through a variable index. A variable that holds
// its address, or shares its pointers, slices or maps, changes it too. Not
// reported:
//
//   - a value asserted on fewer than two distinct fields;
//   - a value a function literal may change, since the literal may run
//     between any two of the assertions;
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
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/types/typeutil"
)

const message = "%s is asserted one field at a time (%s): build the whole expected %s and compare it " +
	"once, normalizing volatile fields first, so a field added to %s cannot slip past this test"

// minFields keeps a lone field out: one assertion is not field by field, even
// on a struct that declares only that field.
const minFields = 2

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

// Options configures the analyzer returned by [New]. The zero value reports
// only a value whose assertions name every field its struct declares.
type Options struct {
	// ReportPartial also reports a value asserted on two or more, but not all,
	// of its fields. Its fix compares fields the test never looked at, so it
	// can fail on a value the test was content with until a volatile field is
	// normalized: a different kind of edit from folding assertions that
	// already name every field.
	ReportPartial bool `json:"report-partial"`
}

// New returns the analyzer configured by opts. Every Options is valid; the
// error is the shape the plugin registers each analyzer with.
func New(opts Options) (*analysis.Analyzer, error) {
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
						c.ranged[pass.TypesInfo.ObjectOf(id)] = true
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

// unit holds what one function body has seen so far. Variables that share
// memory sit in one class, so a change through any of them moves them all.
type unit struct {
	gens     map[*types.Var]int
	classes  map[*types.Var]*[]*types.Var
	captured map[*types.Var]bool
	groups   map[value]*group
	order    []value
}

func (u *unit) class(v *types.Var) []*types.Var {
	if members, ok := u.classes[v]; ok {
		return *members
	}
	return []*types.Var{v}
}

func (u *unit) join(a, b *types.Var) {
	ma, oka := u.classes[a]
	if !oka {
		ma = &[]*types.Var{a}
		u.classes[a] = ma
	}
	mb, okb := u.classes[b]
	if ma == mb {
		return
	}
	if !okb {
		mb = &[]*types.Var{b}
	}
	for _, v := range *mb {
		*ma = append(*ma, v)
		u.classes[v] = ma
	}
}

func (u *unit) bump(v *types.Var) {
	for _, m := range u.class(v) {
		u.gens[m]++
	}
}

func (c *checker) unit(body *ast.BlockStmt) {
	u := &unit{
		gens:     map[*types.Var]int{},
		classes:  map[*types.Var]*[]*types.Var{},
		captured: map[*types.Var]bool{},
		groups:   map[value]*group{},
	}

	ast.Inspect(body, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			outside := func(v *types.Var) bool { return v != nil && (v.Pos() < lit.Pos() || v.Pos() >= lit.End()) }
			ast.Inspect(lit.Body, func(n ast.Node) bool {
				for _, e := range c.writes(n) {
					if v := c.base(e); outside(v) {
						u.captured[v] = true
					}
				}
				if as, ok := n.(*ast.AssignStmt); ok {
					for _, r := range as.Rhs {
						for _, v := range c.references(r) {
							if outside(v) {
								u.captured[v] = true
							}
						}
					}
				}
				return true
			})
			return false
		}
		return true
	})

	record := func(pos token.Pos, operand, other ast.Expr) {
		sel, ok := c.field(operand)
		if !ok {
			return
		}
		if o, ok := c.field(other); ok && !c.counterpart(sel, o) {
			return
		}
		root, ok := c.root(sel.X)
		if !ok || c.ranged[root] {
			return
		}
		typ := c.pass.TypesInfo.TypeOf(sel.X)
		if !c.comparable(typ) {
			return
		}
		key := value{root: root, expr: render(sel.X), gen: u.gens[root]}
		g, ok := u.groups[key]
		if !ok {
			g = &group{pos: pos, typ: typ}
			u.groups[key] = g
			u.order = append(u.order, key)
		}
		if !slices.Contains(g.fields, sel.Sel.Name) {
			g.fields = append(g.fields, sel.Sel.Name)
		}
	}

	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.CallExpr:
			if fn, ok := typeutil.Callee(c.pass.TypesInfo, n).(*types.Func); ok && testify(fn) {
				if actual, expected, ok := c.equality(fn, n); ok {
					c.orient(n.Pos(), actual, expected, record)
				}
				return true
			}
		case *ast.IfStmt:
			if c.fails(n.Body) {
				for _, cmp := range c.inequalities(n.Cond) {
					c.orient(cmp.Pos(), c.resolve(n.Init, cmp.X), c.resolve(n.Init, cmp.Y), record)
				}
			}
		}
		for _, e := range c.writes(n) {
			if v := c.base(e); v != nil {
				u.bump(v)
			}
		}
		c.aliases(n, u)
		return true
	})

	for _, key := range u.order {
		g := u.groups[key]
		if len(g.fields) < minFields || slices.ContainsFunc(u.class(key.root), func(v *types.Var) bool { return u.captured[v] }) {
			continue
		}
		if !c.opts.ReportPartial && !covers(g.typ, g.fields) {
			continue
		}
		named := types.TypeString(deref(g.typ), types.RelativeTo(c.pass.Pkg))
		c.pass.Reportf(g.pos, message, key.expr, strings.Join(g.fields, ", "), named, named)
	}
}

func testify(fn *types.Func) bool {
	if fn.Pkg() == nil {
		return false
	}
	path := fn.Pkg().Path()
	return path == testifyAssert || path == testifyRequire
}

// orient records actual, or expected when actual is a constant or a field of
// a table case and expected is a field: assert.Equal(t, got.Name, "x") and
// `if tt.name != got.Name` both put the value under test second.
func (c *checker) orient(pos token.Pos, actual, expected ast.Expr, record func(token.Pos, ast.Expr, ast.Expr)) {
	if _, ok := c.field(expected); ok && (c.constant(actual) || c.tableCase(actual)) {
		actual, expected = expected, actual
	}
	record(pos, actual, expected)
}

func (c *checker) constant(e ast.Expr) bool {
	tv, ok := c.pass.TypesInfo.Types[e]
	return ok && (tv.Value != nil || tv.IsNil())
}

func (c *checker) tableCase(e ast.Expr) bool {
	sel, ok := c.field(e)
	if !ok {
		return false
	}
	root, ok := c.root(sel.X)
	return ok && c.ranged[root]
}

// counterpart reports whether the field sel is asserted against may stand for
// it: a field of a table case, or the same field of another value of sel's
// type. Two fields of one value compared with each other assert neither.
func (c *checker) counterpart(sel, other *ast.SelectorExpr) bool {
	if render(other.X) == render(sel.X) {
		return false
	}
	info := c.pass.TypesInfo
	return c.tableCase(other) || types.Identical(info.TypeOf(other.X), info.TypeOf(sel.X))
}

// equality returns the actual and expected arguments of a testify equality,
// found by parameter name so the function and method forms read alike.
func (c *checker) equality(fn *types.Func, call *ast.CallExpr) (actual, expected ast.Expr, ok bool) {
	if !equalities[fn.Name()] {
		return nil, nil, false
	}
	params := fn.Signature().Params()
	for i := range min(params.Len(), len(call.Args)) {
		switch params.At(i).Name() {
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
			fn, ok := typeutil.Callee(c.pass.TypesInfo, n).(*types.Func)
			if ok && fn.Pkg() != nil && fn.Pkg().Path() == "testing" && failures[fn.Name()] {
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
		if l, ok := lhs.(*ast.Ident); ok && c.pass.TypesInfo.Defs[l] == obj {
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

// base returns the variable whose memory a write to e may reach, through any
// index or slice: `xs[i] = v` changes xs[0] when i is 0. Unlike root it never
// gives up on an index, so it suits invalidating, never recording.
func (c *checker) base(e ast.Expr) *types.Var {
	for {
		switch x := ast.Unparen(e).(type) {
		case *ast.Ident:
			v, _ := c.pass.TypesInfo.ObjectOf(x).(*types.Var)
			return v
		case *ast.SelectorExpr:
			s := c.pass.TypesInfo.Selections[x]
			if s == nil {
				v, _ := c.pass.TypesInfo.Uses[x.Sel].(*types.Var)
				return v
			}
			if s.Kind() != types.FieldVal {
				return nil
			}
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.UnaryExpr:
			if x.Op != token.AND {
				return nil
			}
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.IndexListExpr:
			e = x.X
		case *ast.SliceExpr:
			e = x.X
		case *ast.TypeAssertExpr:
			e = x.X
		default:
			return nil
		}
	}
}

// writes returns the expressions n may change: what it assigns, the receiver
// of a method that may change it, and each argument a call can write through.
// testify only reads its arguments.
func (c *checker) writes(n ast.Node) []ast.Expr {
	switch n := n.(type) {
	case *ast.AssignStmt:
		return n.Lhs
	case *ast.IncDecStmt:
		return []ast.Expr{n.X}
	case *ast.RangeStmt:
		if n.Tok == token.ASSIGN {
			return slices.DeleteFunc([]ast.Expr{n.Key, n.Value}, func(e ast.Expr) bool { return e == nil })
		}
	case *ast.CallExpr:
		fn, _ := typeutil.Callee(c.pass.TypesInfo, n).(*types.Func)
		if fn != nil && testify(fn) {
			return nil
		}
		var out []ast.Expr
		if sel, ok := ast.Unparen(n.Fun).(*ast.SelectorExpr); ok && fn != nil && mutator(fn) {
			out = append(out, sel.X)
		}
		for _, arg := range n.Args {
			if c.shared(arg) {
				out = append(out, arg)
			}
		}
		return out
	}
	return nil
}

// aliases joins each variable n stores a reference into with the variables
// that reference reaches, so `p := &got` makes a write through p a write to
// got. A reference stored nowhere a variable names, sent on a channel say,
// is not followed.
func (c *checker) aliases(n ast.Node, u *unit) {
	var lhs, rhs []ast.Expr
	switch n := n.(type) {
	case *ast.AssignStmt:
		lhs, rhs = n.Lhs, n.Rhs
	case *ast.ValueSpec:
		for _, name := range n.Names {
			lhs = append(lhs, name)
		}
		rhs = n.Values
	default:
		return
	}
	var reached []*types.Var
	for _, r := range rhs {
		reached = append(reached, c.references(r)...)
	}
	for _, l := range lhs {
		if v := c.base(l); v != nil {
			for _, r := range reached {
				u.join(v, r)
			}
		}
	}
}

// references returns the variables e hands out a reference into: an address,
// a pointer, slice or map it holds, or one a composite literal or a call
// returning a reference carries along. A call returning only values, such as
// json.Unmarshal(b, &got), keeps nothing of its arguments.
func (c *checker) references(e ast.Expr) []*types.Var {
	var out []*types.Var
	switch x := ast.Unparen(e).(type) {
	case *ast.UnaryExpr:
		if x.Op == token.AND {
			out = append(out, c.base(x.X))
		}
	case *ast.CompositeLit:
		for _, elt := range x.Elts {
			if kv, ok := elt.(*ast.KeyValueExpr); ok {
				elt = kv.Value
			}
			out = append(out, c.references(elt)...)
		}
	case *ast.CallExpr:
		if !carries(c.pass.TypesInfo.TypeOf(x)) {
			return nil
		}
		if sel, ok := ast.Unparen(x.Fun).(*ast.SelectorExpr); ok {
			if s := c.pass.TypesInfo.Selections[sel]; s != nil && s.Kind() == types.MethodVal {
				out = append(out, c.base(sel.X))
			}
		}
		for _, arg := range x.Args {
			out = append(out, c.references(arg)...)
		}
	case *ast.Ident, *ast.SelectorExpr, *ast.IndexExpr, *ast.StarExpr, *ast.SliceExpr, *ast.TypeAssertExpr:
		if carries(c.pass.TypesInfo.TypeOf(x)) {
			out = append(out, c.base(x))
		}
	}
	return slices.DeleteFunc(out, func(v *types.Var) bool { return v == nil })
}

// carries reports whether a t shares memory with what it was copied from: a
// reference, or a struct, array or result list holding one.
func carries(t types.Type) bool {
	if reference(t) {
		return true
	}
	switch t := t.(type) {
	case *types.Tuple:
		for v := range t.Variables() {
			if carries(v.Type()) {
				return true
			}
		}
		return false
	case nil:
		return false
	}
	switch u := t.Underlying().(type) {
	case *types.Struct:
		for f := range u.Fields() {
			if carries(f.Type()) {
				return true
			}
		}
	case *types.Array:
		return carries(u.Elem())
	}
	return false
}

// mutator reports whether calling fn may change its receiver: a pointer,
// slice or map receiver, or a method reached through an interface.
func mutator(fn *types.Func) bool {
	recv := fn.Signature().Recv()
	if recv == nil {
		return false
	}
	_, isInterface := recv.Type().Underlying().(*types.Interface)
	return isInterface || reference(recv.Type())
}

// shared reports whether a call handed arg can change what arg reaches: its
// address, a pointer, slice or map, or an interface variable, which may hold
// the address of a value a variable aliases.
func (c *checker) shared(arg ast.Expr) bool {
	arg = ast.Unparen(arg)
	if u, ok := arg.(*ast.UnaryExpr); ok && u.Op == token.AND {
		return true
	}
	t := c.pass.TypesInfo.TypeOf(arg)
	if _, ok := arg.(*ast.Ident); ok && types.IsInterface(t) {
		return true
	}
	return reference(t)
}

// reference reports whether a copy of a t shares memory with the original.
// Underlying sees through aliases and defined types: `type P = *T` and
// `type P *T` are both pointers.
func reference(t types.Type) bool {
	if t == nil {
		return false
	}
	switch t.Underlying().(type) {
	case *types.Pointer, *types.Slice, *types.Map:
		return true
	}
	return false
}

// comparable reports whether a test could build a whole t to compare against:
// a struct, every field of which it can set and a deep comparison can match.
func (c *checker) comparable(t types.Type) bool {
	st, ok := deref(t).Underlying().(*types.Struct)
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

func render(e ast.Expr) string {
	return types.ExprString(ast.Unparen(e))
}
