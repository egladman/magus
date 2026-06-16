package buzz_test

import (
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/ast"
)

func TestParse_ValidProgram(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"empty", ""},
		{"literal", `var x: int = 42;`},
		{"function", `fun add(a: int, b: int) > int { return a + b; }`},
		{"if statement", `if (true) { var x: int = 1; }`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prog, err := buzz.Parse(tc.src)
			if err != nil {
				t.Fatalf("Parse(%q): unexpected error: %v", tc.src, err)
			}
			if prog == nil {
				t.Fatal("Parse returned nil program without error")
			}
		})
	}
}

// A `::<T>` generic call argument must be captured on the CallExpr so the
// checker can use it as the call's result type (upstream Buzz semantics).
func TestParse_GenericCallTypeArg(t *testing.T) {
	prog, err := buzz.Parse(`final x = b.readZAt::<double>(at: 0);`)
	if err != nil {
		t.Fatalf("Parse: unexpected error: %v", err)
	}
	decl, ok := prog.Stmts[0].(*ast.DeclStmt)
	if !ok {
		t.Fatalf("stmt 0 is %T, want *ast.DeclStmt", prog.Stmts[0])
	}
	call, ok := decl.Value.(*ast.CallExpr)
	if !ok {
		t.Fatalf("decl value is %T, want *ast.CallExpr", decl.Value)
	}
	if call.TypeArg != "double" {
		t.Errorf("CallExpr.TypeArg = %q, want %q", call.TypeArg, "double")
	}
}

func TestParse_InvalidSyntax(t *testing.T) {
	cases := []string{
		`fun (`, // incomplete function
		`var x: = ;`, // missing type
	}
	for _, src := range cases {
		_, err := buzz.Parse(src)
		if err == nil {
			t.Errorf("Parse(%q): expected error, got nil", src)
		}
	}
}
