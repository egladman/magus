package playground

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEvalBuzz_recorderSpellOp is the load-bearing proof for the spell docs' Run
// button: a canonical spell example that wires a fork op into a target must, under
// WithRecorder, produce the op's dry-run trace rather than executing (or failing
// on) anything. It mirrors the shape of every authored spells/examples/**/*.buzz.
func TestEvalBuzz_recorderSpellOp(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun build(args: [str]) > void { go["go-build"](); }
`
	r := EvalBuzz(context.Background(), src, WithRecorder())
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1, "the go-build op should record exactly one host op")
	assert.Equal(t, "build", r.Trace[0].Target, "op attributes to the target whose body invoked it")
	assert.Equal(t, "spell", r.Trace[0].Kind)
	assert.Equal(t, "go-build", r.Trace[0].Name)
}

// TestEvalBuzz_recorderMultiTarget checks the trace flattens every target's ops in
// discovery (sorted-key) order, so a multi-op example reads top to bottom.
func TestEvalBuzz_recorderMultiTarget(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/go";

magus.project({ "spells": [go] });

export fun build(args: [str]) > void { go["go-build"](); }
export fun test(args: [str]) > void { go["go-test"](); }
`
	r := EvalBuzz(context.Background(), src, WithRecorder())
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 2)
	// Targets are probed in sorted key order: build before test.
	assert.Equal(t, "go-build", r.Trace[0].Name)
	assert.Equal(t, "go-test", r.Trace[1].Name)
}

// TestEvalBuzz_withSpells exercises the first-class registration of a non-built-in
// spell: WithSpells adds it to the recording host, so its example records just like
// a built-in's. Without the option the same import would resolve to an inert stub
// and record nothing.
func TestEvalBuzz_withSpells(t *testing.T) {
	const src = `
import "magus";
import "magus/spell/acme";

magus.project({ "spells": [acme] });

export fun deploy(args: [str]) > void { acme["acme-ship"](); }
`
	// Not a built-in: without WithSpells the op call records nothing.
	bare := EvalBuzz(context.Background(), src, WithRecorder())
	require.True(t, bare.OK, "eval failed: %+v", bare.Diag)
	assert.Empty(t, bare.Trace, "an unregistered spell should record no ops")

	// Registered via WithSpells: the op records.
	r := EvalBuzz(context.Background(), src, WithSpells(map[string][]string{"acme": {"acme-ship"}}))
	require.True(t, r.OK, "eval failed: %+v", r.Diag)
	require.Len(t, r.Trace, 1, "the registered acme-ship op should record one host op")
	assert.Equal(t, "acme-ship", r.Trace[0].Name)
}

// TestEvalBuzz_recorderParseError surfaces a compile failure as a Diag instead of a
// bogus empty trace, so a broken example shows the error rather than passing.
func TestEvalBuzz_recorderParseError(t *testing.T) {
	r := EvalBuzz(context.Background(), "export fun build(args: [str]) > void { this is not buzz }", WithRecorder())
	assert.False(t, r.OK)
	assert.NotNil(t, r.Diag)
}
