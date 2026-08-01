//go:build !goexperiment.jsonv2

// Package json provides Magus's consistent JSON encoding surface. It selects
// encoding/json/v2 when GOEXPERIMENT=jsonv2 is enabled, while preserving the
// v1-compatible streaming API and duration representation used by Magus.
package json

import (
	"encoding/json"
	"io"
)

// RawMessage is the deferred-decode JSON type (an alias of encoding/json.RawMessage).
type RawMessage = json.RawMessage

// Marshal encodes v as JSON.
func Marshal(v any) ([]byte, error) { return json.Marshal(v) }

// Unmarshal decodes JSON data into v.
func Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func Valid(data []byte) bool { return json.Valid(data) }

// MarshalIndent encodes v as indented JSON using prefix and indent.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

// NewEncoder returns a JSON encoder that writes to w.
func NewEncoder(w io.Writer) Encoder { return json.NewEncoder(w) }

// NewDecoder returns a JSON decoder that reads from r.
func NewDecoder(r io.Reader) Decoder { return json.NewDecoder(r) }

// Version reports the active JSON implementation ("v1").
func Version() string { return "v1" }
