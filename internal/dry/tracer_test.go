package dry

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/dry/gen/mocks"
)

// TestEval_tracerSpellOp is the load-bearing proof for the spell docs' Run button:
// a canonical spell example that wires a fork op into a target must, under WithTracer,
// produce the op's dry-run trace rather than executing (or failing on) anything. It
// mirrors the shape of every authored spells/examples/**/*.buzz.
func TestEval_tracerSpellOp(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](); }
`
	r := Eval(context.Background(), src, WithTracer())
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1, "the go-build op should trace exactly one host op")
	assert.Equal(t, "build", r.Trace[0].Target, "op attributes to the target whose body invoked it")
	assert.Equal(t, "spell", r.Trace[0].Kind)
	assert.Equal(t, "go-build", r.Trace[0].Name)
}

// TestEval_tracerMultiTarget checks the trace flattens every target's ops in
// discovery (sorted-key) order, so a multi-op example reads top to bottom.
func TestEval_tracerMultiTarget(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun build(ctx: magus\Context, args: [str]) > void { go["go-build"](); }
export fun test(ctx: magus\Context, args: [str]) > void { go["go-test"](); }
`
	r := Eval(context.Background(), src, WithTracer())
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 2)
	// Targets are probed in sorted key order: build before test.
	assert.Equal(t, "go-build", r.Trace[0].Name)
	assert.Equal(t, "go-test", r.Trace[1].Name)
}

// TestEval_withSpells exercises registration of a non-built-in spell: WithSpells
// adds it to the tracing host, so its example traces just like a built-in's. Without
// the option the same import resolves to an inert stub and traces nothing.
func TestEval_withSpells(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/acme";

magus.project({ "spells": [acme] });

export fun deploy(ctx: magus\Context, args: [str]) > void { acme["acme-ship"](); }
`
	// Not a built-in: without WithSpells the op call traces nothing.
	bare := Eval(context.Background(), src, WithTracer())
	require.True(t, bare.OK, "eval failed: %+v", bare.Diag)
	assert.Empty(t, bare.Trace, "an unregistered spell should trace no ops")

	// Registered via WithSpells: the op traces.
	r := Eval(context.Background(), src, WithSpells(map[string][]string{"acme": {"acme-ship"}}))
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1, "the registered acme-ship op should trace one host op")
	assert.Equal(t, "acme-ship", r.Trace[0].Name)
}

// TestEval_withCatalog proves the SpellCatalog seam: the built-in surface the tracer
// stubs comes from the injected catalog, not a hard-coded manifest. A mock catalog with
// one fake built-in makes that spell's op trace like a real built-in's. This is the
// mock-driven replacement for the old hand-written manifest + drift-gate test.
func TestEval_withCatalog(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/acme";

magus.project({ "spells": [acme] });

export fun deploy(ctx: magus\Context, args: [str]) > void { acme["acme-ship"](); }
`
	cat := mocks.NewMockSpellCatalog(t)
	cat.EXPECT().BuiltinOps().Return(map[string][]string{"acme": {"acme-ship"}})

	r := Eval(context.Background(), src, WithCatalog(cat))
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1, "the acme-ship op from the injected catalog should trace")
	assert.Equal(t, "acme-ship", r.Trace[0].Name)
}

// TestEval_tracerParseError surfaces a compile failure as a Diag instead of a
// bogus empty trace, so a broken example shows the error rather than passing.
func TestEval_tracerParseError(t *testing.T) {
	r := Eval(context.Background(), "export fun build(ctx: magus\\Context, args: [str]) > void { this is not buzz }", WithTracer())
	assert.False(t, r.OK)
	assert.NotNil(t, r.Diag)
}
