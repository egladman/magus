package codec

import (
	"bytes"
	"io"
	"testing"

	"github.com/ulikunitz/xz"
)

// Payload sizes chosen to be representative of real-world usage:
//   - 1 MiB   ≈ small archive file (zstd benchmarks)
//   - 512 KiB ≈ moderate xz payload (xz benchmarks)

// BenchmarkZstdCompress measures streaming compression throughput.
// Run CGO_ENABLED=0 first to capture the pure-Go baseline, then
// CGO_ENABLED=1 for the libzstd path.
//
//	go test -bench=BenchmarkZstdCompress -benchmem -count=10 ./internal/codec/ > old.txt  # CGO=0
//	go test -bench=BenchmarkZstdCompress -benchmem -count=10 ./internal/codec/ > new.txt  # CGO=1
//	benchstat old.txt new.txt
func BenchmarkZstdCompress(b *testing.B) {
	for _, size := range []int{256 * 1024, 1 << 20, 4 << 20} {
		payload := testPayload(size)
		b.Run(bytesLabel(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				var buf bytes.Buffer
				w, err := NewZstdWriter(&buf, 3, 0)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := w.Write(payload); err != nil {
					b.Fatal(err)
				}
				if err := w.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkZstdDecompress measures streaming decompression throughput.
func BenchmarkZstdDecompress(b *testing.B) {
	for _, size := range []int{256 * 1024, 1 << 20, 4 << 20} {
		payload := testPayload(size)

		// Pre-compress once with the active codec so both paths decompress
		// their own format (guaranteed to parse correctly).
		var compressed bytes.Buffer
		w, err := NewZstdWriter(&compressed, 3, 0)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := w.Write(payload); err != nil {
			b.Fatal(err)
		}
		if err := w.Close(); err != nil {
			b.Fatal(err)
		}
		compressedBytes := compressed.Bytes()

		b.Run(bytesLabel(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				r, err := NewZstdReader(bytes.NewReader(compressedBytes), 0)
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, r); err != nil {
					b.Fatal(err)
				}
				r.Close()
			}
		})
	}
}

// BenchmarkXzDecompress measures streaming xz decompression throughput.
// Compression is done by the ulikunitz/xz reference encoder (since xz
// compress is not in the current archive scope).
func BenchmarkXzDecompress(b *testing.B) {
	for _, size := range []int{256 * 1024, 512 * 1024} {
		payload := testPayload(size)

		// Compress once with ulikunitz reference.
		var compressed bytes.Buffer
		xw, err := xz.NewWriter(&compressed)
		if err != nil {
			b.Fatal(err)
		}
		if _, err := xw.Write(payload); err != nil {
			b.Fatal(err)
		}
		if err := xw.Close(); err != nil {
			b.Fatal(err)
		}
		compressedBytes := compressed.Bytes()

		b.Run(bytesLabel(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				r, err := NewXzReader(bytes.NewReader(compressedBytes))
				if err != nil {
					b.Fatal(err)
				}
				if _, err := io.Copy(io.Discard, r); err != nil {
					b.Fatal(err)
				}
				r.Close()
			}
		})
	}
}

// bytesLabel returns a human-readable label for a byte count (KiB / MiB).
func bytesLabel(n int) string {
	switch {
	case n >= 1<<20:
		return formatInt(n>>20) + "MiB"
	default:
		return formatInt(n>>10) + "KiB"
	}
}

func formatInt(n int) string {
	// Simple int-to-string without fmt to avoid import.
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 20)
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
