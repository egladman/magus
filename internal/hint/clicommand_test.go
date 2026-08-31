package hint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestCommandRender(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"string", QueryOutput.String(), "magus query output"},
		{"string via fmt", QueryOutput.String(), QueryOutput.String()},
		{"with no args", Run.With(), "magus run"},
		{"with one arg", QueryOutput.With("out1a2b3c"), "magus query output out1a2b3c"},
		{"with two args", QueryOutput.With("out1a2b3c", "--open"), "magus query output out1a2b3c --open"},
		{"single-token head", Status.Head(), "status"},
		{"multi-token head", GraphExport.Head(), "graph"},
		{"single-token leaf", Status.Leaf(), "status"},
		{"multi-token leaf", GraphExport.Leaf(), "export"},
		{"deep leaf", MCPTokenGenerate.Leaf(), "generate"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestAllDeclaredAreRegistered fails if a Command is declared but left out of AllCommands,
// so the drift test in cmd/magus keeps walking the full set.
//
// It reads the declarations out of the source rather than comparing AllCommands against a
// second hand-written list. That is not pedantry: the hand-written version compared LENGTHS
// against a copy of AllCommands itself, so forgetting a command in both places - which is
// exactly what forgetting looks like - kept the counts equal and the test green.
// ServerReload was declared, routed on by serverCmd, and absent from the registry for as
// long as it existed.
//
// Go cannot enumerate its own package-level vars at runtime, so the source is the only place
// the full set exists.
func TestAllDeclaredAreRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, c := range AllCommands {
		registered[c.String()] = true
	}
	for _, name := range declaredCommands(t) {
		if !registered[name] {
			t.Errorf("%q is declared but missing from AllCommands, so no drift test walks it", name)
		}
	}
}

// declaredCommands returns every "magus ..." path declared with cmd() in clicommand.go,
// read from the file so a new declaration is picked up without anyone remembering to
// list it.
func declaredCommands(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "clicommand.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing clicommand.go: %v", err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, isIdent := call.Fun.(*ast.Ident); !isIdent || id.Name != "cmd" {
			return true
		}
		tokens := make([]string, 0, len(call.Args))
		for _, a := range call.Args {
			lit, isLit := a.(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				t.Fatalf("cmd() called with a non-literal argument; this test reads literals only")
			}
			tokens = append(tokens, lit.Value[1:len(lit.Value)-1])
		}
		out = append(out, Command{tokens: tokens}.String())
		return true
	})
	if len(out) == 0 {
		t.Fatal("found no cmd() declarations; the test is reading the wrong file")
	}
	return out
}
