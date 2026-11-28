package emit

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

const (
	specBlock = "block"
	specRaw   = "raw"
)

// kindName names a value's kind the way a Buzz author does.
func kindName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case types.Template, *types.Template:
		return "a template"
	case string:
		return "a str"
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Bool:
		return "a bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "an int"
	case reflect.Float32, reflect.Float64:
		return "a double"
	case reflect.Slice, reflect.Array:
		return "a list"
	case reflect.Map:
		return "a map"
	}
	return fmt.Sprintf("a %T", v)
}

// stringForm is the `!s` conversion: the text of a str or scalar. A template, list
// or map has none.
func stringForm(v any) (string, bool) {
	if s, ok := v.(string); ok {
		return s, true
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Bool:
		return strconv.FormatBool(rv.Bool()), true
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(rv.Int(), 10), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(rv.Uint(), 10), true
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(rv.Float(), 'g', -1, 64), true
	}
	return "", false
}

// encoder applies a language's encodings to one value at a time.
type encoder struct {
	patterns map[string]*regexp.Regexp
}

func newEncoder() *encoder {
	return &encoder{patterns: map[string]*regexp.Regexp{}}
}

func (e *encoder) pattern(src string) (*regexp.Regexp, error) {
	if re, ok := e.patterns[src]; ok {
		return re, nil
	}
	re, err := regexp.Compile(fmt.Sprintf("^(?:%s)$", src))
	if err != nil {
		return nil, err
	}
	e.patterns[src] = re
	return re, nil
}

// encode writes v as a token of the encoding's kind. A non-empty message is the
// refusal, under the code returned beside it.
func (e *encoder) encode(enc spells.Encoding, v string) (string, types.DiagnosticCode, string) {
	if !utf8.ValidString(v) {
		return "", types.EmitRefusedValue, fmt.Sprintf("the value is not valid UTF-8, so :%s refuses it", enc.Name)
	}
	if enc.Pattern != "" {
		re, err := e.pattern(enc.Pattern)
		if err != nil {
			return "", types.EmitUnknownEncoding, fmt.Sprintf("encoding %q declares a pattern that does not compile: %v", enc.Name, err)
		}
		if !re.MatchString(v) {
			return "", types.EmitRefusedValue, fmt.Sprintf("%q does not match what :%s accepts (%s)", v, enc.Name, enc.Pattern)
		}
	}
	for _, r := range enc.Reserved {
		if v == r {
			return "", types.EmitRefusedValue, fmt.Sprintf("%q is reserved, so :%s refuses it", v, enc.Name)
		}
	}
	var b strings.Builder
	b.WriteString(enc.Open)
	for i := 0; i < len(v); {
		if esc, ok := matchEscape(enc.Escapes, v[i:]); ok {
			b.WriteString(esc.To)
			i += len(esc.Text)
			continue
		}
		r, size := utf8.DecodeRuneInString(v[i:])
		if r < 0x20 || r == 0x7f {
			if enc.Control == "" {
				return "", types.EmitRefusedValue, fmt.Sprintf("the value holds control character U+%04X, which :%s cannot write", r, enc.Name)
			}
			b.WriteString(controlText(enc.Control, byte(r)))
		} else {
			b.WriteString(v[i : i+size])
		}
		i += size
	}
	b.WriteString(enc.Close)
	return b.String(), "", ""
}

func matchEscape(escapes []spells.Escape, rest string) (spells.Escape, bool) {
	for _, esc := range escapes {
		if esc.Text != "" && strings.HasPrefix(rest, esc.Text) {
			return esc, true
		}
	}
	return spells.Escape{}, false
}

// controlText fills the one placeholder an Encoding.Control holds with c's digits.
func controlText(control string, c byte) string {
	for _, p := range []struct{ holder, format string }{
		{"{hex2}", "%02x"}, {"{hex4}", "%04x"}, {"{oct3}", "%03o"},
	} {
		if strings.Contains(control, p.holder) {
			return strings.Replace(control, p.holder, fmt.Sprintf(p.format, c), 1)
		}
	}
	return control
}
