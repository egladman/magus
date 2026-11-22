// Package codec is one JSON policy for every program that shares the bytes it writes:
// deterministic output, nil slices and maps as null, <, > and & written as typed, time
// durations as strings through [Duration], and a refusal where a document is ambiguous
// (a repeated object key, invalid UTF-8, trailing data).
//
// The package builds two ways and writes the same bytes both ways. Built with
// GOEXPERIMENT=jsonv2 it runs on encoding/json/v2; built without it, on encoding/json.
// encoding/json reads and writes some types differently from v2 and has no switch for
// it: a case-only key match, a named `,inline` field, an `,unknown` field, omitempty on
// a bool, a number, a struct, an interface or a type with its own MarshalJSON, and a bare
// time.Duration. That build refuses such a type with an [*UnsupportedError] instead of
// writing other bytes; a type gets past it with its own MarshalJSON and UnmarshalJSON.
// Errors the codec raises itself ([*SyntaxError], [*MemberError], [*StrictError],
// [*EncodeError], [*TypeError]) read the same in both builds. A value its Go target cannot
// hold is a [*TypeError] in both, wrapping the library's own error. An error a type's own
// method returns is passed through as the underlying library words it.
//
// The JSON Lines store in jsonl.go writes through this API. Nothing in the package reads
// the environment, writes to stdout or stderr, or keeps process-wide state.
package codec

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// RawMessage is a raw encoded JSON value. A Marshal writes it in the codec's form:
// whitespace dropped and each string escaped as Marshal would escape it.
type RawMessage = json.RawMessage

var (
	// ErrDuplicateKey matches a name repeated within one object.
	ErrDuplicateKey = errors.New("duplicate key")
	// ErrInvalidUTF8 matches a string that is not valid UTF-8, an escaped lone surrogate
	// included.
	ErrInvalidUTF8 = errors.New("invalid UTF-8")
	// ErrTrailingData matches anything but whitespace after the one value a document holds.
	ErrTrailingData = errors.New("trailing data")
	// ErrUnknownField matches a member a strict decode's target does not declare.
	ErrUnknownField = errors.New("unknown field")
	// ErrCaseOnlyMatch matches a member whose name equals a field's only when case is
	// ignored. encoding/json would fill the field and encoding/json/v2 would skip the
	// member, so the encoding/json build refuses it.
	ErrCaseOnlyMatch = errors.New("matches a field only when case is ignored")
)

// SyntaxError is input that is not one JSON value the codec reads: malformed JSON, a
// repeated key, invalid UTF-8, a truncated document (Err is then io.ErrUnexpectedEOF),
// or trailing data.
type SyntaxError struct {
	// Offset is the byte offset of the fault in the input.
	Offset int64
	Err    error
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("%v at byte offset %d", e.Err, e.Offset)
}

func (e *SyntaxError) Unwrap() error { return e.Err }

// MemberError is an object member the target refuses: one it does not declare, under a
// strict decode ([ErrUnknownField]), or one that matches a field only when case is
// ignored ([ErrCaseOnlyMatch]).
type MemberError struct {
	// Offset is the byte offset of the member's name.
	Offset int64
	Name   string
	// Field is the field's name for [ErrCaseOnlyMatch], and empty otherwise.
	Field string
	Err   error
}

func (e *MemberError) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("member %q %v %q at byte offset %d", e.Name, e.Err, e.Field, e.Offset)
	}
	return fmt.Sprintf("%v %q at byte offset %d", e.Err, e.Name, e.Offset)
}

func (e *MemberError) Unwrap() error { return e.Err }

// StrictError is a refusal from [UnmarshalStrict] at Offset, a byte
// offset into the input. Err is the [*SyntaxError] or [*MemberError] behind it, or a
// [*TypeError] or the decoder's own error, which Offset then places at the start of the
// value it was reading.
type StrictError struct {
	Offset int64
	Err    error
}

func (e *StrictError) Error() string { return e.Err.Error() }
func (e *StrictError) Unwrap() error { return e.Err }

// TypeError is a JSON value its Go target cannot hold: a string for an int, a number out
// of the target's range, or an array of another length than its Go array's. Err is the
// library's own error, nil when the codec found the fault itself.
type TypeError struct {
	// JSON is the kind of value read: "string", "number", "bool", "null", "object" or
	// "array".
	JSON   string
	GoType reflect.Type
	// Reason says more where the kind alone does not, such as an array's length.
	Reason string
	Err    error
}

func (e *TypeError) Error() string {
	msg := fmt.Sprintf("cannot decode a JSON %s into Go %v", e.JSON, e.GoType)
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg
}

func (e *TypeError) Unwrap() error { return e.Err }

// EncodeError is a value Marshal would not write: a string that is not valid UTF-8, or
// an object holding one key twice, which a type's own MarshalJSON or MarshalText can
// produce. Err matches [ErrInvalidUTF8] or [ErrDuplicateKey].
type EncodeError struct{ Err error }

func (e *EncodeError) Error() string { return "codec: cannot encode: " + e.Err.Error() }
func (e *EncodeError) Unwrap() error { return e.Err }

// UnsupportedError is a type the encoding/json build cannot read or write the way the
// encoding/json/v2 build does. Op is "encode" or "decode", Type the value's type, Field
// the path of Go field names to the cause, and Reason what that field holds.
type UnsupportedError struct {
	Op     string
	Type   string
	Field  string
	Reason string
}

func (e *UnsupportedError) Error() string {
	at := e.Type
	if e.Field != "" {
		at += "." + e.Field
	}
	return fmt.Sprintf("codec: this build (encoding/json) cannot %s %s: %s holds %s, which encoding/json/v2 reads and writes differently,"+
		" so give the type its own MarshalJSON and UnmarshalJSON, or build with GOEXPERIMENT=jsonv2", e.Op, e.Type, at, e.Reason)
}

// Marshal returns the JSON encoding of v: map keys sorted, nil slices and maps as null,
// <, > and & unescaped, and a time.Duration (the encoding/json/v2 build) or [Duration] as
// its String form. It fails with an [*EncodeError] over a string that is not valid UTF-8,
// and in the encoding/json build with an [*UnsupportedError] over a type that build would
// write differently.
func Marshal(v any) ([]byte, error) { return marshal(v) }

// MarshalIndent is [Marshal] with each element on its own line, starting with prefix and
// one indent per level of nesting. prefix and indent hold only spaces and tabs.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	if strings.Trim(prefix, " \t") != "" || strings.Trim(indent, " \t") != "" {
		return nil, fmt.Errorf("codec: an indent and its prefix hold only spaces and tabs, not %q and %q", indent, prefix)
	}
	compact, err := marshal(v)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	if err := json.Indent(&b, compact, prefix, indent); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// Unmarshal decodes the one JSON value in data into v. An object member v does not
// declare is skipped. A malformed document fails with a [*SyntaxError].
func Unmarshal(data []byte, v any) error { return unmarshal(data, v) }

// UnmarshalStrict is Unmarshal for a TYPED INPUT: a member the target does not declare,
// or a name repeated within an object, is a [*StrictError] rather than something quietly
// dropped or overwritten. An empty document matches io.ErrUnexpectedEOF.
//
// For input a person or an agent composed, never for data a program wrote. A record read
// back from a store has to survive being written by a newer writer, so dropping an
// unknown field is right there; a field the reader never looks at reads to its author as
// one that was taken into account.
func UnmarshalStrict(data []byte, v any) error {
	d := newStrictDecoder(data)
	if err := d.Decode(v); err != nil {
		return err
	}
	return d.End()
}

// UnmarshalLossless is [Unmarshal] without rounding a large number: a number decoded into
// an interface is a json.Number holding its text. A repeated key is refused as Unmarshal
// refuses it. Configuration mergers use it before rewriting a document someone else owns.
func UnmarshalLossless(data []byte, v any) error { return unmarshalLossless(data, v) }

// Valid reports whether data is one JSON value Unmarshal reads: well formed, valid
// UTF-8, and no key repeated within an object.
func Valid(data []byte) bool { return validate(data) == nil }

// MarshalWithUnknown writes v, a struct or a pointer to one, as [Marshal] does, then each
// member of unknown after v's own, sorted by name: the members a record kept from a decode
// by [UnmarshalWithUnknown]. A name v declares is an [*EncodeError] matching
// [ErrDuplicateKey], since writing both would make the document ambiguous.
//
// It is the body of a MarshalJSON for a type that keeps the members it does not declare.
// Pass v as a defined type without that method (`type plain T`), or the call recurses.
func MarshalWithUnknown(v any, unknown map[string]RawMessage) ([]byte, error) {
	b, err := Marshal(v)
	if err != nil || len(unknown) == 0 {
		return b, err
	}
	t := reflect.TypeOf(v)
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct || len(b) < 2 || b[0] != '{' {
		return nil, &EncodeError{Err: fmt.Errorf("unknown members need a struct written as an object, and %T is not one", v)}
	}
	declared := structFields(t).byName
	out := slices.Clone(b[:len(b)-1])
	for _, name := range slices.Sorted(maps.Keys(unknown)) {
		if _, ok := declared[name]; ok {
			return nil, &EncodeError{Err: fmt.Errorf("%w: %q: the type declares it and the unknown members carry it too", ErrDuplicateKey, name)}
		}
		key, err := Marshal(name)
		if err != nil {
			return nil, err
		}
		value, err := Marshal(unknown[name])
		if err != nil {
			return nil, err
		}
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = append(append(append(out, key...), ':'), value...)
	}
	return append(out, '}'), nil
}

// UnmarshalWithUnknown decodes data into v, a non-nil pointer to a struct, as [Unmarshal]
// does, and returns the members v's type does not declare, nil when there are none. A
// member whose name matches a field only when case is ignored is one it does not declare,
// so both builds keep it here. It is the body of an UnmarshalJSON for a type that keeps
// them; see [MarshalWithUnknown].
//
// [UnmarshalStrict] still refuses those members for a type whose bag field is tagged
// `codec:"unknown"`: the strict walk reads such a type's own fields rather than leaving the
// type to its UnmarshalJSON, so an authored record keeps no member it does not declare.
func UnmarshalWithUnknown(data []byte, v any) (map[string]RawMessage, error) {
	t, _ := pointee(v)
	if t == nil || t.Kind() != reflect.Struct {
		return nil, fmt.Errorf("codec: unknown members are kept by a struct, and %T does not point to one", v)
	}
	var members map[string]RawMessage
	if err := Unmarshal(data, &members); err != nil {
		if _, typed := errors.AsType[*TypeError](err); typed {
			return nil, Unmarshal(data, v)
		}
		return nil, err
	}
	if members == nil {
		return nil, Unmarshal(data, v)
	}
	declared := structFields(t).byName
	known := []byte{'{'}
	var unknown map[string]RawMessage
	for _, name := range slices.Sorted(maps.Keys(members)) {
		raw := members[name]
		if _, ok := declared[name]; !ok {
			if unknown == nil {
				unknown = map[string]RawMessage{}
			}
			unknown[name] = raw
			continue
		}
		if len(known) > 1 {
			known = append(known, ',')
		}
		known = append(append(appendQuote(known, name), ':'), raw...)
	}
	// Only the declared members reach v: encoding/json would fill a field from a member that
	// matches it only when case is ignored, and that member belongs to the bag.
	if err := Unmarshal(append(known, '}'), v); err != nil {
		return nil, err
	}
	return unknown, nil
}

// keepsUnknown reports whether t is a struct that keeps its undeclared members in a field
// tagged `codec:"unknown"`, its own or one an embedded struct promotes.
func keepsUnknown(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := range t.NumField() {
		sf := t.Field(i)
		if sf.Tag.Get("codec") == "unknown" || (sf.Anonymous && keepsUnknown(sf.Type)) {
			return true
		}
	}
	return false
}

// Version names the library this build runs on: "v2" for encoding/json/v2, "v1" for
// encoding/json.
func Version() string { return version() }

// Encoder writes JSON values to a stream, each followed by a newline.
type Encoder struct{ w io.Writer }

// NewEncoder writes to w.
func NewEncoder(w io.Writer) *Encoder { return &Encoder{w: w} }

// Encode writes v as [Marshal] encodes it, then a newline, in one Write.
func (e *Encoder) Encode(v any) error {
	b, err := marshal(v)
	if err != nil {
		return err
	}
	_, err = e.w.Write(append(b, '\n'))
	return err
}

// Decoder reads a stream of JSON values separated by whitespace.
type Decoder struct {
	r io.Reader
	// buf holds the bytes read and not yet decoded; off is the stream offset of buf[0].
	buf []byte
	off int64
	// readErr is the error the last Read returned, io.EOF at the end of the stream.
	readErr error
	// fault is a malformed stream's error, which every later Decode returns.
	fault error
	// adjoined says the last value read ended with no whitespace after it yet.
	adjoined bool
}

// NewDecoder reads from r, buffering as it goes. It reads only as far as the value it
// decodes, and past a number to the byte that ends it.
func NewDecoder(r io.Reader) *Decoder { return &Decoder{r: r} }

// Decode reads the next value into v as [Unmarshal] does. At the end of the stream it
// returns io.EOF. A malformed stream is a [*SyntaxError] whose Offset counts from the start
// of the stream, a value that does not follow the last one's whitespace included
// ([ErrTrailingData]); once the stream is malformed, every later Decode returns that error.
func (d *Decoder) Decode(v any) error {
	if d.fault != nil {
		return d.fault
	}
	start, end, err := d.next()
	if err != nil {
		if _, syntax := errors.AsType[*SyntaxError](err); syntax {
			d.fault = err
		}
		return err
	}
	raw := d.buf[start:end]
	at := d.off + int64(start)
	d.buf, d.off = d.buf[end:], d.off+int64(end)
	d.adjoined = true
	if err := unmarshal(raw, v); err != nil {
		return shiftOffset(err, at)
	}
	return nil
}

// next reads until buf holds one whole value and returns where it starts and ends.
func (d *Decoder) next() (int, int, error) {
	for {
		s := scanner{data: d.buf}
		s.skipSpace()
		if s.pos > 0 {
			d.adjoined = false
		}
		if s.pos == len(d.buf) {
			if d.readErr != nil {
				return 0, 0, d.atEnd()
			}
			d.fill()
			continue
		}
		if d.adjoined {
			return 0, 0, &SyntaxError{Offset: d.off, Err: ErrTrailingData}
		}
		start := s.pos
		err := s.value(0, nil, reflect.Value{})
		// A number ends only at the byte after it, which may not have arrived.
		incomplete := errors.Is(err, io.ErrUnexpectedEOF) ||
			(err == nil && s.pos == len(d.buf) && isNumberStart(d.buf[start]))
		if incomplete && d.readErr == nil {
			d.fill()
			continue
		}
		if err != nil {
			return 0, 0, shiftOffset(err, d.off)
		}
		return start, s.pos, nil
	}
}

func (d *Decoder) atEnd() error {
	if errors.Is(d.readErr, io.EOF) {
		return io.EOF
	}
	return d.readErr
}

func (d *Decoder) fill() {
	if len(d.buf) == cap(d.buf) {
		grown := make([]byte, len(d.buf), max(2*cap(d.buf), 4096))
		copy(grown, d.buf)
		d.buf = grown
	}
	n, err := d.r.Read(d.buf[len(d.buf):cap(d.buf)])
	d.buf = d.buf[:len(d.buf)+n]
	if err != nil {
		d.readErr = err
	}
}

func isNumberStart(c byte) bool { return c == '-' || isDigit(c) }

// shiftOffset moves the offset of a codec error found in a slice of the input by the
// slice's start, so it counts from the start of the input.
func shiftOffset(err error, by int64) error {
	if syntax, ok := errors.AsType[*SyntaxError](err); ok {
		syntax.Offset += by
	}
	if member, ok := errors.AsType[*MemberError](err); ok {
		member.Offset += by
	}
	return err
}

// Duration is a time.Duration written as its String form ("1h30m0s") and read back from
// one. A bare time.Duration field has no portable JSON form: the encoding/json/v2 build
// writes a string and the encoding/json build refuses it.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return appendQuote(nil, time.Duration(d).String()), nil
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}
	s := scanner{data: b}
	s.skipSpace()
	if s.pos < len(s.data) && s.data[s.pos] != '"' {
		return fmt.Errorf("codec: a duration is a string such as \"1h30m0s\", not %s", b)
	}
	text, err := s.stringLiteral()
	if err != nil {
		return err
	}
	parsed, err := time.ParseDuration(text)
	if err != nil {
		return fmt.Errorf("codec: %w", err)
	}
	*d = Duration(parsed)
	return nil
}

// strictDecoder reads one document a person composed, token by token, so a caller can
// say where each part of it starts. Like [UnmarshalStrict] it refuses what a typed input
// must not carry: a name repeated within an object, and a member the target does not
// declare. Each refusal is a [*StrictError] carrying the byte offset it names.
type strictDecoder struct{ s scanner }

// newStrictDecoder reads data.
func newStrictDecoder(data []byte) *strictDecoder {
	return &strictDecoder{s: scanner{data: data, strict: true, likeV2: walkLikeV2}}
}

// Offset is the byte offset just past the last token or value read; whitespace and a
// separator may sit between it and the next one.
func (d *strictDecoder) Offset() int64 { return int64(d.s.pos) }

// More reports whether the object or array being read holds another member or element.
func (d *strictDecoder) More() bool {
	d.s.skipSeparator()
	if d.s.pos >= len(d.s.data) {
		return false
	}
	c := d.s.data[d.s.pos]
	return c != '}' && c != ']'
}

// Delim reads the next token, which must be want: one of '{', '}', '[' or ']'.
func (d *strictDecoder) Delim(want byte) error {
	d.s.skipSeparator()
	if d.s.pos >= len(d.s.data) {
		return d.refuse(d.s.errorAt(d.s.pos, io.ErrUnexpectedEOF))
	}
	if got := d.s.data[d.s.pos]; got != want {
		return d.refuse(d.s.errorAt(d.s.pos, fmt.Errorf("want %q, got %s", string(want), quoteChar(got))))
	}
	d.s.pos++
	return nil
}

// Name reads the next object member's name and the colon after it.
func (d *strictDecoder) Name() (string, error) {
	d.s.skipSeparator()
	if d.s.pos >= len(d.s.data) {
		return "", d.refuse(d.s.errorAt(d.s.pos, io.ErrUnexpectedEOF))
	}
	if c := d.s.data[d.s.pos]; c != '"' {
		return "", d.refuse(d.s.errorAt(d.s.pos, fmt.Errorf("want a member name, got %s", quoteChar(c))))
	}
	name, err := d.s.stringLiteral()
	if err != nil {
		return "", d.refuse(err)
	}
	d.s.skipSpace()
	if d.s.pos >= len(d.s.data) || d.s.data[d.s.pos] != ':' {
		return "", d.refuse(d.s.errorAt(d.s.pos, errors.New("want ':' after a member name")))
	}
	d.s.pos++
	return name, nil
}

// Decode reads the next value into v.
func (d *strictDecoder) Decode(v any) error {
	d.s.skipSeparator()
	start := d.s.pos
	t, rv := pointee(v)
	if err := refuseDecode(t); err != nil {
		return &StrictError{Offset: int64(start), Err: err}
	}
	if err := d.s.value(0, t, rv); err != nil {
		if _, typed := errors.AsType[*TypeError](err); typed {
			return &StrictError{Offset: int64(start), Err: err}
		}
		return d.refuse(err)
	}
	if err := unmarshalStrict(d.s.data[start:d.s.pos], v); err != nil {
		return &StrictError{Offset: int64(start), Err: err}
	}
	return nil
}

// End reports an error unless nothing but whitespace follows the document.
func (d *strictDecoder) End() error {
	d.s.skipSpace()
	if d.s.pos < len(d.s.data) {
		return d.refuse(d.s.errorAt(d.s.pos, ErrTrailingData))
	}
	return nil
}

// refuse places one of the codec's own errors at the offset it carries.
func (d *strictDecoder) refuse(err error) error {
	var syntax *SyntaxError
	var member *MemberError
	switch {
	case errors.As(err, &syntax):
		return &StrictError{Offset: syntax.Offset, Err: err}
	case errors.As(err, &member):
		return &StrictError{Offset: member.Offset, Err: err}
	}
	return &StrictError{Offset: int64(d.s.pos), Err: err}
}

// pointee is the value v points at and its type, or neither when v is not a non-nil
// pointer, which the decoders refuse in their own words.
func pointee(v any) (reflect.Type, reflect.Value) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Pointer || rv.IsNil() {
		return nil, reflect.Value{}
	}
	return rv.Type().Elem(), rv.Elem()
}

// validate reads data as one document with no target, so it refuses only syntax.
func validate(data []byte) error {
	s := scanner{data: data}
	return s.document(nil, reflect.Value{})
}

// ObjectSchema describes the members of an object or the elements of an array.
// A nil child leaves its contents to the field's decoder.
type ObjectSchema struct {
	Name    string
	Closed  bool
	Fields  map[string]*ObjectSchema
	Element *ObjectSchema
}

// UnknownField identifies an undeclared member of a closed object.
type UnknownField struct{ Object, Path, Name string }

// InspectedObject contains the typed decoder's input and the members excluded
// from it. Known aliases the input when there are no extensions.
type InspectedObject struct {
	Known      []byte
	RootFields map[string]struct{}
	Extensions map[string]map[string]RawMessage // keyed by the enclosing object's path
	Rejected   []UnknownField
}

// Inspect validates syntax and classifies members in one walk. Extension values
// are copied so callers may retain them after releasing the original line.
func (schema *ObjectSchema) Inspect(data []byte) (InspectedObject, error) {
	s := scanner{data: data, schema: schema}
	if err := s.document(nil, reflect.Value{}); err != nil {
		return InspectedObject{}, err
	}
	if s.copied == 0 {
		s.inspected.Known = data
	} else {
		s.inspected.Known = append(s.inspected.Known, data[s.copied:]...)
	}
	return s.inspected, nil
}

type pathPart struct {
	name  string
	index int
	array bool
}

func fieldPath(parts []pathPart) string {
	var b strings.Builder
	writeName := func(n string) {
		if n == "" || strings.ContainsAny(n, ".,[]\"\\\t") || !strconv.CanBackquote(n) {
			b.WriteByte('[')
			b.WriteString(strconv.Quote(n))
			b.WriteByte(']')
			return
		}
		if b.Len() > 0 {
			b.WriteByte('.')
		}
		b.WriteString(n)
	}
	for _, p := range parts {
		if p.array {
			b.WriteByte('[')
			b.WriteString(strconv.Itoa(p.index))
			b.WriteByte(']')
		} else {
			writeName(p.name)
		}
	}
	return b.String()
}

// maxDepth is the nesting both libraries refuse past.
const maxDepth = 10000

// scanner reads JSON text and refuses what encoding/json/v2 refuses by default: a
// repeated name within an object, invalid UTF-8 (an escaped lone surrogate included),
// and anything but whitespace after the value. Both builds run it, so its refusals and
// their offsets are the same in each.
//
// Given a target type it also walks the target and refuses, as v2 does, an array whose
// length is not its Go array's. strict refuses a member the target does not declare,
// caseOnly refuses a member that matches a field only when case is ignored, and likeV2
// does what v2 does on decode and encoding/json does not: it sets a non-nullable target to
// its zero value on null. With canon set it appends the input to out in the form
// encoding/json/v2 writes: no whitespace and each string escaped the v2 way.
// sawReplacement records a \ufffd escape, which encoding/json also writes in place of an
// invalid byte.
type scanner struct {
	data []byte
	pos  int

	strict    bool
	schema    *ObjectSchema
	path      []pathPart
	inspected InspectedObject
	copied    int // first input byte not yet copied after removing an extension
	caseOnly  bool
	likeV2    bool

	canon          bool
	sawReplacement bool
	out            []byte

	fields map[reflect.Type]*fieldSet
}

func (s *scanner) errorAt(off int, err error) *SyntaxError {
	return &SyntaxError{Offset: int64(off), Err: err}
}

func (s *scanner) skipSpace() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

// skipSeparator steps over whitespace and one comma, where a strictDecoder caller
// reading members token by token leaves one.
func (s *scanner) skipSeparator() {
	s.skipSpace()
	if s.pos < len(s.data) && s.data[s.pos] == ',' {
		s.pos++
		s.skipSpace()
	}
}

func (s *scanner) emit(b ...byte) {
	if s.canon {
		s.out = append(s.out, b...)
	}
}

// document reads one value into the target t, v describes and refuses trailing data.
func (s *scanner) document(t reflect.Type, v reflect.Value) error {
	if err := s.value(0, t, v); err != nil {
		return err
	}
	s.skipSpace()
	if s.pos < len(s.data) {
		return s.errorAt(s.pos, ErrTrailingData)
	}
	return nil
}

func (s *scanner) value(depth int, t reflect.Type, v reflect.Value) error {
	s.skipSpace()
	if s.pos >= len(s.data) {
		return s.errorAt(s.pos, io.ErrUnexpectedEOF)
	}
	if depth > maxDepth {
		return s.errorAt(s.pos, fmt.Errorf("nesting deeper than %d", maxDepth))
	}
	c := s.data[s.pos]
	if c == 'n' {
		if err := s.literal("null"); err != nil {
			return err
		}
		s.null(t, v)
		return nil
	}
	t, v = s.target(t, v)
	switch {
	case c == '{':
		return s.object(depth, t, v)
	case c == '[':
		return s.array(depth, t, v)
	case c == '"':
		_, err := s.stringLiteral()
		return err
	case c == 't':
		return s.literal("true")
	case c == 'f':
		return s.literal("false")
	case c == '-' || isDigit(c):
		return s.number()
	}
	return s.errorAt(s.pos, fmt.Errorf("invalid character %s looking for the start of a value", quoteChar(c)))
}

// null sets v to its zero value when likeV2 asks for it and encoding/json would leave it
// alone: a value that is not a pointer, interface, map or slice and has no UnmarshalJSON
// of its own.
func (s *scanner) null(t reflect.Type, v reflect.Value) {
	if !s.likeV2 || t == nil || !v.IsValid() || !v.CanSet() || decodesItself(t) {
		return
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice:
		return
	}
	if !v.IsZero() {
		v.SetZero()
	}
}

// target is where a value goes once pointers are followed. A type that decodes itself,
// and an interface, take any value, so the walk reads the value with no target. A strict
// walk still reads a type that keeps its undeclared members (see [keepsUnknown]): its
// UnmarshalJSON would keep the very member strict exists to refuse.
func (s *scanner) target(t reflect.Type, v reflect.Value) (reflect.Type, reflect.Value) {
	for t != nil {
		if decodesItself(t) && (!s.strict || !keepsUnknown(t)) {
			return nil, reflect.Value{}
		}
		switch t.Kind() {
		case reflect.Pointer:
			if v.IsValid() && !v.IsNil() {
				v = v.Elem()
			} else {
				v = reflect.Value{}
			}
			t = t.Elem()
		case reflect.Interface:
			return nil, reflect.Value{}
		default:
			return t, v
		}
	}
	return nil, reflect.Value{}
}

func (s *scanner) object(depth int, t reflect.Type, v reflect.Value) error {
	s.pos++
	s.emit('{')
	var fs *fieldSet
	var elem reflect.Type
	if t != nil {
		switch t.Kind() {
		case reflect.Struct:
			fs = s.cachedFields(t)
		case reflect.Map:
			elem = t.Elem()
		}
	}
	s.skipSpace()
	if s.pos < len(s.data) && s.data[s.pos] == '}' {
		s.pos++
		s.emit('}')
		return nil
	}
	seen := map[string]struct{}{}
	if depth == 0 && s.schema != nil {
		s.inspected.RootFields = make(map[string]struct{})
		seen = s.inspected.RootFields
	}
	separator, kept := s.pos, false
	for {
		s.skipSpace()
		if s.pos >= len(s.data) {
			return s.errorAt(s.pos, io.ErrUnexpectedEOF)
		}
		if c := s.data[s.pos]; c != '"' {
			return s.errorAt(s.pos, fmt.Errorf("invalid character %s looking for the start of an object key", quoteChar(c)))
		}
		at := s.pos
		name, err := s.stringLiteral()
		if err != nil {
			return err
		}
		if _, dup := seen[name]; dup {
			return s.errorAt(at, fmt.Errorf("%w: %q", ErrDuplicateKey, name))
		}
		seen[name] = struct{}{}
		s.skipSpace()
		if s.pos >= len(s.data) {
			return s.errorAt(s.pos, io.ErrUnexpectedEOF)
		}
		if c := s.data[s.pos]; c != ':' {
			return s.errorAt(s.pos, fmt.Errorf("invalid character %s after an object key", quoteChar(c)))
		}
		s.pos++
		s.emit(':')
		var ft reflect.Type
		var fv reflect.Value
		switch {
		case fs != nil:
			if f, ok := fs.byName[name]; ok {
				ft, fv = f.typ, fieldValue(v, f.index)
				break
			}
			if fs.fallback {
				break
			}
			if s.strict {
				return &MemberError{Offset: int64(at), Name: name, Err: ErrUnknownField}
			}
			if field, ok := fs.foldMatch(name); ok && s.caseOnly {
				return &MemberError{Offset: int64(at), Name: name, Field: field, Err: ErrCaseOnlyMatch}
			}
		case elem != nil:
			ft = elem
		}
		parent := s.schema
		extension := false
		if parent != nil {
			child, known := parent.Fields[name]
			if !known && parent.Closed {
				s.inspected.Rejected = append(s.inspected.Rejected, UnknownField{Object: parent.Name, Name: name, Path: fieldPath(append(s.path, pathPart{name: name}))})
			}
			extension = !known && !parent.Closed && parent.Fields != nil
			s.schema = child
			if child != nil {
				s.path = append(s.path, pathPart{name: name})
			}
		}
		start := s.pos
		if err := s.value(depth+1, ft, fv); err != nil {
			return err
		}
		if s.schema != nil {
			s.path = s.path[:len(s.path)-1]
		}
		s.schema = parent
		end := s.pos
		s.skipSpace()
		if s.pos >= len(s.data) {
			return s.errorAt(s.pos, io.ErrUnexpectedEOF)
		}
		if extension {
			path := fieldPath(s.path)
			if s.inspected.Extensions == nil {
				s.inspected.Extensions = make(map[string]map[string]RawMessage)
			}
			if s.inspected.Extensions[path] == nil {
				s.inspected.Extensions[path] = make(map[string]RawMessage)
			}
			s.inspected.Extensions[path][name] = bytes.Clone(bytes.TrimSpace(s.data[start:end]))
			from, to := at, end
			if kept {
				from = separator
			} else if s.data[s.pos] == ',' {
				to = s.pos + 1
			}
			s.inspected.Known = append(s.inspected.Known, s.data[s.copied:from]...)
			s.copied = to
		} else {
			kept = true
		}
		separator = s.pos
		switch c := s.data[s.pos]; c {
		case ',':
			s.pos++
			s.emit(',')
		case '}':
			s.pos++
			s.emit('}')
			return nil
		default:
			return s.errorAt(s.pos, fmt.Errorf("invalid character %s after an object member", quoteChar(c)))
		}
	}
}

func (s *scanner) array(depth int, t reflect.Type, v reflect.Value) error {
	s.pos++
	s.emit('[')
	var elem reflect.Type
	fixed := false
	if t != nil && (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) {
		elem, fixed = t.Elem(), t.Kind() == reflect.Array
	}
	length := func(n int) error {
		if fixed && n != t.Len() {
			reason := fmt.Sprintf("an array of %d elements", n)
			if n == 1 {
				reason = "an array of 1 element"
			}
			return &TypeError{JSON: "array", GoType: t, Reason: reason}
		}
		return nil
	}
	s.skipSpace()
	if s.pos < len(s.data) && s.data[s.pos] == ']' {
		s.pos++
		s.emit(']')
		return length(0)
	}
	for i := 0; ; i++ {
		var ev reflect.Value
		if fixed && v.IsValid() && i < v.Len() {
			ev = v.Index(i)
		}
		parent := s.schema
		if parent != nil {
			s.schema = parent.Element
			if s.schema != nil {
				s.path = append(s.path, pathPart{index: i, array: true})
			}
		}
		if err := s.value(depth+1, elem, ev); err != nil {
			return err
		}
		if s.schema != nil {
			s.path = s.path[:len(s.path)-1]
		}
		s.schema = parent
		s.skipSpace()
		if s.pos >= len(s.data) {
			return s.errorAt(s.pos, io.ErrUnexpectedEOF)
		}
		switch c := s.data[s.pos]; c {
		case ',':
			s.pos++
			s.emit(',')
		case ']':
			s.pos++
			s.emit(']')
			return length(i + 1)
		default:
			return s.errorAt(s.pos, fmt.Errorf("invalid character %s after an array element", quoteChar(c)))
		}
	}
}

func (s *scanner) literal(want string) error {
	end := min(s.pos+len(want), len(s.data))
	got := s.data[s.pos:end]
	for i := range got {
		if got[i] != want[i] {
			return s.errorAt(s.pos+i, fmt.Errorf("invalid character %s in literal %s", quoteChar(got[i]), want))
		}
	}
	if len(got) < len(want) {
		return s.errorAt(end, io.ErrUnexpectedEOF)
	}
	s.emit(got...)
	s.pos = end
	return nil
}

// number reads -?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)? and copies it as written.
func (s *scanner) number() error {
	start := s.pos
	if s.data[s.pos] == '-' {
		s.pos++
	}
	switch {
	case s.pos >= len(s.data):
		return s.errorAt(s.pos, io.ErrUnexpectedEOF)
	case s.data[s.pos] == '0':
		s.pos++
	case isDigit(s.data[s.pos]):
		s.digits()
	default:
		return s.errorAt(s.pos, fmt.Errorf("invalid character %s in numeric literal", quoteChar(s.data[s.pos])))
	}
	if s.pos < len(s.data) && s.data[s.pos] == '.' {
		s.pos++
		if err := s.needDigit(); err != nil {
			return err
		}
		s.digits()
	}
	if s.pos < len(s.data) && (s.data[s.pos] == 'e' || s.data[s.pos] == 'E') {
		s.pos++
		if s.pos < len(s.data) && (s.data[s.pos] == '+' || s.data[s.pos] == '-') {
			s.pos++
		}
		if err := s.needDigit(); err != nil {
			return err
		}
		s.digits()
	}
	s.emit(s.data[start:s.pos]...)
	return nil
}

func (s *scanner) needDigit() error {
	if s.pos >= len(s.data) {
		return s.errorAt(s.pos, io.ErrUnexpectedEOF)
	}
	if !isDigit(s.data[s.pos]) {
		return s.errorAt(s.pos, fmt.Errorf("invalid character %s in numeric literal", quoteChar(s.data[s.pos])))
	}
	return nil
}

func (s *scanner) digits() {
	for s.pos < len(s.data) && isDigit(s.data[s.pos]) {
		s.pos++
	}
}

// stringLiteral reads the string at s.pos and returns it unescaped.
func (s *scanner) stringLiteral() (string, error) {
	s.pos++
	from := s.pos
	var b []byte
	escaped := false
	for {
		if s.pos >= len(s.data) {
			return "", s.errorAt(s.pos, io.ErrUnexpectedEOF)
		}
		c := s.data[s.pos]
		switch {
		case c == '"':
			var text string
			if escaped {
				text = string(append(b, s.data[from:s.pos]...))
			} else {
				text = string(s.data[from:s.pos])
			}
			s.pos++
			if s.canon {
				s.out = appendQuote(s.out, text)
			}
			return text, nil
		case c == '\\':
			b = append(b, s.data[from:s.pos]...)
			escaped = true
			r, err := s.escape()
			if err != nil {
				return "", err
			}
			b = utf8.AppendRune(b, r)
			from = s.pos
		case c < 0x20:
			return "", s.errorAt(s.pos, fmt.Errorf("invalid character %s in a string", quoteChar(c)))
		case c < utf8.RuneSelf:
			s.pos++
		default:
			r, n := utf8.DecodeRune(s.data[s.pos:])
			if r == utf8.RuneError && n == 1 {
				if !utf8.FullRune(s.data[s.pos:]) {
					return "", s.errorAt(len(s.data), io.ErrUnexpectedEOF)
				}
				return "", s.errorAt(s.pos, fmt.Errorf("reading a string: %w", ErrInvalidUTF8))
			}
			s.pos += n
		}
	}
}

// escape reads the escape sequence at s.pos.
func (s *scanner) escape() (rune, error) {
	at := s.pos
	if s.pos+1 >= len(s.data) {
		return 0, s.errorAt(len(s.data), io.ErrUnexpectedEOF)
	}
	c := s.data[s.pos+1]
	s.pos += 2
	switch c {
	case '"', '\\', '/':
		return rune(c), nil
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'u':
		r, err := s.hex4(s.pos)
		if err != nil {
			return 0, err
		}
		s.pos += 4
		if r == utf8.RuneError {
			s.sawReplacement = true
		}
		if !utf16.IsSurrogate(r) {
			return r, nil
		}
		if s.pos+6 <= len(s.data) && s.data[s.pos] == '\\' && s.data[s.pos+1] == 'u' {
			low, err := s.hex4(s.pos + 2)
			if err != nil {
				return 0, err
			}
			if pair := utf16.DecodeRune(r, low); pair != utf8.RuneError {
				s.pos += 6
				return pair, nil
			}
		}
		return 0, s.errorAt(at, fmt.Errorf("reading a string: %w", ErrInvalidUTF8))
	}
	return 0, s.errorAt(at, fmt.Errorf("invalid escape sequence %q in a string", s.data[at:s.pos]))
}

func (s *scanner) hex4(at int) (rune, error) {
	if at+4 > len(s.data) {
		return 0, s.errorAt(len(s.data), io.ErrUnexpectedEOF)
	}
	var r rune
	for i, c := range s.data[at : at+4] {
		var d byte
		switch {
		case isDigit(c):
			d = c - '0'
		case 'a' <= c && c <= 'f':
			d = c - 'a' + 10
		case 'A' <= c && c <= 'F':
			d = c - 'A' + 10
		default:
			return 0, s.errorAt(at+i, fmt.Errorf("invalid character %s in a \\u escape", quoteChar(c)))
		}
		r = r<<4 | rune(d)
	}
	return r, nil
}

// appendQuote writes text as encoding/json/v2 does by default: only '"', '\\' and the
// control characters escaped, the common ones in their short form.
func appendQuote(dst []byte, text string) []byte {
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	from := 0
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c >= 0x20 && c != '"' && c != '\\' {
			continue
		}
		dst = append(dst, text[from:i]...)
		switch c {
		case '"', '\\':
			dst = append(dst, '\\', c)
		case '\b':
			dst = append(dst, '\\', 'b')
		case '\f':
			dst = append(dst, '\\', 'f')
		case '\n':
			dst = append(dst, '\\', 'n')
		case '\r':
			dst = append(dst, '\\', 'r')
		case '\t':
			dst = append(dst, '\\', 't')
		default:
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
		}
		from = i + 1
	}
	dst = append(dst, text[from:]...)
	return append(dst, '"')
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

func quoteChar(c byte) string {
	if c == '\'' {
		return `'\''`
	}
	if c == '"' {
		return `'"'`
	}
	return "'" + strings.Trim(strconv.QuoteRune(rune(c)), `'`) + "'"
}

// field is one JSON object member a struct declares: the Go field it fills and its type.
type field struct {
	index  []int
	typ    reflect.Type
	depth  int
	tagged bool
}

// fieldSet is the members a struct declares. fallback says it also takes the members it
// does not declare, into an `,unknown` field or a named `,inline` map.
type fieldSet struct {
	byName   map[string]field
	fallback bool
}

// foldMatch returns the declared name that equals name when case is ignored.
func (fs *fieldSet) foldMatch(name string) (string, bool) {
	for declared := range fs.byName {
		if strings.EqualFold(declared, name) {
			return declared, true
		}
	}
	return "", false
}

func (s *scanner) cachedFields(t reflect.Type) *fieldSet {
	if fs, ok := s.fields[t]; ok {
		return fs
	}
	if s.fields == nil {
		s.fields = map[reflect.Type]*fieldSet{}
	}
	fs := structFields(t)
	s.fields[t] = fs
	return fs
}

// structFields resolves t's members the way both libraries do: embedded structs and
// `,inline` structs promote their fields, the shallowest field of a name wins, a tagged
// one breaks a tie, and an unbroken tie hides the name.
func structFields(t reflect.Type) *fieldSet {
	fs := &fieldSet{byName: map[string]field{}}
	candidates := map[string][]field{}
	visited := map[reflect.Type]bool{}
	var walk func(t reflect.Type, index []int, depth int)
	walk = func(t reflect.Type, index []int, depth int) {
		if visited[t] {
			return
		}
		visited[t] = true
		for i := range t.NumField() {
			sf := t.Field(i)
			tag := sf.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			at := append(slices.Clone(index), i)
			if hasOption(opts, "unknown") {
				fs.fallback = true
				continue
			}
			if hasOption(opts, "inline") || (sf.Anonymous && name == "") {
				inner := sf.Type
				if inner.Kind() == reflect.Pointer {
					inner = inner.Elem()
				}
				switch {
				case inner.Kind() == reflect.Struct:
					walk(inner, at, depth+1)
					continue
				case inner.Kind() == reflect.Map && hasOption(opts, "inline"):
					fs.fallback = true
					continue
				}
			}
			if !sf.IsExported() {
				continue
			}
			tagged := name != ""
			if !tagged {
				name = sf.Name
			}
			candidates[name] = append(candidates[name], field{index: at, typ: sf.Type, depth: depth, tagged: tagged})
		}
	}
	walk(t, nil, 0)
	for name, group := range candidates {
		if f, ok := dominant(group); ok {
			fs.byName[name] = f
		}
	}
	return fs
}

func dominant(group []field) (field, bool) {
	shallowest := slices.MinFunc(group, func(a, b field) int { return a.depth - b.depth }).depth
	var top, tagged []field
	for _, f := range group {
		if f.depth == shallowest {
			top = append(top, f)
			if f.tagged {
				tagged = append(tagged, f)
			}
		}
	}
	switch {
	case len(top) == 1:
		return top[0], true
	case len(tagged) == 1:
		return tagged[0], true
	}
	return field{}, false
}

func hasOption(opts, want string) bool {
	for opt := range strings.SplitSeq(opts, ",") {
		if opt == want {
			return true
		}
	}
	return false
}

// fieldValue follows index from v, or returns the zero Value where v is absent or a nil
// embedded pointer stands in the way.
func fieldValue(v reflect.Value, index []int) reflect.Value {
	if !v.IsValid() {
		return v
	}
	for _, i := range index {
		if v.Kind() == reflect.Pointer {
			if v.IsNil() {
				return reflect.Value{}
			}
			v = v.Elem()
		}
		v = v.Field(i)
	}
	return v
}

func implements(t, iface reflect.Type) bool {
	return t.Implements(iface) || (t.Kind() != reflect.Pointer && reflect.PointerTo(t).Implements(iface))
}

func decodesItself(t reflect.Type) bool {
	return implements(t, reflect.TypeFor[json.Unmarshaler]()) || implements(t, reflect.TypeFor[encoding.TextUnmarshaler]())
}
