//go:build goexperiment.jsonv2

package codec

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"io"
)

var marshalOpts = jsonv2.JoinOptions(
	jsonv2.Deterministic(true),
	jsonv2.FormatNilSliceAsNull(true),
	jsonv2.FormatNilMapAsNull(true),
)

func Marshal(v any) ([]byte, error) { return jsonv2.Marshal(v, marshalOpts) }

func Unmarshal(data []byte, v any) error { return jsonv2.Unmarshal(data, v) }

func Valid(data []byte) bool { return jsontext.Value(data).IsValid() }

func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	data, err := Marshal(v)
	if err != nil {
		return nil, err
	}
	val := jsontext.Value(data)
	var opts []jsontext.Options
	if indent != "" {
		opts = append(opts, jsontext.WithIndent(indent))
	}
	if prefix != "" {
		opts = append(opts, jsontext.WithIndentPrefix(prefix))
	}
	if err := val.Indent(opts...); err != nil {
		return nil, err
	}
	return []byte(val), nil
}

type encoder struct{ enc *jsontext.Encoder }

func NewEncoder(w io.Writer) *encoder { return &encoder{jsontext.NewEncoder(w)} }

func (e *encoder) Encode(v any) error {
	return jsonv2.MarshalEncode(e.enc, v, marshalOpts)
}

type decoder struct{ dec *jsontext.Decoder }

func NewDecoder(r io.Reader) *decoder { return &decoder{jsontext.NewDecoder(r)} }

func (d *decoder) Decode(v any) error { return jsonv2.UnmarshalDecode(d.dec, v) }
