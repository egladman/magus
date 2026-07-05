// Package ast defines the Buzz abstract syntax tree node types.
package ast

// Node is a Buzz AST node.
type Node interface{ astPos() Pos }

// Pos records the source position of an AST node.
type Pos struct{ Line, Col int }

func (p Pos) astPos() Pos { return p }

// NodePos returns the source position of any Node.
func NodePos(n Node) Pos { return n.astPos() }

// Program is the top-level node.
type Program struct {
	Stmts []Node
}

// ---- statements ----

// ImportStmt: import "module"; / import "module" as alias; / import "module" as _;
type ImportStmt struct {
	Pos
	Path  string
	Alias string // "" = use basename; "_" = flat (no bound name)
}

// NamespaceStmt: namespace name; — declares the module's namespace identifier.
type NamespaceStmt struct {
	Pos
	Name string
}

// DeclStmt: const/var name = expr;
type DeclStmt struct {
	Pos
	IsExported bool
	IsConst    bool
	Name       string
	TypeAnnot  string // optional explicit type, e.g. "int", "[str]"
	Value      Node
}

// AssignStmt: target = value;  (target is *IdentExpr, *MemberExpr, or *IndexExpr)
type AssignStmt struct {
	Pos
	Target Node
	Value  Node
}

// ReturnStmt: return expr?;
type ReturnStmt struct {
	Pos
	Value Node // nil for bare return
}

// ExprStmt: expr;
type ExprStmt struct {
	Pos
	Expr Node
}

// BlockStmt: { stmt* }
type BlockStmt struct {
	Pos
	Stmts []Node
}

// IfStmt: if (cond) then [else elseBranch]. Else is *BlockStmt, *IfStmt, or nil.
type IfStmt struct {
	Pos
	Cond Node
	Then *BlockStmt
	Else Node
	// BindName is set for optional-call narrowing, `if (opt -> name) { ... }`:
	// Cond is the optional expression, and name is bound to its non-null value
	// inside Then. Empty for an ordinary boolean if.
	BindName string
}

// WhileStmt: while (cond) body
type WhileStmt struct {
	Pos
	Cond Node
	Body *BlockStmt
}

// ForStmt: for (init; cond; post) body. Init/Cond/Post may be nil.
type ForStmt struct {
	Pos
	Init Node
	Cond Node
	Post Node
	Body *BlockStmt
}

// ForEachStmt: foreach (val in iter) or foreach (key, val in iter)
type ForEachStmt struct {
	Pos
	KeyName string // "" when only a value binding is present
	ValName string
	Iter    Node
	Body    *BlockStmt
}

// BreakStmt: break;
type BreakStmt struct{ Pos }

// ContinueStmt: continue;
type ContinueStmt struct{ Pos }

// FunDecl: fun name(params) rettype { body } — a named function statement/method.
type FunDecl struct {
	Pos
	IsExported  bool
	Name        string
	Params      []string
	ParamAnnots []string // parallel to Params; "" = unannotated
	RetAnnot    string   // return type annotation; "" = unannotated
	YieldAnnot  string   // yield type annotation after *>; "" = non-fiber function
	Body        *BlockStmt
	// Doc is the documentation comment block immediately preceding the
	// declaration (see token.Token.Doc); "" when undocumented. Carried onto the
	// compiled chunk so host code (spell resolution, magus describe/doctor) can
	// recover a target handler's comment.
	Doc string
}

// TestDecl: test "name" { body } — a named test block (upstream Buzz). Its body
// runs only under the test runner (buzz --test), never during a normal run.
type TestDecl struct {
	Pos
	Name string
	Body *BlockStmt
}

// ObjectDecl: object Name { fields; methods }
type ObjectDecl struct {
	Pos
	IsExported bool
	Name       string
	Fields     []ObjField
	Methods    []*FunDecl
}

// ObjField is a single object field declaration with an optional default.
type ObjField struct {
	Name      string
	TypeAnnot string // e.g. "int", "[str]"; "" = unannotated
	Default   Node   // nil when no default
}

// EnumDecl: enum Name { CASE1, CASE2 }
type EnumDecl struct {
	Pos
	IsExported bool
	Name       string
	Cases      []string
}

// ---- expressions ----

// BinaryExpr: left op right
type BinaryExpr struct {
	Pos
	Op    string
	Left  Node
	Right Node
}

// UnaryExpr: op operand  (op is "-" or "!")
type UnaryExpr struct {
	Pos
	Op      string
	Operand Node
}

// CallExpr: callee(args...). ArgNames is parallel to Args when any argument
// was labeled (upstream Buzz's `f(a: 1, b: 2)` named-argument syntax); "" in
// a slot means that argument was positional. nil when every argument was
// positional. The checker resolves labels against the callee's parameter
// names and reorders Args, so later stages never see them.
type CallExpr struct {
	Pos
	Callee   Node
	Args     []Node
	ArgNames []string
	// TypeArg holds an explicit generic type argument, e.g. "double" from
	// `buf.readZAt::<double>(...)`. Empty for ordinary calls. The VM ignores it;
	// the checker uses it as the call's result type for generic accessors whose
	// return type the type arg names.
	TypeArg string
}

// MemberExpr: object.name
type MemberExpr struct {
	Pos
	Object Node
	Name   string
}

// IndexExpr: object[index]. Optional is set for the checked subscript form
// object[?index], which yields null on an out-of-bounds index instead of an
// error (Buzz null-safety).
type IndexExpr struct {
	Pos
	Object   Node
	Index    Node
	Optional bool
}

// ForceExpr: operand! — force-unwraps an optional, erroring at runtime if the
// value is null.
type ForceExpr struct {
	Pos
	Operand Node
}

// PatLit: $"regex" — a pattern (pat) literal.
type PatLit struct {
	Pos
	Pattern string
}

// FunExpr: fun(params) type { body }
type FunExpr struct {
	Pos
	Params      []string
	ParamAnnots []string // parallel to Params; "" = unannotated
	RetAnnot    string   // return type annotation; "" = unannotated
	YieldAnnot  string   // yield type annotation after *>; "" = non-fiber function
	Body        *BlockStmt
}

// MapExpr: {"key": val, ...}. Mut is set for the `mut {…}` form (a mutable map);
// a plain map literal is immutable.
type MapExpr struct {
	Pos
	Keys   []Node // key expressions (string literals or arbitrary exprs)
	Values []Node
	Mut    bool
}

// ListExpr: [val, ...]. Mut is set for the `mut [...]` form (a mutable list); a
// plain list literal is immutable.
type ListExpr struct {
	Pos
	Items []Node
	Mut   bool
	// ElemType holds the element-type annotation of an empty typed-list literal
	// `[<T>]` (e.g. "str"). Empty when the literal has items or no annotation.
	ElemType string
}

// ObjectLit: TypeName{ field = val, ... }. Mut is set for `mut TypeName{…}` (a
// mutable instance); a plain object literal is immutable.
type ObjectLit struct {
	Pos
	TypeName string
	Keys     []string
	Values   []Node
	Mut      bool
}

// InterpExpr: "text {expr} ..." — alternating literal and expression parts.
type InterpExpr struct {
	Pos
	Parts []InterpPart
}

// InterpPart is one piece of an interpolated string.
type InterpPart struct {
	Lit  string // literal text when Expr == nil
	Expr Node   // embedded expression, else nil
}

// IdentExpr: name
type IdentExpr struct {
	Pos
	Name string
}

// StringLit: "..."
type StringLit struct {
	Pos
	Val string
}

// IntLit: 42
type IntLit struct {
	Pos
	Val int64
}

// FloatLit: 3.14
type FloatLit struct {
	Pos
	Val float64
}

// BoolLit: true/false
type BoolLit struct {
	Pos
	Val bool
}

// NullLit: null
type NullLit struct{ Pos }

// DoStmt: do { body } until (cond);
type DoStmt struct {
	Pos
	Body *BlockStmt
	Cond Node
}

// RangeExpr: lo..hi
type RangeExpr struct {
	Pos
	Lo Node
	Hi Node
}

// IsExpr: expr is TypeName
type IsExpr struct {
	Pos
	Expr     Node
	TypeName string
}

// AsExpr: expr as TypeName
type AsExpr struct {
	Pos
	Expr     Node
	TypeName string
	// Optional marks the `as?` form (upstream Buzz): a cast that yields null on
	// a type mismatch instead of erroring. Plain `as` coerces or errors.
	Optional bool
}

// CatchExpr: callExpr catch defaultExpr — upstream Buzz's inline catch. If the
// call throws, the expression evaluates to Default instead. The thrown error is
// not bound (matching upstream's call-suffix form).
type CatchExpr struct {
	Pos
	Expr    Node
	Default Node
}

// TryStmt: try { body } catch (name) { handler }
type TryStmt struct {
	Pos
	Body    *BlockStmt
	ErrName string // catch binding name
	Catch   *BlockStmt
}

// ThrowStmt: throw expr;
type ThrowStmt struct {
	Pos
	Value Node
}

// YieldExpr: yield expr — suspends the current fiber and returns a value to the resumer.
// Outside a fiber the yield value is evaluated and dismissed; the expression evaluates to null.
type YieldExpr struct {
	Pos
	Value Node
}

// FiberExpr: &call(args) — wraps a function call in a suspended fiber without executing it.
type FiberExpr struct {
	Pos
	Call *CallExpr
}

// ResumeExpr: resume fiber — runs the fiber to the next yield or completion.
// Returns the yielded value, or null if nothing was yielded or the fiber is over.
type ResumeExpr struct {
	Pos
	Fiber Node
}

// ResolveExpr: resolve fiber — runs the fiber to completion, ignoring all yields.
// Returns the function's return value; callable after the fiber is over.
type ResolveExpr struct {
	Pos
	Fiber Node
}
