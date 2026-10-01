package vm_test //nolint:testlayout // in-package would close a cycle: gopherbuzz imports gopherbuzz/vm

import (
	"context"
	"testing"

	"github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMarshalRoundTrip(t *testing.T) {
	prog, err := buzz.ParseEmbedded(`var x: int = 42;`)
	require.NoError(t, err, "Parse")
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	require.NoError(t, err, "CompileWith")

	data, err := chunk.Marshal()
	require.NoError(t, err, "Marshal")
	require.NotEmpty(t, data, "Marshal produced empty output")

	chunk2, err := vm.UnmarshalChunk(data)
	require.NoError(t, err, "UnmarshalChunk")
	require.NotNil(t, chunk2, "UnmarshalChunk returned nil")
	assert.NotEmpty(t, chunk2.Code, "unmarshalled chunk has no instructions")
}

func TestUnmarshalChunk_InvalidData(t *testing.T) {
	_, err := vm.UnmarshalChunk([]byte("not bytecode"))
	assert.Error(t, err, "UnmarshalChunk(garbage): expected error, got nil")
}

// marshalFixtures holds programs chosen to cover the encoder's constant kinds and
// node shapes (see enc.constVal and enc.node): null/bool/int/float/str constants,
// enum definitions, object declarations, pattern literals, functions (a nested
// chunk each), and the control-flow node varieties.
var marshalFixtures = []struct {
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
// fixture program, (1) the original chunk and the decoded chunk produce the same
// result, and (2) re-marshaling the decoded chunk reproduces the original bytes
// exactly. The byte-stability half is the whole-struct assertion for a Chunk:
// two chunks that encode identically are identical in every serialized field,
// with none of the pointer-identity noise a require.Equal on the structs would
// trip over.
func TestMarshalRoundTripSemantics(t *testing.T) {
	for _, c := range marshalFixtures {
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
	chunk := compileChunk(t, marshalFixtures[2].src) // function constants: nested funs exercise the tree walk
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
// the decoder. Each must return an error: never panic, never a (chunk, nil)
// success from partial data. The fuzz target explores mutated bytes; this sweep
// is the deterministic, always-on floor for the most common corruption
// (truncation), and it runs every length rather than sampled ones.
func TestUnmarshalChunkTruncated(t *testing.T) {
	data, err := compileChunk(t, marshalFixtures[0].src).Marshal()
	require.NoError(t, err, "Marshal")
	for n := 0; n < len(data); n++ {
		c, err := vm.UnmarshalChunk(data[:n])
		require.Errorf(t, err, "truncation at %d/%d bytes decoded without error", n, len(data))
		require.Nilf(t, c, "truncation at %d returned a chunk alongside the error", n)
	}
}

// validChunkBytes compiles a tiny program and marshals it, yielding a known-good
// .bo blob to seed the fuzz inputs with (and to exercise the round-trip path).
func validChunkBytes(t *testing.T) []byte {
	t.Helper()
	prog, err := buzz.ParseEmbedded(`var x: int = 42;`)
	require.NoError(t, err, "ParseEmbedded")
	chunk, err := buzz.CompileWith(prog, buzz.CompileOptions{})
	require.NoError(t, err, "CompileWith")
	data, err := chunk.Marshal()
	require.NoError(t, err, "Marshal")
	require.NotEmpty(t, data, "Marshal produced empty output")
	return data
}

// FuzzUnmarshalChunk fuzzes the bytecode chunk decoder, which deserializes
// untrusted bytes (a persisted/loaded .bo blob). The SAFETY INVARIANT under test:
// UnmarshalChunk must never panic and must never return a (chunk, err) pair that
// breaks the contract — on any malformed input it returns (nil, non-nil error),
// and on success it returns (non-nil chunk, nil error). It must NEVER return
// (nil, nil) (silent failure) nor (non-nil, nil-err) on a partially-decoded blob.
// Malformed offsets, truncated constant pools, and bad lengths must all surface a
// clean error rather than a panic or out-of-bounds access.
func FuzzUnmarshalChunk(f *testing.F) {
	valid := validChunkBytes(&testing.T{})

	// A valid blob plus a spread of malformed inputs: empty, truncated at several
	// boundaries, bad magic, good magic but truncated body, and pure garbage.
	f.Add(valid)
	f.Add([]byte(nil))
	f.Add([]byte{})
	f.Add([]byte("not bytecode"))
	f.Add([]byte("BZBC"))                                   // magic only, no version
	f.Add([]byte{'B', 'Z', 'B', 'C', 0x08, 0x00})           // magic + version, no body
	f.Add([]byte{'B', 'Z', 'B', 'C', 0xff, 0xff})           // magic + wrong version
	f.Add([]byte{'B', 'Z', 'D', 'B', 0x08, 0x00})           // .bdb magic, wrong for chunk
	f.Add([]byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06}) // garbage
	if len(valid) > 8 {
		f.Add(valid[:len(valid)/2]) // valid header, truncated mid-body
		f.Add(valid[:8])            // header + a few body bytes only
	}
	// A blob with a deliberately huge length field after a valid header would, in
	// a naive decoder, drive a giant make([]T) or OOB read; checkCount must reject
	// it. Splice a 0xFFFFFFFF count where the first length-prefixed field starts.
	if len(valid) >= 14 {
		bad := make([]byte, len(valid))
		copy(bad, valid)
		// bytes 6..10 are the first u32 (Name length) right after the 6-byte header.
		bad[6], bad[7], bad[8], bad[9] = 0xff, 0xff, 0xff, 0xff
		f.Add(bad)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		// The decoder must not panic on any input. require.NotPanics captures the
		// panic value and fails the seed/fuzz case cleanly so the offending input
		// is recorded by the fuzzing engine instead of crashing the run.
		var chunk *vm.Chunk
		var err error
		require.NotPanics(t, func() {
			chunk, err = vm.UnmarshalChunk(data)
		}, "UnmarshalChunk panicked on input %#v", data)

		// Contract: exactly one of (chunk, err) is the "populated" side.
		if err != nil {
			// On failure the chunk must be nil — never a partially-built tree.
			assert.Nil(t, chunk, "UnmarshalChunk returned (non-nil chunk, error); want (nil, error)")
			return
		}
		// On success the chunk must be non-nil — never (nil, nil) silent success.
		require.NotNil(t, chunk, "UnmarshalChunk returned (nil, nil); want (chunk, nil) or (nil, error)")

		// Stability: a chunk the decoder accepted must re-marshal, and that blob
		// must itself round-trip back to an accepted chunk. This guards against an
		// accepted-but-corrupt chunk that the encoder then chokes on or that decodes
		// to something different.
		out, merr := chunk.Marshal()
		require.NoError(t, merr, "Marshal of accepted chunk failed")
		require.NotEmpty(t, out, "Marshal of accepted chunk produced empty output")

		chunk2, err2 := vm.UnmarshalChunk(out)
		require.NoError(t, err2, "re-Unmarshal of re-Marshalled chunk failed (Marshal/Unmarshal not stable)")
		require.NotNil(t, chunk2, "re-Unmarshal returned nil chunk")

		out2, merr2 := chunk2.Marshal()
		require.NoError(t, merr2, "second Marshal failed")
		assert.Equal(t, out, out2, "Marshal(Unmarshal(Marshal(x))) is not byte-stable")
	})
}
