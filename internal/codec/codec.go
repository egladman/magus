// Package codec provides the serialization and compression primitives magus
// uses for cache manifests and report streams: pluggable streaming JSON
// encoders/decoders and zstd/xz compressors. The compressors have cgo fast
// paths (libzstd, liblzma) selected by build tag, with pure-Go fallbacks so
// the module builds and runs without a C toolchain.
package codec

import "io"

// Encoder is the common interface for streaming JSON encoders.
type Encoder interface {
	Encode(v any) error
}

// Decoder is the common interface for streaming JSON decoders.
type Decoder interface {
	Decode(v any) error
}

// codec.go owns the exported surface of the streaming compression codecs. Each
// codec has two implementations selected at build time: a cgo path backed by
// libzstd/liblzma (zstd_cgo.go, xz_cgo.go) and a pure-Go fallback
// (zstd_other.go, xz_other.go). The wrappers below delegate to whichever
// implementation the build selected, so callers get one documented API
// regardless of whether cgo is enabled.

// NewZstdWriter returns a streaming zstd compressor writing to w. level is the
// compression level (-1 = default, 1-19); threads sets encoder concurrency
// (0 = single-threaded).
func NewZstdWriter(w io.Writer, level, threads int) (io.WriteCloser, error) {
	return newZstdWriter(w, level, threads)
}

// NewZstdReader returns a streaming zstd decompressor reading from r. threads
// sets decoder concurrency (0 = single-threaded).
func NewZstdReader(r io.Reader, threads int) (io.ReadCloser, error) {
	return newZstdReader(r, threads)
}

// NewXzReader returns a streaming xz decompressor reading from r.
func NewXzReader(r io.Reader) (io.ReadCloser, error) {
	return newXzReader(r)
}
