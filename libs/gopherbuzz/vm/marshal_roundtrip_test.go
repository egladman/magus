package vm_test

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/require"
)

// marshalCorpus holds programs chosen to cover the encoder's constant kinds and
// node shapes (see enc.constVal and enc.node): null/bool/int/float/str constants,
// enum definitions, object declarations, pattern literals, functions (a nested
// chunk each), and the control-flow node varieties.
var marshalCorpus = []struct {
	name string
	src  string
	want string // canonical rendering of the program's result
}{
	{"scalar constants", `final a = 42; final b = 2.5; final c = "s"; final d = true; final e = null;
		return [a, b, c, d, e];`, "[42, 2.5, s, true, null]"},
	{"negative and large ints", `return [0 - 42, 1 << 46];`, "[-42, 70368744177664]"},
	{"function constants and calls", `fun add(a: int, b: int) > int { return a + b; }
		fun twice(f: fun (int, int) > int, x: int) > int { return f(x, x); }
		return twice(add, 21);`, "42"},
	{"closures capture", `fun mk(n: int) > fun () > int { return fun () > int { return n * 2; }; }
		return mk(21)();`, "42"},
	{"control flow", `var s = 0; var i = 0;
		while (i < 10) { if (i % 2 == 0) { s = s + i; } else { s = s - 1; } i = i + 1; }
		foreach (x in 0..3) { s = s + x; }
		return s;`, "18"},
	{"enum definition and access", `enum Color { red, green, blue }
		return [Color.green.value, Color.blue.name];`, "[1, blue]"},
	{"object declaration", `object Point { x: int, y: int,
		fun sum(this: Point) > int { return this.x + this.y; } }
		final p = Point{ x = 20, y = 22 };
		return p.sum();`, "42"},
	{"string escapes survive", `return "a\nb\tc\"d";`, "a\nb\tc\"d"},
	{"list and map building", `var m = mut {"k": [1, 2]}; m["k2"] = [3]; return [m.len(), m["k"][1]];`, "[2, 2]"},
}

// compileChunk compiles src to a chunk, failing the test on any error.
func compileChunk(t *testing.T, src string) *vm.Chunk {
	t.Helper()
	prog, err := buzz.ParseEmbedded(src)
	require.NoError(t, err, "parse")
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	require.NoError(t, err, "compile")
	return chunk
}

// runChunk executes a chunk in a fresh env and returns the result's rendering.
func runChunk(t *testing.T, c *vm.Chunk) string {
	t.Helper()
	env := vm.NewEnv()
	vm.RegisterStdlib(env)
	v, err := vm.NewVM(context.Background()).Run(c, env)
	require.NoError(t, err, "run")
	return v.String()
}

// TestMarshalRoundTripSemantics is the marshal contract that matters: for every
// corpus program, (1) the original chunk and the decoded chunk produce the same
// result, and (2) re-marshaling the decoded chunk reproduces the original bytes
// exactly. The byte-stability half is the whole-struct assertion for a Chunk:
// two chunks that encode identically are identical in every serialized field,
// with none of the pointer-identity noise a require.Equal on the structs would
// trip over.
func TestMarshalRoundTripSemantics(t *testing.T) {
	for _, c := range marshalCorpus {
		t.Run(c.name, func(t *testing.T) {
			chunk := compileChunk(t, c.src)
			require.Equal(t, c.want, runChunk(t, chunk), "original chunk result")

			data, err := chunk.Marshal()
			require.NoError(t, err, "Marshal")

			decoded, err := vm.UnmarshalChunk(data)
			require.NoError(t, err, "UnmarshalChunk")
			require.Equal(t, c.want, runChunk(t, decoded), "decoded chunk result")

			again, err := decoded.Marshal()
			require.NoError(t, err, "re-Marshal")
			require.Equal(t, data, again, "marshal is not byte-stable across a round trip")
		})
	}
}

// TestMarshalDebugRoundTrip pins the .bo/.bdb pair: DebugOnly yields a separate
// blob, AttachDebug folds it back onto a freshly decoded chunk, and a debug blob
// from a DIFFERENT chunk is rejected rather than silently mismatching lines.
func TestMarshalDebugRoundTrip(t *testing.T) {
	chunk := compileChunk(t, marshalCorpus[2].src) // function constants: nested funs exercise the tree walk
	code, err := chunk.Marshal()
	require.NoError(t, err, "Marshal code")
	debug, err := chunk.Marshal(vm.DebugOnly())
	require.NoError(t, err, "Marshal debug")
	require.NotEqual(t, code, debug, "code and debug blobs must differ")

	decoded, err := vm.UnmarshalChunk(code)
	require.NoError(t, err, "UnmarshalChunk")
	require.NoError(t, decoded.AttachDebug(debug), "AttachDebug on the matching chunk")

	t.Run("mismatched shape rejected", func(t *testing.T) {
		other := compileChunk(t, `return 1;`)
		otherDebug, err := other.Marshal(vm.DebugOnly())
		require.NoError(t, err, "Marshal other debug")
		require.Error(t, decoded.AttachDebug(otherDebug),
			"a .bdb from a different chunk must be rejected, not silently attached")
	})
	t.Run("code blob rejected as debug", func(t *testing.T) {
		require.Error(t, decoded.AttachDebug(code), "a .bo blob is not a .bdb")
	})
}

// TestExecBytecode covers the one-shot decode-and-run entry point, including its
// error propagation from both halves (a bad blob, then a runtime error).
func TestExecBytecode(t *testing.T) {
	env := vm.NewEnv()
	vm.RegisterStdlib(env)

	data, err := compileChunk(t, `final x = 21; return x * 2;`).Marshal()
	require.NoError(t, err, "Marshal")
	require.NoError(t, vm.ExecBytecode(context.Background(), data, env), "run")

	t.Run("bad blob", func(t *testing.T) {
		require.Error(t, vm.ExecBytecode(context.Background(), []byte("junk"), env))
	})
	t.Run("runtime error propagates", func(t *testing.T) {
		data, err := compileChunk(t, `final d = 0; return 1 / d;`).Marshal()
		require.NoError(t, err, "Marshal")
		require.ErrorContains(t, vm.ExecBytecode(context.Background(), data, env), "division by zero")
	})
}

// TestUnmarshalChunkTruncated sweeps every proper prefix of a valid blob through
// the decoder. Each must return an error - never panic, never a (chunk, nil)
// success from partial data. The fuzz target explores mutated bytes; this sweep
// is the deterministic, always-on floor for the most common corruption
// (truncation), and it runs every length rather than sampled ones.
func TestUnmarshalChunkTruncated(t *testing.T) {
	data, err := compileChunk(t, marshalCorpus[0].src).Marshal()
	require.NoError(t, err, "Marshal")
	for n := 0; n < len(data); n++ {
		c, err := vm.UnmarshalChunk(data[:n])
		require.Errorf(t, err, "truncation at %d/%d bytes decoded without error", n, len(data))
		require.Nilf(t, c, "truncation at %d returned a chunk alongside the error", n)
	}
}
