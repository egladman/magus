package vm_test

import (
	"testing"

	"github.com/egladman/gopherbuzz"
	"github.com/egladman/gopherbuzz/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// validChunkBytes compiles a tiny program and marshals it, yielding a known-good
// .bo blob to seed the fuzz corpus with (and to exercise the round-trip path).
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
