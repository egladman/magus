//go:build !goexperiment.jsonv2

package codec

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"time"
)

// walkLikeV2 is true: the scanner does first what v2 does on decode and encoding/json
// does not.
const walkLikeV2 = true

func version() string { return "v1" }

// marshal writes v through encoding/json, then rewrites the output in v2's form: every
// string re-escaped the v2 way (U+2028 and U+2029 written as typed, a RawMessage's escapes
// normalized), a repeated key refused, and the \ufffd escape encoding/json writes in place
// of an invalid byte refused as invalid UTF-8.
func marshal(v any) ([]byte, error) {
	v, err := encodable(v)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	data := bytes.TrimSuffix(b.Bytes(), []byte{'\n'})
	s := scanner{data: data, canon: true, refuseReplacement: true, out: make([]byte, 0, len(data))}
	if err := s.document(nil, reflect.Value{}); err != nil {
		var syntax *SyntaxError
		if errors.As(err, &syntax) {
			return nil, &EncodeError{Err: syntax.Err}
		}
		return nil, err
	}
	return s.out, nil
}

// encodable refuses v when encoding/json would write it differently from v2, and returns
// it behind a pointer: v2 calls a MarshalJSON with a pointer receiver on any value,
// encoding/json only on one it can address.
func encodable(v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	rv := reflect.ValueOf(v)
	c := newChecker("encode", rv.Type())
	if err := c.check(rv.Type(), ""); err != nil {
		return nil, err
	}
	if err := c.value(rv, "", 0); err != nil {
		return nil, err
	}
	if rv.Kind() == reflect.Pointer {
		return v, nil
	}
	p := reflect.New(rv.Type())
	p.Elem().Set(rv)
	return p.Interface(), nil
}

func unmarshal(data []byte, v any) error {
	t, rv := targetOf(v)
	if err := refuseDecode(t); err != nil {
		return err
	}
	s := scanner{data: data, caseOnly: true, likeV2: true}
	if err := s.document(t, rv); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// unmarshalStrict decodes one value the scanner has read and walked already.
func unmarshalStrict(data []byte, v any) error { return json.Unmarshal(data, v) }

func refuseDecode(t reflect.Type) error {
	if t == nil {
		return nil
	}
	return newChecker("decode", t).check(t, "")
}

type streamDecoder struct{ dec *json.Decoder }

func newStreamDecoder(r io.Reader) streamDecoder {
	return streamDecoder{dec: json.NewDecoder(r)}
}

func (d streamDecoder) decode(v any) error {
	var raw json.RawMessage
	if err := d.dec.Decode(&raw); err != nil {
		return err
	}
	return unmarshal(raw, v)
}

// checker walks a type for what encoding/json reads or writes differently from v2. It
// keeps no state past one call: the walk is over types, not values, so it costs the
// distinct types a value holds.
type checker struct {
	op      string
	root    reflect.Type
	seen    map[reflect.Type]bool
	dynamic map[reflect.Type]bool
}

func newChecker(op string, root reflect.Type) *checker {
	return &checker{op: op, root: root, seen: map[reflect.Type]bool{}, dynamic: map[reflect.Type]bool{}}
}

func (c *checker) refuse(path, reason string) error {
	return &UnsupportedError{Op: c.op, Type: c.root.String(), Field: path, Reason: reason}
}

// ownsCodec reports whether t reads or writes itself in the direction c checks.
func (c *checker) ownsCodec(t reflect.Type) bool {
	if c.op == "encode" {
		return encodesItself(t)
	}
	return decodesItself(t)
}

func (c *checker) check(t reflect.Type, path string) error {
	for {
		if c.ownsCodec(t) {
			return nil
		}
		if t == reflect.TypeFor[time.Duration]() {
			return c.refuse(path, "a bare time.Duration, which encoding/json writes as a number; use codec.Duration")
		}
		if method := c.v2Method(t); method != "" {
			return c.refuse(path, "a "+method+" method, which encoding/json does not call")
		}
		if t.Kind() != reflect.Pointer {
			break
		}
		t = t.Elem()
	}
	if c.seen[t] {
		return nil
	}
	c.seen[t] = true
	switch t.Kind() {
	case reflect.Struct:
		for i := range t.NumField() {
			sf := t.Field(i)
			tag := sf.Tag.Get("json")
			at := joinPath(path, sf.Name)
			if tag == "-" {
				continue
			}
			if !sf.IsExported() && !sf.Anonymous {
				if tag != "" {
					return c.refuse(at, "a json tag on an unexported field, which v2 refuses")
				}
				continue
			}
			_, opts, _ := strings.Cut(tag, ",")
			if reason := c.tagRefusal(sf, opts); reason != "" {
				return c.refuse(at, reason)
			}
			if err := c.check(sf.Type, at); err != nil {
				return err
			}
		}
	case reflect.Map:
		if c.op == "encode" {
			if k := t.Key(); k.Kind() == reflect.String && encodesItself(k) {
				return c.refuse(path, "map keys of a string type with its own MarshalText, which encoding/json ignores")
			}
			if pointerOnlyMarshaler(t.Elem()) {
				return c.refuse(path, "map values whose MarshalJSON or MarshalText has a pointer receiver, which encoding/json does not call on a map value")
			}
		}
		return c.check(t.Elem(), path)
	case reflect.Slice, reflect.Array:
		return c.check(t.Elem(), path)
	}
	return nil
}

// v2Method names a method only v2 calls on t, when t lacks the one encoding/json calls.
func (c *checker) v2Method(t reflect.Type) string {
	name := "UnmarshalJSONFrom"
	if c.op == "encode" {
		name = "MarshalJSONTo"
	}
	if _, ok := t.MethodByName(name); ok {
		return name
	}
	if t.Kind() != reflect.Pointer && t.Kind() != reflect.Interface {
		if _, ok := reflect.PointerTo(t).MethodByName(name); ok {
			return name
		}
	}
	return ""
}

func (c *checker) tagRefusal(sf reflect.StructField, opts string) string {
	switch {
	case hasOption(opts, "unknown"):
		return "an `,unknown` field, which encoding/json treats as a member named " + sf.Name
	case hasOption(opts, "inline") && !sf.Anonymous:
		return "a named `,inline` field, which encoding/json treats as one member"
	case hasOption(opts, "string") && !isNumber(deref(sf.Type).Kind()):
		return "the `,string` option on a " + deref(sf.Type).Kind().String() + ", which v2 applies to numbers alone"
	case c.op == "encode" && hasOption(opts, "omitempty"):
		return omitEmptyRefusal(sf.Type)
	}
	return ""
}

// omitEmptyRefusal says why omitempty on a field of type t can differ: encoding/json
// omits by the Go value and v2 by what it writes (null, "", {} or []), so the two agree
// only on strings, slices, maps, arrays and pointers to what never writes empty.
func omitEmptyRefusal(t reflect.Type) string {
	if encodesItself(t) {
		return "omitempty on a type with its own MarshalJSON or MarshalText; use omitzero"
	}
	switch k := t.Kind(); {
	case k == reflect.Bool || isNumber(k):
		return "omitempty on a " + k.String() + ", which v2 never omits; drop it or use omitzero"
	case k == reflect.Struct:
		return "omitempty on a struct; use omitzero"
	case k == reflect.Interface:
		return "omitempty on an interface; use omitzero"
	case k == reflect.Pointer:
		switch e := t.Elem().Kind(); e {
		case reflect.String, reflect.Slice, reflect.Map, reflect.Struct, reflect.Interface, reflect.Pointer, reflect.Array:
			return "omitempty on a pointer to a " + e.String() + "; use omitzero"
		}
	}
	return ""
}

// value checks the dynamic type of each interface value v holds, which the walk over
// static types cannot see. It descends only where an interface can sit.
func (c *checker) value(v reflect.Value, path string, depth int) error {
	if depth > maxDepth || !v.IsValid() || !c.reachesInterface(v.Type()) {
		return nil
	}
	switch v.Kind() {
	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		e := v.Elem()
		if pointerOnlyMarshaler(e.Type()) {
			return c.refuse(path, "an interface holding a "+e.Type().String()+" whose MarshalJSON or MarshalText has a pointer receiver")
		}
		if err := c.check(e.Type(), path); err != nil {
			return err
		}
		return c.value(e, path, depth+1)
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return c.value(v.Elem(), path, depth+1)
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			sf := t.Field(i)
			if sf.Tag.Get("json") == "-" || (!sf.IsExported() && !sf.Anonymous) {
				continue
			}
			if err := c.value(v.Field(i), joinPath(path, sf.Name), depth+1); err != nil {
				return err
			}
		}
	case reflect.Map:
		for iter := v.MapRange(); iter.Next(); {
			if err := c.value(iter.Value(), path, depth+1); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			if err := c.value(v.Index(i), path, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *checker) reachesInterface(t reflect.Type) bool {
	if r, ok := c.dynamic[t]; ok {
		return r
	}
	r := reaches(t, map[reflect.Type]bool{})
	c.dynamic[t] = r
	return r
}

func reaches(t reflect.Type, visited map[reflect.Type]bool) bool {
	if visited[t] || encodesItself(t) {
		return false
	}
	visited[t] = true
	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return reaches(t.Elem(), visited)
	case reflect.Struct:
		for i := range t.NumField() {
			sf := t.Field(i)
			if sf.Tag.Get("json") == "-" || (!sf.IsExported() && !sf.Anonymous) {
				continue
			}
			if reaches(sf.Type, visited) {
				return true
			}
		}
	}
	return false
}

// pointerOnlyMarshaler reports whether only *t writes itself, which encoding/json calls
// on an addressable value alone.
func pointerOnlyMarshaler(t reflect.Type) bool {
	if t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface {
		return false
	}
	m, tm := reflect.TypeFor[json.Marshaler](), reflect.TypeFor[encoding.TextMarshaler]()
	if t.Implements(m) || t.Implements(tm) {
		return false
	}
	p := reflect.PointerTo(t)
	return p.Implements(m) || p.Implements(tm)
}

func deref(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}

func isNumber(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Complex64, reflect.Complex128:
		return true
	}
	return false
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func encodesItself(t reflect.Type) bool {
	return implements(t, reflect.TypeFor[json.Marshaler]()) || implements(t, reflect.TypeFor[encoding.TextMarshaler]())
}
