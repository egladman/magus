package hint

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestFollowUpError and TestFollowUpSuccess pin the exact line each
// tool+outcome earns, byte for byte: these strings are appended verbatim to MCP
// results, so a drifted word here is a drifted agent surface.
func TestFollowUpError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		tool ToolName
		want string
	}{
		{
			name: "run_target error points at describe",
			tool: ToolRunTarget,
			want: "next: list valid targets with magus_describe (kind=targets)",
		},
		{
			name: "run_affected error points at describe",
			tool: ToolRunAffected,
			want: "next: list valid targets with magus_describe (kind=targets)",
		},
		{
			name: "where error points at describe projects",
			tool: ToolWhere,
			want: "next: list projects with magus_describe (kind=projects)",
		},
		{
			name: "output error explains where refs come from",
			tool: ToolOutput,
			want: "next: output refs come from magus_run_target or magus_tail_log",
		},
		{
			name: "explain error recovers via query",
			tool: ToolExplain,
			want: "next: locate a node with magus_query, then explain it",
		},
		{
			name: "path error recovers via query",
			tool: ToolPath,
			want: "next: locate the endpoints with magus_query",
		},
		{
			name: "refs error recovers via query",
			tool: ToolRefs,
			want: "next: locate a symbol with magus_query",
		},
		{
			name: "unmapped tool error gets no hint",
			tool: ToolStats,
			want: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FollowUpError(tt.tool))
		})
	}
}

func TestFollowUpSuccess(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tool      ToolName
		mintedRef string
		want      string
	}{
		{
			name: "affected_plan success chains into run_affected",
			tool: ToolAffectedPlan,
			want: "next: run the affected set with magus_run_affected",
		},
		{
			name:      "run success carrying a ref chains into output naming the ref",
			tool:      ToolRunTarget,
			mintedRef: "out1a2b3c4d",
			want:      "next: fetch the captured output with magus_output (ref=out1a2b3c4d)",
		},
		{
			name: "run success with no ref gets no chain hint",
			tool: ToolRunAffected,
			want: "",
		},
		{
			name:      "ref on a non-minting tool earns nothing",
			tool:      ToolQuery,
			mintedRef: "out1a2b3c4d",
			want:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FollowUpSuccess(tt.tool, tt.mintedRef))
		})
	}

	// A plain success from a read tool earns nothing - output bytes are the
	// agent's context cost, so silent successes stay lean.
	for _, tool := range []ToolName{ToolQuery, ToolExplain, ToolStats, ToolDescribe, ToolWhere} {
		assert.Empty(t, FollowUpSuccess(tool, ""), "no follow-up for a plain %s success", tool)
	}
}

// TestAllDeclaredToolsAreRegistered fails if a ToolName is declared but left out
// of AllToolNames, so TestMCPToolHintsResolve keeps walking the full set.
//
// It reads the declarations out of the source rather than comparing AllToolNames
// against a second hand-written list: the hand-written version is forgotten in
// both places at once, which is exactly what forgetting looks like, and the
// counts stay equal. clicommand_test.go's twin records the incident that taught
// this - ServerReload was declared, routed on, and outside the guard for as long
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

// declaredToolNames returns every tool name declared as a const in mcptool.go,
// read from the file so a new declaration is picked up without anyone remembering
// to list it. An untyped const counts too: it converts implicitly where
// AllToolNames is built, so it is exactly as registrable and exactly as
// forgettable as a ToolName-typed one.
func declaredToolNames(t *testing.T) []string {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "mcptool.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing mcptool.go: %v", err)
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

func TestMintsRef(t *testing.T) {
	t.Parallel()

	assert.True(t, MintsRef(ToolRunTarget))
	assert.True(t, MintsRef(ToolRunAffected))
	assert.False(t, MintsRef(ToolQuery))
	assert.False(t, MintsRef(ToolAffectedPlan))
}
