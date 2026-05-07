// Package codec provides the serialization and compression primitives magus
// uses for cache manifests and report streams: pluggable streaming JSON
// encoders/decoders and zstd/xz compressors. The compressors have cgo fast
// paths (libzstd, liblzma) selected by build tag, with pure-Go fallbacks so
// the module builds and runs without a C toolchain.
package codec

// Encoder is the common interface for streaming JSON encoders.
type Encoder interface {
	Encode(v any) error
}

// Decoder is the common interface for streaming JSON decoders.
type Decoder interface {
	Decode(v any) error
}
