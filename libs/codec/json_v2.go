//go:build goexperiment.jsonv2

package codec

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"reflect"
	"strconv"
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

func losslessOptions() jsonv2.Options {
	return jsonv2.WithUnmarshalers(jsonv2.JoinUnmarshalers(
		jsonv2.UnmarshalFromFunc(unmarshalDuration),
		jsonv2.UnmarshalFromFunc(unmarshalNumber),
	))
}

// unmarshalNumber keeps a number bound for an interface as a json.Number, and leaves
// every other value to v2.
func unmarshalNumber(dec *jsontext.Decoder, v *any) error {
	if dec.PeekKind() != '0' {
		return jsonv2.SkipFunc
	}
	val, err := dec.ReadValue()
	if err != nil {
		return err
	}
	*v = json.Number(val)
	return nil
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
// that build does.
func encodeError(err error) error {
	var syntactic *jsontext.SyntacticError
	if !errors.As(err, &syntactic) || syntactic.Err == nil {
		return err
	}
	switch {
	case errors.Is(syntactic.Err, jsontext.ErrDuplicateName):
		return &EncodeError{Err: fmt.Errorf("%w: %q", ErrDuplicateKey, syntactic.JSONPointer.LastToken())}
	case errors.Is(syntactic.Err, invalidUTF8()):
		return &EncodeError{Err: fmt.Errorf("writing a string: %w", ErrInvalidUTF8)}
	}
	return err
}

// invalidUTF8 is the error jsontext raises over invalid UTF-8. Its package does not export
// it, so quoting an invalid byte produces it.
func invalidUTF8() error {
	_, err := jsontext.AppendQuote(nil, "\xff")
	if syntactic, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
		return syntactic.Err
	}
	return err
}

func unmarshal(data []byte, v any) error {
	return decodeError(data, v, jsonv2.Unmarshal(data, v, unmarshalOptions()))
}

func unmarshalLossless(data []byte, v any) error {
	return decodeError(data, v, jsonv2.Unmarshal(data, v, losslessOptions()))
}

// unmarshalStrict decodes one value the scanner has read and walked already.
func unmarshalStrict(data []byte, v any) error {
	return decodeError(data, v, jsonv2.Unmarshal(data, v, unmarshalOptions(), jsonv2.RejectUnknownMembers(true)))
}

// decodeError reads data again, only when v2 failed on it, so the error is the one the
// encoding/json build raises: a syntax fault is the codec's [*SyntaxError] with the codec's
// offset, an array of the wrong length the scanner's [*TypeError], and a value its target
// cannot hold a [*TypeError] around v2's own error. The second read walks v's type with no
// value, so it changes nothing v2 left in v.
func decodeError(data []byte, v any, err error) error {
	if err == nil {
		return nil
	}
	if syntax := validate(data); syntax != nil {
		return syntax
	}
	t, _ := pointee(v)
	s := scanner{data: data}
	if lengths := s.document(t, reflect.Value{}); lengths != nil {
		return lengths
	}
	var semantic *jsonv2.SemanticError
	if !errors.As(err, &semantic) || semantic.GoType == nil || !mismatch(semantic) {
		return err
	}
	return &TypeError{JSON: kindName(semantic.JSONKind), GoType: semantic.GoType, Err: err}
}

// mismatch reports whether v2 refused a value for its target's type: with no cause, or a
// number out of range or not of the target's form. A type that decodes itself raises its
// own errors, which stay as they are.
func mismatch(e *jsonv2.SemanticError) bool {
	if e.Err == nil {
		return true
	}
	return !decodesItself(e.GoType) && (errors.Is(e.Err, strconv.ErrRange) || errors.Is(e.Err, strconv.ErrSyntax))
}

// kindName is k in the words encoding/json uses for a value of the wrong type.
func kindName(k jsontext.Kind) string {
	switch k {
	case '"':
		return "string"
	case '0':
		return "number"
	case 't', 'f':
		return "bool"
	case 'n':
		return "null"
	case '{':
		return "object"
	case '[':
		return "array"
	}
	return "value"
}

// refuseDecode refuses nothing: every type decodes here as it is declared.
func refuseDecode(reflect.Type) error { return nil }
