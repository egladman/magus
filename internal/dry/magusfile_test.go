package dry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A real magusfile that imports a spell and calls magus.* must lint clean: the
// browser-safe host setup has to bind those names, or valid files would light up
// with spurious "undefined" squiggles.
func TestDiagnostics_CleanMagusfile(t *testing.T) {
	got := Diagnostics(context.Background(), sampleMagusfile)
	assert.Empty(t, got, "a valid magusfile should report no diagnostics, got %+v", got)
}

func TestDiagnostics_MultipleErrorsSorted(t *testing.T) {
	// Two undefined references on different lines; both must surface (Exec would
	// stop at the first), sorted by position.
	src := "export fun a(ctx: magus\\Context, args: [str]) > void { missingOne(); }\n" +
		"export fun b(ctx: magus\\Context, args: [str]) > void { missingTwo(); }"
	got := Diagnostics(context.Background(), src)
	require.Len(t, got, 2, "both undefined references should be reported, got %+v", got)
	assert.Equal(t, 1, got[0].Line)
	assert.Contains(t, got[0].Msg, "missingOne")
	assert.Equal(t, 2, got[1].Line)
	assert.Contains(t, got[1].Msg, "missingTwo")
}

// New-in-0.6 syntax (expression-body / arrow functions) must lint clean through
// Diagnostics: the checker accepts it, so the editor must not squiggle it.
func TestDiagnostics_ArrowBodyClean(t *testing.T) {
	got := Diagnostics(context.Background(), "export fun triple(x: int) > int => x * 3;")
	assert.Empty(t, got, "arrow-body function should lint clean, got %+v", got)
}

func TestExecs(t *testing.T) {
	const src = `
import "magus";
import "proc";

fun delegate(script: str) > void !> any { proc\exec("pnpm", ["run", script]); }

export fun build(ctx: magus\Context, args: [str]) > void !> any {
    final r = proc\exec("go", ["build", "./..."]);
    if (r.ok) {
        delegate("bundle");
    }
}
export fun lint(ctx: magus\Context, args: [str]) > void !> any {
    final sh = proc\shell("npm run lint && echo done");
    proc\exec(sh.bin, sh.args);
}
export fun fmt(ctx: magus\Context, args: [str]) > void {}
`
	got, diag := Execs(context.Background(), src)
	require.Nil(t, diag)
	assert.Equal(t, []Op{
		{Target: "build", Kind: "exec", Name: "go", Detail: "build ./...", Argv: []string{"go", "build", "./..."}},
		{Target: "build", Kind: "exec", Name: "pnpm", Detail: "run bundle", Argv: []string{"pnpm", "run", "bundle"}},
		{Target: "lint", Kind: "exec", Name: "/bin/sh", Detail: "-c npm run lint && echo done",
			Argv: []string{"/bin/sh", "-c", "npm run lint && echo done"}},
	}, got)
}

func TestExecs_spellBufferAndFailure(t *testing.T) {
	got, diag := Execs(context.Background(), twoOpSpell)
	assert.Nil(t, diag)
	assert.Empty(t, got, "a spell buffer's ops are declared, not traced")

	got, diag = Execs(context.Background(), "export fun a(ctx: magus\\Context, args: [str]) > void { var x = ; }")
	assert.Empty(t, got)
	assert.NotNil(t, diag)
}

func TestDiagnostics_ParseError(t *testing.T) {
	got := Diagnostics(context.Background(), "export fun a(ctx: magus\\Context, args: [str]) > void { var x = ; }")
	require.Len(t, got, 1)
	assert.NotZero(t, got[0].Line, "parse error should carry a position: %+v", got[0])
	assert.NotEmpty(t, got[0].Msg)
}
