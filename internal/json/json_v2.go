//go:build goexperiment.jsonv2

package json

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"io"
	"time"
)

type RawMessage = jsontext.Value

// json/v2 needs an explicit time.Duration representation.
var (
	marshalOpts = jsonv2.JoinOptions(
		jsonv2.Deterministic(true),
		jsonv2.FormatNilSliceAsNull(true),
		jsonv2.FormatNilMapAsNull(true),
		jsonv2.WithMarshalers(jsonv2.MarshalToFunc(marshalDuration)),
	)
	unmarshalOpts = jsonv2.WithUnmarshalers(jsonv2.UnmarshalFromFunc(unmarshalDuration))
)

func marshalDuration(enc *jsontext.Encoder, d time.Duration) error {
	return enc.WriteToken(jsontext.String(d.String()))
}

func unmarshalDuration(dec *jsontext.Decoder, d *time.Duration) error {
	var s string
	if err := jsonv2.UnmarshalDecode(dec, &s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

func Marshal(v any) ([]byte, error)      { return jsonv2.Marshal(v, marshalOpts) }
func Unmarshal(data []byte, v any) error { return jsonv2.Unmarshal(data, v, unmarshalOpts) }
func Valid(data []byte) bool             { return jsontext.Value(data).IsValid() }
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	data, err := jsonv2.Marshal(v, marshalOpts)
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
func (e *v2encoder) Encode(v any) error { return jsonv2.MarshalEncode(e.enc, v, marshalOpts) }

type v2decoder struct{ dec *jsontext.Decoder }

func NewDecoder(r io.Reader) Decoder    { return &v2decoder{dec: jsontext.NewDecoder(r)} }
func (d *v2decoder) Decode(v any) error { return jsonv2.UnmarshalDecode(d.dec, v, unmarshalOpts) }
func Version() string                   { return "v2" }
