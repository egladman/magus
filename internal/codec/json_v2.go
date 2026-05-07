//go:build goexperiment.jsonv2

package codec

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"io"
)

type RawMessage = jsontext.Value

func Marshal(v any) ([]byte, error)      { return jsonv2.Marshal(v) }
func Unmarshal(data []byte, v any) error { return jsonv2.Unmarshal(data, v) }
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	data, err := jsonv2.Marshal(v)
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

type v2encoder struct{ enc *jsontext.Encoder }

func NewEncoder(w io.Writer) Encoder    { return &v2encoder{enc: jsontext.NewEncoder(w)} }
func (e *v2encoder) Encode(v any) error { return jsonv2.MarshalEncode(e.enc, v) }

type v2decoder struct{ dec *jsontext.Decoder }

func NewDecoder(r io.Reader) Decoder    { return &v2decoder{dec: jsontext.NewDecoder(r)} }
func (d *v2decoder) Decode(v any) error { return jsonv2.UnmarshalDecode(d.dec, v) }
func CodecVersion() string              { return "v2" }
