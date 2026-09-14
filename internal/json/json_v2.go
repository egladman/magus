// Package json is magus's single JSON surface, over encoding/json/v2.
//
// A magus build REQUIRES GOEXPERIMENT=jsonv2; requires_jsonv2.go is what a build missing
// it hits. There is deliberately no v1 fallback: v1 escapes <, > and & to their \u00XX
// form and v2 does not, so a shim would make every byte magus writes depend on how the
// binary was built, and magus writes committed generated output and a signed release
// index.
//
// libs/gopherbuzz keeps its own two-armed shim: it is a separate module that must build
// standalone, and inside a magus binary this requirement already forces its v2 arm.
package json

import (
	"bytes"
	legacyjson "encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
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

// UnmarshalStrict is Unmarshal for a TYPED INPUT: a member the target does not declare is
// an error rather than something quietly dropped.
//
// For input a person or an agent composed, never for data magus wrote. A record magus
// reads back from its own store has to survive being written by a newer magus, so dropping
// an unknown field is right there; a field the grader never looks at reads to its author
// as one that was taken into account.
func UnmarshalStrict(data []byte, v any) error {
	return jsonv2.Unmarshal(data, v, unmarshalOpts, jsonv2.RejectUnknownMembers(true))
}

// UnmarshalLossless decodes an untyped JSON document without silently
// collapsing duplicate object keys or large numbers. Configuration mergers use
// it before rewriting a caller-owned document.
func UnmarshalLossless(data []byte, v any) error {
	if err := rejectDuplicateKeys(data); err != nil {
		return err
	}
	decoder := legacyjson.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(v); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected second JSON value")
		}
		return err
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := legacyjson.NewDecoder(bytes.NewReader(data))
	if err := walkJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("unexpected second JSON value")
		}
		return err
	}
	return nil
}

func walkJSONValue(decoder *legacyjson.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(legacyjson.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := map[string]bool{}
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return fmt.Errorf("object key is not a string")
			}
			if keys[name] {
				return fmt.Errorf("duplicate JSON object key %q", name)
			}
			keys[name] = true
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	case '[':
		for decoder.More() {
			if err := walkJSONValue(decoder); err != nil {
				return err
			}
		}
		_, err = decoder.Token()
		return err
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

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
