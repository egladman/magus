//go:build !goexperiment.jsonv2

package codec

import (
	"encoding/json"
	"io"
)

func Marshal(v any) ([]byte, error) { return json.Marshal(v) }

func Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }

func Valid(data []byte) bool { return json.Valid(data) }

func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	return json.MarshalIndent(v, prefix, indent)
}

func NewEncoder(w io.Writer) *json.Encoder { return json.NewEncoder(w) }

func NewDecoder(r io.Reader) *json.Decoder { return json.NewDecoder(r) }
