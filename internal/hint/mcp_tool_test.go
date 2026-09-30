package hint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The sentence is quoted verbatim by the tool description, the MCP guide and the
// changelog; the minutes come from the bound the handler enforces.
func TestClientBoundSentence(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "Bounded at 10 minutes when called directly; a host that supports MCP tasks can run it as a task without that bound.", ClientBoundSentence())
}

// TestAllDeclaredToolsAreRegistered fails if a ToolName is declared but left out
// of AllToolNames, so the drift tests keep walking the full set.
//
// It reads the declarations out of the source rather than comparing AllToolNames
// against a second hand-written list: the hand-written version is forgotten in
// both places at once, which is exactly what forgetting looks like, and the
// counts stay equal. clicommand_test.go's twin records the incident that taught
// this: ServerReload was declared, routed on, and outside the guard for as long
// as it existed.
//
// Go cannot enumerate its own package-level consts at runtime, so the source is
// the only place the full set exists.
func TestAllDeclaredToolsAreRegistered(t *testing.T) {
	t.Parallel()

	registered := map[string]bool{}
	for _, tn := range AllToolNames {
		registered[tn.String()] = true
	}
	for _, name := range declaredToolNames(t) {
		if !registered[name] {
			t.Errorf("%q is declared but missing from AllToolNames, so no drift test walks it", name)
		}
	}
}

// declaredToolNames returns every tool name declared as a const in mcp_tool.go,
// read from the file so a new declaration is picked up without anyone remembering
// to list it. An untyped const counts too: it converts implicitly where
// AllToolNames is built, so it is exactly as registrable and exactly as
// forgettable as a ToolName-typed one.
func declaredToolNames(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "mcp_tool.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing mcp_tool.go: %v", err)
	}
	var out []string
	for _, d := range f.Decls {
		gen, ok := d.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, s := range gen.Specs {
			spec := s.(*ast.ValueSpec)
			if spec.Type != nil {
				id, isIdent := spec.Type.(*ast.Ident)
				if !isIdent || id.Name != "ToolName" {
					continue
				}
			}
			for _, v := range spec.Values {
				lit, isLit := v.(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					t.Fatalf("tool-name const declared with a non-literal value; this test reads literals only")
				}
				out = append(out, lit.Value[1:len(lit.Value)-1])
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no tool-name consts; the test is reading the wrong file")
	}
	return out
}
