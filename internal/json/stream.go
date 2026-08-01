package json

// Encoder is the common interface implemented by the active streaming JSON
// encoder.
type Encoder interface {
	Encode(v any) error
}

// Decoder is the common interface implemented by the active streaming JSON
// decoder.
type Decoder interface {
	Decode(v any) error
}
