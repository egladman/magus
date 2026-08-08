package buzz

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/gopherbuzz/ast"
)

// TestInspectKitchenSink drives ast.Inspect over a REAL parse of a program using
// most statement and expression forms, then asserts the node census - which
// kinds were visited and that traversal reached inside each construct. The
// hand-built trees in ast/walk_test.go verify the visitor mechanics; this pins
// the traversal switch against what the parser actually produces, so a node
// kind whose children Inspect forgets to descend into shows up as a missing
// census entry rather than as a silently shallower walk.
func TestInspectKitchenSink(t *testing.T) {
	src := `
fun helper(n: int) > int {
	if (n > 1) { return n * 2; } else { return 0 - n; }
}

object Point {
	x: int,
	y: int,
	fun sum(this: Point) > int { return this.x + this.y; }
}

enum Color { red, green }

fun main() > void {
	var total = 0;
	final list = [1, 2, 3];
	final m = {"k": 1};
	while (total < 10) { total = total + 1; }
	do { total = total - 1; } until (total < 5)
	foreach (x in 1..3) { total = total + x; }
	foreach (item in list) { total = total + item; }
	final p = Point{ x = 1, y = 2 };
	total = total + p.sum() + m["k"] + list[0];
	final anon = fun (a: int) > int { return a + 1; };
	total = anon(total);
	final interp = "total is {total}";
	final cond = total > 0 and total < 1000 or false;
	if (cond) { total = helper(total); }
	if (!cond) { total = 0; }
	helper(total);
	final safe = helper(1) catch 0;
	try {
		throw "boom";
	} catch (e: str) {
		total = total + 1;
	}
}
`
	prog, err := ParseEmbedded(src)
	require.NoError(t, err, "parse")

	census := map[string]int{}
	for _, decl := range prog.Stmts {
		ast.Inspect(decl, func(n ast.Node) bool {
			census[fmt.Sprintf("%T", n)]++
			return true
		})
	}

	// Every construct the source spells must have been VISITED, and the counts
	// for the unambiguous ones must be exact - an off-by-one there means Inspect
	// double-visited or skipped a nesting level.
	wantExact := map[string]int{
		"*ast.FunDecl":     3, // helper, main, and Point.sum
		"*ast.ObjectDecl":  1,
		"*ast.WhileStmt":   1,
		"*ast.DoStmt":      1,
		"*ast.ForEachStmt": 2,
		"*ast.TryStmt":     1,
		"*ast.ThrowStmt":   1,
		"*ast.FunExpr":     1, // the anonymous fun
		"*ast.RangeExpr":   1, // 1..3
		"*ast.InterpExpr":  1, // "total is {total}"
		"*ast.ObjectLit":   1, // Point{...}
		"*ast.MapExpr":     1,
	}
	for kind, want := range wantExact {
		require.Equalf(t, want, census[kind], "census[%s]", kind)
	}

	// Present with parser-dependent counts: assert reached, not how many times.
	wantPresent := []string{
		"*ast.IfStmt", "*ast.ReturnStmt", "*ast.DeclStmt", "*ast.AssignStmt",
		"*ast.ExprStmt", "*ast.BlockStmt", "*ast.BinaryExpr", "*ast.UnaryExpr",
		"*ast.CallExpr", "*ast.MemberExpr", "*ast.IndexExpr", "*ast.ListExpr",
		"*ast.CatchExpr",
	}
	for _, kind := range wantPresent {
		require.Containsf(t, census, kind, "Inspect never visited a %s; either the "+
			"source no longer produces one or the traversal does not descend to it", kind)
	}

	// The early-stop contract on a real tree: stopping at every FunDecl must
	// suppress everything nested inside function bodies.
	stopped := map[string]int{}
	for _, decl := range prog.Stmts {
		ast.Inspect(decl, func(n ast.Node) bool {
			stopped[fmt.Sprintf("%T", n)]++
			_, isFun := n.(*ast.FunDecl)
			return !isFun
		})
	}
	require.Zero(t, stopped["*ast.WhileStmt"], "returning false at FunDecl must prune its body")
	require.Equal(t, 3, stopped["*ast.FunDecl"], "the pruned nodes themselves are still visited")
}
