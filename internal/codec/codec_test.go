package codec

import (
	"bytes"
	"io"
	"strings"
	"testing"

	kzstd "github.com/klauspost/compress/zstd"
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

// --- zstd ---

func TestZstdRoundTrip(t *testing.T) {
	want := testPayload(1 << 20) // 1 MiB

	var buf bytes.Buffer
	w, err := NewZstdWriter(&buf, -1, 0)
	if err != nil {
		t.Fatalf("NewZstdWriter: %v", err)
	}
	if _, err := w.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewZstdReader(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("NewZstdReader: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

// TestZstdCrossCompatWrite verifies that the codec writer produces a stream
// readable by the klauspost pure-Go decoder (format compatibility).
func TestZstdCrossCompatWrite(t *testing.T) {
	want := testPayload(512 * 1024)

	var buf bytes.Buffer
	w, err := NewZstdWriter(&buf, 3, 0)
	if err != nil {
		t.Fatalf("NewZstdWriter: %v", err)
	}
	if _, err := w.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Decode with klauspost reference decoder.
	ref, err := kzstd.NewReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("kzstd.NewReader: %v", err)
	}
	defer ref.Close()

	got, err := io.ReadAll(ref)
	if err != nil {
		t.Fatalf("kzstd ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("cross-compat write: got %d bytes, want %d bytes", len(got), len(want))
	}
}

// TestZstdCrossCompatRead verifies that streams written by the klauspost
// encoder are readable by the codec reader.
func TestZstdCrossCompatRead(t *testing.T) {
	want := testPayload(512 * 1024)

	// Compress with klauspost reference encoder.
	var buf bytes.Buffer
	enc, err := kzstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("kzstd.NewWriter: %v", err)
	}
	if _, err := enc.Write(want); err != nil {
		t.Fatalf("kzstd Write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("kzstd Close: %v", err)
	}

	r, err := NewZstdReader(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("NewZstdReader: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("cross-compat read: got %d bytes, want %d bytes", len(got), len(want))
	}
}

func TestZstdMultithreaded(t *testing.T) {
	want := testPayload(4 * 1 << 20) // 4 MiB; exercises multi-worker path

	var buf bytes.Buffer
	w, err := NewZstdWriter(&buf, 3, 4)
	if err != nil {
		t.Fatalf("NewZstdWriter: %v", err)
	}
	if _, err := w.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewZstdReader(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("NewZstdReader: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("multithreaded round-trip mismatch")
	}
}

// --- xz ---

func TestXzDecompress(t *testing.T) {
	want := testPayload(512 * 1024)

	// Compress with ulikunitz/xz reference encoder.
	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("xz.NewWriter: %v", err)
	}
	if _, err := xw.Write(want); err != nil {
		t.Fatalf("xz Write: %v", err)
	}
	if err := xw.Close(); err != nil {
		t.Fatalf("xz Close: %v", err)
	}

	// Decompress with the codec.
	r, err := NewXzReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewXzReader: %v", err)
	}
	defer r.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("xz ReadAll: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("xz decompress mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}
}

// TestXzCrossCompatRead verifies the codec reader output against the ulikunitz
// reference output on the same compressed stream.
func TestXzCrossCompatRead(t *testing.T) {
	want := testPayload(512 * 1024)

	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("xz.NewWriter: %v", err)
	}
	if _, err := xw.Write(want); err != nil {
		t.Fatalf("xz Write: %v", err)
	}
	if err := xw.Close(); err != nil {
		t.Fatalf("xz Close: %v", err)
	}
	compressed := buf.Bytes()

	// Decode with codec.
	r, err := NewXzReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("NewXzReader: %v", err)
	}
	codecGot, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatalf("codec xz ReadAll: %v", err)
	}

	// Decode with ulikunitz reference.
	ref, err := xz.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("xz.NewReader: %v", err)
	}
	refGot, err := io.ReadAll(ref)
	if err != nil {
		t.Fatalf("ref xz ReadAll: %v", err)
	}

	if !bytes.Equal(codecGot, refGot) {
		t.Fatalf("xz cross-compat: codec and reference output differ")
	}
}

func TestXzSmallChunks(t *testing.T) {
	want := testPayload(64 * 1024)

	var buf bytes.Buffer
	xw, err := xz.NewWriter(&buf)
	if err != nil {
		t.Fatalf("xz.NewWriter: %v", err)
	}
	if _, err := xw.Write(want); err != nil {
		t.Fatalf("xz Write: %v", err)
	}
	if err := xw.Close(); err != nil {
		t.Fatalf("xz Close: %v", err)
	}

	// Read back 1 byte at a time to exercise the buffering logic.
	r, err := NewXzReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("NewXzReader: %v", err)
	}
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
		if err != nil {
			t.Fatalf("Read: %v", err)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("xz small-chunk mismatch: got %d bytes, want %d", len(got), len(want))
	}
}

// TestZstdSmallChunks exercises the streaming reader when Read is called with
// a 1-byte buffer — validates that internal buffering is correct.
func TestZstdSmallChunks(t *testing.T) {
	want := []byte(strings.Repeat("hello world\n", 1000))

	var buf bytes.Buffer
	w, err := NewZstdWriter(&buf, -1, 0)
	if err != nil {
		t.Fatalf("NewZstdWriter: %v", err)
	}
	if _, err := w.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := NewZstdReader(bytes.NewReader(buf.Bytes()), 0)
	if err != nil {
		t.Fatalf("NewZstdReader: %v", err)
	}
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
		if rErr != nil {
			t.Fatalf("Read: %v", rErr)
		}
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("zstd small-chunk mismatch")
	}
}
