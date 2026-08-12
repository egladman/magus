// Package compress provides the streaming compression primitives Magus uses for
// cache artifacts and archives. The compressors have cgo fast paths (libzstd,
// liblzma) selected by build tag, with pure-Go fallbacks so the module builds
// and runs without a C toolchain.
//
// Not "codec": that name covers JSON and protobuf too, and here it already meant
// JSON until internal/codec/json.go became internal/json.
package compress

import "io"

// The wrappers below delegate to whichever implementation the build selected
// (zstd_cgo.go / zstd_other.go, xz_cgo.go / xz_other.go), so callers get one
// documented API whether or not cgo is enabled.

// NewZstdWriter returns a streaming zstd compressor writing to w. level is the
// compression level (-1 = default, 1-19); threads sets encoder concurrency
// (0 = single-threaded on the cgo build; GOMAXPROCS on the pure-Go fallback).
func NewZstdWriter(w io.Writer, level, threads int) (io.WriteCloser, error) {
	return newZstdWriter(w, level, threads)
}

// NewZstdReader returns a streaming zstd decompressor reading from r. threads
// sets decoder concurrency (0 = single-threaded on the cgo build; GOMAXPROCS
// on the pure-Go fallback).
func NewZstdReader(r io.Reader, threads int) (io.ReadCloser, error) {
	return newZstdReader(r, threads)
}

// NewXzReader returns a streaming xz decompressor reading from r.
func NewXzReader(r io.Reader) (io.ReadCloser, error) {
	return newXzReader(r)
}
