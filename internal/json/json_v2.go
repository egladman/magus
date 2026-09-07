// Package json is magus's single JSON surface, over encoding/json/v2.
//
// It used to select v2 or v1 on a build tag, and the two arms did not agree on bytes: v1
// escapes the three HTML-significant characters to their \u00XX form and v2 leaves them
// as typed. That made every JSON byte magus writes a function of how the BINARY was
// built. Committed generated output is the place it bit - the v0.4.3 release index was
// published with 336 lines of gen/knowledge-graph.json in the v1 spelling, which the next
// gate read as drift - but the signed release index runs the same risk, which is why
// IndexRelease already carries an omitzero comment saying these bytes must not depend on
// the builder. The fallback is gone rather than reconciled: one implementation cannot
// diverge from itself.
//
// So a magus build REQUIRES GOEXPERIMENT=jsonv2. mise.toml sets it for a checkout, the
// Dockerfiles and setup-magus set it where they compile, and every Go target in
// magusfile.buzz threads it through ctx.withEnv. requires_jsonv2.go is what a build
// missing it hits, so the failure names the variable instead of a missing stdlib package.
//
// libs/gopherbuzz keeps its own two-armed shim on purpose: it is a separate module that
// must build standalone, and inside a magus binary this requirement already forces its v2
// arm to be the one compiled.
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
