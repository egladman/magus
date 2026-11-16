//go:build goexperiment.jsonv2

package codec

import (
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"reflect"
	"time"
)

// walkLikeV2 is false: v2 does on decode what the scanner would.
const walkLikeV2 = false

func version() string { return "v2" }

// marshalOptions is the policy: sorted map keys, nil as null, and a time.Duration as its
// String form, since v2 gives a Duration no default form (go.dev/issue/71631).
func marshalOptions() jsonv2.Options {
	return jsonv2.JoinOptions(
		jsonv2.Deterministic(true),
		jsonv2.FormatNilSliceAsNull(true),
		jsonv2.FormatNilMapAsNull(true),
		jsonv2.WithMarshalers(jsonv2.MarshalToFunc(marshalDuration)),
	)
}

func unmarshalOptions() jsonv2.Options {
	return jsonv2.WithUnmarshalers(jsonv2.UnmarshalFromFunc(unmarshalDuration))
}

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

func marshal(v any) ([]byte, error) {
	b, err := jsonv2.Marshal(v, marshalOptions())
	if err != nil {
		return nil, encodeError(err)
	}
	return b, nil
}

// encodeError words the faults the encoding/json build finds in its own output the way
// that build does. jsonwire's ErrInvalidUTF8 is not exported, so its text names it.
func encodeError(err error) error {
	var syntactic *jsontext.SyntacticError
	if !errors.As(err, &syntactic) || syntactic.Err == nil {
		return err
	}
	switch {
	case errors.Is(syntactic.Err, jsontext.ErrDuplicateName):
		return &EncodeError{Err: fmt.Errorf("%w %q", ErrDuplicateKey, syntactic.JSONPointer.LastToken())}
	case syntactic.Err.Error() == "invalid UTF-8":
		return &EncodeError{Err: fmt.Errorf("%w in a string", ErrInvalidUTF8)}
	}
	return err
}

// unmarshal leaves the input to v2 and, only when it fails, reads it again so a syntax
// fault is the codec's [*SyntaxError] with the codec's offset.
func unmarshal(data []byte, v any) error {
	err := jsonv2.Unmarshal(data, v, unmarshalOptions())
	if err == nil {
		return nil
	}
	if syntax := validate(data); syntax != nil {
		return syntax
	}
	return err
}

// unmarshalStrict decodes one value the scanner has read and walked already.
func unmarshalStrict(data []byte, v any) error {
	return jsonv2.Unmarshal(data, v, unmarshalOptions(), jsonv2.RejectUnknownMembers(true))
}

// refuseDecode refuses nothing: every type decodes here as it is declared.
func refuseDecode(reflect.Type) error { return nil }

type streamDecoder struct{ dec *jsontext.Decoder }

func newStreamDecoder(r io.Reader) streamDecoder {
	return streamDecoder{dec: jsontext.NewDecoder(r)}
}

func (d streamDecoder) decode(v any) error {
	return jsonv2.UnmarshalDecode(d.dec, v, unmarshalOptions())
}
