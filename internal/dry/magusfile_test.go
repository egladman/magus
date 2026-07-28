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
// Diagnostics - the checker accepts it, so the editor must not squiggle it.
func TestDiagnostics_ArrowBodyClean(t *testing.T) {
	got := Diagnostics(context.Background(), "export fun triple(x: int) > int => x * 3;")
	assert.Empty(t, got, "arrow-body function should lint clean, got %+v", got)
}

func TestDiagnostics_ParseError(t *testing.T) {
	got := Diagnostics(context.Background(), "export fun a(ctx: magus\\Context, args: [str]) > void { var x = ; }")
	require.Len(t, got, 1)
	assert.NotZero(t, got[0].Line, "parse error should carry a position: %+v", got[0])
	assert.NotEmpty(t, got[0].Msg)
}
