package pycompat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// Marshal is json.dumps(v, sort_keys=True, ensure_ascii=True) with CPython's
// separators: ", " and ": " on one line when indent is 0, otherwise one item
// per line with ": " and the given indent. Structs are encoded through their
// json tags with the keys sorted, since a dataclass turns into a dict before
// it is dumped; nil slices and maps are empty containers, never null. Floats
// print as repr, so 3.0 keeps its fraction.
func Marshal(v any, indent int) ([]byte, error) {
	var buf bytes.Buffer
	if err := encode(&buf, reflect.ValueOf(v), indent, 0); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func encode(buf *bytes.Buffer, v reflect.Value, indent, depth int) error {
	if !v.IsValid() {
		buf.WriteString("null")
		return nil
	}
	if v.Type() == reflect.TypeFor[Number]() {
		n, _ := v.Interface().(Number)
		buf.WriteString(n.String())
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			buf.WriteString("null")
			return nil
		}
		return encode(buf, v.Elem(), indent, depth)
	case reflect.Bool:
		buf.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		buf.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		buf.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		buf.WriteString(jsonFloat(v.Float()))
	case reflect.String:
		buf.WriteString(quoteASCII(v.String()))
	case reflect.Slice, reflect.Array:
		return encodeList(buf, v, indent, depth)
	case reflect.Map:
		if v.Type().Key().Kind() != reflect.String {
			return fmt.Errorf("pycompat: map key %s is not a string", v.Type().Key())
		}
		keys := v.MapKeys()
		items := make([]item, len(keys))
		for i, k := range keys {
			items[i] = item{k.String(), v.MapIndex(k)}
		}
		return encodeObject(buf, items, indent, depth)
	case reflect.Struct:
		var items []item
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = f.Name
			}
			items = append(items, item{name, v.Field(i)})
		}
		return encodeObject(buf, items, indent, depth)
	default:
		return fmt.Errorf("pycompat: cannot encode %s", v.Type())
	}
	return nil
}

type item struct {
	key   string
	value reflect.Value
}

func jsonFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	return floatRepr(f)
}

func newline(buf *bytes.Buffer, indent, depth int) {
	buf.WriteByte('\n')
	buf.WriteString(strings.Repeat(" ", indent*depth))
}

func encodeList(buf *bytes.Buffer, v reflect.Value, indent, depth int) error {
	if v.Len() == 0 {
		buf.WriteString("[]")
		return nil
	}
	buf.WriteByte('[')
	for i := 0; i < v.Len(); i++ {
		if i > 0 {
			buf.WriteByte(',')
			if indent == 0 {
				buf.WriteByte(' ')
			}
		}
		if indent > 0 {
			newline(buf, indent, depth+1)
		}
		if err := encode(buf, v.Index(i), indent, depth+1); err != nil {
			return err
		}
	}
	if indent > 0 {
		newline(buf, indent, depth)
	}
	buf.WriteByte(']')
	return nil
}

func encodeObject(buf *bytes.Buffer, items []item, indent, depth int) error {
	if len(items) == 0 {
		buf.WriteString("{}")
		return nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	buf.WriteByte('{')
	for i, it := range items {
		if i > 0 {
			buf.WriteByte(',')
			if indent == 0 {
				buf.WriteByte(' ')
			}
		}
		if indent > 0 {
			newline(buf, indent, depth+1)
		}
		buf.WriteString(quoteASCII(it.key))
		buf.WriteString(": ")
		if err := encode(buf, it.value, indent, depth+1); err != nil {
			return err
		}
	}
	if indent > 0 {
		newline(buf, indent, depth)
	}
	buf.WriteByte('}')
	return nil
}

// quoteASCII is json's ensure_ascii string form: everything outside
// printable ASCII becomes a lowercase \uXXXX escape, astral code points as a
// surrogate pair, and the solidus is left alone.
func quoteASCII(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		default:
			switch {
			case r >= ' ' && r <= '~':
				b.WriteRune(r)
			case r > 0xFFFF:
				hi, lo := utf16.EncodeRune(r)
				fmt.Fprintf(&b, `\u%04x\u%04x`, hi, lo)
			default:
				fmt.Fprintf(&b, `\u%04x`, r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

// Unmarshal is json.loads into the shapes Python would hold: map[string]any,
// []any, string, bool, nil, and Number for every numeric literal, so an int
// and a float stay apart through a round trip.
func Unmarshal(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("trailing data after JSON value")
	}
	return convert(v)
}

func convert(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		return parseNumber(x.String())
	case map[string]any:
		for k, item := range x {
			c, err := convert(item)
			if err != nil {
				return nil, err
			}
			x[k] = c
		}
		return x, nil
	case []any:
		for i, item := range x {
			c, err := convert(item)
			if err != nil {
				return nil, err
			}
			x[i] = c
		}
		return x, nil
	}
	return v, nil
}
