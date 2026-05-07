//go:build !goexperiment.jsonv2

package codec

import (
	"encoding/json"
	"io"
)

// RawMessage is the codec's deferred-decode JSON type (alias of encoding/json.RawMessage).
type RawMessage = json.RawMessage

// Marshal encodes v as JSON.
func Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes JSON data into v.
func Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

// MarshalIndent encodes v as indented JSON using prefix and indent.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// NewEncoder returns a JSON encoder that writes to w.
func NewEncoder(w io.Writer) Encoder { return json.NewEncoder(w) }

// NewDecoder returns a JSON decoder that reads from r.
func NewDecoder(r io.Reader) Decoder { return json.NewDecoder(r) }

// CodecVersion reports the active JSON codec version ("v1").
func CodecVersion() string { return "v1" }
