package ast

// Inspect walks the AST rooted at n in depth-first order, calling fn for every
// node; fn returns false to stop descending into that node's children. It is the
// go/ast.Inspect analogue for the Buzz AST, and it lives here with the node types
// it traverses so that a new node kind and its traversal are added together (a
// consumer's copy of this switch would silently miss the new nesting). Concrete-
// pointer children are nil-checked before recursion so a nil *BlockStmt is not
// wrapped into a non-nil Node interface.
func Inspect(n Node, fn func(Node) bool) {
	if n == nil || !fn(n) {
		return
	}
	switch s := n.(type) {
	case *DeclStmt:
		Inspect(s.Value, fn)
	case *AssignStmt:
		Inspect(s.Target, fn)
		Inspect(s.Value, fn)
	case *ReturnStmt:
		Inspect(s.Value, fn)
	case *ExprStmt:
		Inspect(s.Expr, fn)
	case *BlockStmt:
		for _, c := range s.Stmts {
			Inspect(c, fn)
		}
	case *IfStmt:
		Inspect(s.Cond, fn)
		if s.Then != nil {
			Inspect(s.Then, fn)
		}
		Inspect(s.Else, fn)
	case *WhileStmt:
		Inspect(s.Cond, fn)
		if s.Body != nil {
			Inspect(s.Body, fn)
		}
	case *ForStmt:
		Inspect(s.Init, fn)
		Inspect(s.Cond, fn)
		Inspect(s.Post, fn)
		if s.Body != nil {
			Inspect(s.Body, fn)
		}
	case *ForEachStmt:
		Inspect(s.Iter, fn)
		if s.Body != nil {
			Inspect(s.Body, fn)
		}
	case *FunDecl:
		if s.Body != nil {
			Inspect(s.Body, fn)
		}
	case *TestDecl:
		if s.Body != nil {
			Inspect(s.Body, fn)
		}
	case *ObjectDecl:
		for i := range s.Fields {
			Inspect(s.Fields[i].Default, fn)
		}
		for _, m := range s.Methods {
			Inspect(m, fn)
		}
	case *BinaryExpr:
		Inspect(s.Left, fn)
		Inspect(s.Right, fn)
	case *UnaryExpr:
		Inspect(s.Operand, fn)
	case *CallExpr:
		Inspect(s.Callee, fn)
		for _, a := range s.Args {
			Inspect(a, fn)
		}
	case *MemberExpr:
		Inspect(s.Object, fn)
	case *IndexExpr:
		Inspect(s.Object, fn)
		Inspect(s.Index, fn)
	case *ForceExpr:
		Inspect(s.Operand, fn)
	case *FunExpr:
		if s.Body != nil {
			Inspect(s.Body, fn)
		}
	case *MapExpr:
		for _, k := range s.Keys {
			Inspect(k, fn)
		}
		for _, v := range s.Values {
			Inspect(v, fn)
		}
	case *ListExpr:
		for _, it := range s.Items {
			Inspect(it, fn)
		}
	case *ObjectLit:
		for _, v := range s.Values {
			Inspect(v, fn)
		}
	case *InterpExpr:
		for i := range s.Parts {
			Inspect(s.Parts[i].Expr, fn)
		}
	case *DoStmt:
		if s.Body != nil {
			Inspect(s.Body, fn)
		}
		Inspect(s.Cond, fn)
	case *RangeExpr:
		Inspect(s.Lo, fn)
		Inspect(s.Hi, fn)
	case *IsExpr:
		Inspect(s.Expr, fn)
	case *AsExpr:
		Inspect(s.Expr, fn)
	case *CatchExpr:
		Inspect(s.Expr, fn)
		Inspect(s.Default, fn)
	case *TryStmt:
		if s.Body != nil {
			Inspect(s.Body, fn)
		}
		if s.Catch != nil {
			Inspect(s.Catch, fn)
		}
	case *ThrowStmt:
		Inspect(s.Value, fn)
	case *YieldExpr:
		Inspect(s.Value, fn)
	case *FiberExpr:
		if s.Call != nil {
			Inspect(s.Call, fn)
		}
	case *ResumeExpr:
		Inspect(s.Fiber, fn)
	case *ResolveExpr:
		Inspect(s.Fiber, fn)
	}
}
