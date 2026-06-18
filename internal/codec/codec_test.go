package codec

import (
	"bytes"
	"io"
	"strings"
	"testing"

	kzstd "github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/ulikunitz/xz"
)

// testPayload generates a compressible payload of n bytes (repeating text).
func testPayload(n int) []byte {
	base := []byte("the quick brown fox jumps over the lazy dog\n")
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = base[i%len(base)]
	}
	return out
}

func TestZstdRoundTrip(t *testing.T) {
	want := testPayload(1 << 20) // 1 MiB

	var buf bytes.Buffer
	w, err := NewZstdWriter(&buf, -1, 0)
	require.NoError(t, err, "NewZstdWriter")
	_, err = w.Write(want)
	require.NoError(t, err, "Write")
	require.NoError(t, w.Close(), "Close")

	r, err := NewZstdReader(bytes.NewReader(buf.Bytes()), 0)
	require.NoError(t, err, "NewZstdReader")
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err, "ReadAll")
	assert.Equal(t, want, got, "round-trip mismatch")
}

// TestZstdCrossCompatWrite verifies that the codec writer produces a stream
// readable by the klauspost pure-Go decoder (format compatibility).
func TestZstdCrossCompatWrite(t *testing.T) {
	want := testPayload(512 * 1024)

	var buf bytes.Buffer
	w, err := NewZstdWriter(&buf, 3, 0)
	require.NoError(t, err, "NewZstdWriter")
	_, err = w.Write(want)
	require.NoError(t, err, "Write")
	require.NoError(t, w.Close(), "Close")

	// Decode with klauspost reference decoder.
	ref, err := kzstd.NewReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err, "kzstd.NewReader")
	defer ref.Close()

	got, err := io.ReadAll(ref)
	require.NoError(t, err, "kzstd ReadAll")
	assert.Equal(t, want, got, "cross-compat write")
}

// TestZstdCrossCompatRead verifies that streams written by the klauspost
// encoder are readable by the codec reader.
func TestZstdCrossCompatRead(t *testing.T) {
	want := testPayload(512 * 1024)

	// Compress with klauspost reference encoder.
	var buf bytes.Buffer
	enc, err := kzstd.NewWriter(&buf)
	require.NoError(t, err, "kzstd.NewWriter")
	_, err = enc.Write(want)
	require.NoError(t, err, "kzstd Write")
	require.NoError(t, enc.Close(), "kzstd Close")

	r, err := NewZstdReader(bytes.NewReader(buf.Bytes()), 0)
	require.NoError(t, err, "NewZstdReader")
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err, "ReadAll")
	assert.Equal(t, want, got, "cross-compat read")
}

func TestZstdMultithreaded(t *testing.T) {
	want := testPayload(4 * 1 << 20) // 4 MiB; exercises multi-worker path

	var buf bytes.Buffer
	w, err := NewZstdWriter(&buf, 3, 4)
	require.NoError(t, err, "NewZstdWriter")
	_, err = w.Write(want)
	require.NoError(t, err, "Write")
	require.NoError(t, w.Close(), "Close")

	r, err := NewZstdReader(bytes.NewReader(buf.Bytes()), 0)
	require.NoError(t, err, "NewZstdReader")
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err, "ReadAll")
	assert.Equal(t, want, got, "multithreaded round-trip mismatch")
}

func TestXzDecompress(t *testing.T) {
	want := testPayload(512 * 1024)

	// Compress with ulikunitz/xz reference encoder.
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	require.NoError(t, err, "xz.NewWriter")
	_, err = xw.Write(want)
	require.NoError(t, err, "xz Write")
	require.NoError(t, xw.Close(), "xz Close")

	// Decompress with the codec.
	r, err := NewXzReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err, "NewXzReader")
	defer r.Close()

	got, err := io.ReadAll(r)
	require.NoError(t, err, "xz ReadAll")
	assert.Equal(t, want, got, "xz decompress mismatch")
}

// TestXzCrossCompatRead verifies the codec reader output against the ulikunitz
// reference output on the same compressed stream.
func TestXzCrossCompatRead(t *testing.T) {
	want := testPayload(512 * 1024)

	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	require.NoError(t, err, "xz.NewWriter")
	_, err = xw.Write(want)
	require.NoError(t, err, "xz Write")
	require.NoError(t, xw.Close(), "xz Close")
	compressed := buf.Bytes()

	// Decode with codec.
	r, err := NewXzReader(bytes.NewReader(compressed))
	require.NoError(t, err, "NewXzReader")
	codecGot, err := io.ReadAll(r)
	r.Close()
	require.NoError(t, err, "codec xz ReadAll")

	// Decode with ulikunitz reference.
	ref, err := xz.NewReader(bytes.NewReader(compressed))
	require.NoError(t, err, "xz.NewReader")
	refGot, err := io.ReadAll(ref)
	require.NoError(t, err, "ref xz ReadAll")

	assert.Equal(t, refGot, codecGot, "xz cross-compat: codec and reference output differ")
}

func TestXzSmallChunks(t *testing.T) {
	want := testPayload(64 * 1024)

	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	require.NoError(t, err, "xz.NewWriter")
	_, err = xw.Write(want)
	require.NoError(t, err, "xz Write")
	require.NoError(t, xw.Close(), "xz Close")

	// Read back 1 byte at a time to exercise the buffering logic.
	r, err := NewXzReader(bytes.NewReader(buf.Bytes()))
	require.NoError(t, err, "NewXzReader")
	defer r.Close()

	var got []byte
	oneByte := make([]byte, 1)
	for {
		n, err := r.Read(oneByte)
		if n > 0 {
			got = append(got, oneByte[:n]...)
		}
		if err == io.EOF {
			break
		}
		require.NoError(t, err, "Read")
	}
	assert.Equal(t, want, got, "xz small-chunk mismatch")
}

// TestZstdSmallChunks exercises the streaming reader when Read is called with
// a 1-byte buffer — validates that internal buffering is correct.
func TestZstdSmallChunks(t *testing.T) {
	want := []byte(strings.Repeat("hello world\n", 1000))

	var buf bytes.Buffer
	w, err := NewZstdWriter(&buf, -1, 0)
	require.NoError(t, err, "NewZstdWriter")
	_, err = w.Write(want)
	require.NoError(t, err, "Write")
	require.NoError(t, w.Close(), "Close")

	r, err := NewZstdReader(bytes.NewReader(buf.Bytes()), 0)
	require.NoError(t, err, "NewZstdReader")
	defer r.Close()

	var got []byte
	oneByte := make([]byte, 1)
	for {
		n, rErr := r.Read(oneByte)
		if n > 0 {
			got = append(got, oneByte[:n]...)
		}
		if rErr == io.EOF {
			break
		}
		require.NoError(t, rErr, "Read")
	}
	assert.Equal(t, want, got, "zstd small-chunk mismatch")
}
