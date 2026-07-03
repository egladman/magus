//go:build !cgo || wasm

package codec

// The `|| wasm` arm routes the wasm playground here even under cgo: native
// libzstd can't be linked into a wasm sandbox (see zstd_cgo.go).

import (
	"io"

	"github.com/klauspost/compress/zstd"
)

// newZstdWriter returns a streaming zstd compressor that writes to w.
// level is the user-specified compression level (-1 = default, 1-19).
// threads controls encoder concurrency (0 = single-threaded).
func newZstdWriter(w io.Writer, level, threads int) (io.WriteCloser, error) {
	return zstd.NewWriter(w, zstdPureGoLevel(level), zstd.WithEncoderConcurrency(threads))
}

// newZstdReader returns a streaming zstd decompressor reading from r.
// threads controls decoder concurrency (0 = single-threaded).
func newZstdReader(r io.Reader, threads int) (io.ReadCloser, error) {
	dec, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(threads))
	if err != nil {
		return nil, err
	}
	return zstdDecoderRC{dec}, nil
}

// zstdDecoderRC wraps *zstd.Decoder (whose Close returns nothing) to satisfy
// io.ReadCloser (Close returns error).
type zstdDecoderRC struct{ *zstd.Decoder }

func (d zstdDecoderRC) Close() error { d.Decoder.Close(); return nil }

// zstdPureGoLevel maps a user level to a klauspost EncoderLevel option.
func zstdPureGoLevel(level int) zstd.EOption {
	switch {
	case level <= 1:
		return zstd.WithEncoderLevel(zstd.SpeedFastest)
	case level <= 4:
		return zstd.WithEncoderLevel(zstd.SpeedDefault)
	case level <= 7:
		return zstd.WithEncoderLevel(zstd.SpeedBetterCompression)
	default:
		return zstd.WithEncoderLevel(zstd.SpeedBestCompression)
	}
}
