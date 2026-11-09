package emit

import (
	"fmt"
	"reflect"
	"strings"
)

// CallbackFuncName names the generated adapter for callback type t, a func
// whose first parameter is a context. The name spells its parameter and
// result types (callbackStringReturnsBool), so two callbacks share an
// adapter only when their types match. ident names a package's type.
func CallbackFuncName(t reflect.Type, ident func(reflect.Type) string) string {
	var b strings.Builder
	b.WriteString("callback")
	for i := 1; i < t.NumIn(); i++ {
		b.WriteString(callbackTypePart(t.In(i), ident))
	}
	if t.NumOut() == 2 {
		b.WriteString("Returns" + callbackTypePart(t.Out(0), ident))
	}
	if t.NumIn() == 1 && t.NumOut() == 1 {
		b.WriteString("Void")
	}
	return b.String()
}

func callbackTypePart(t reflect.Type, ident func(reflect.Type) string) string {
	switch {
	case t.Kind() == reflect.Interface && t.Name() == "":
		return "Any"
	case t.Kind() == reflect.Slice && t.Name() == "":
		return callbackTypePart(t.Elem(), ident) + "Slice"
	case t.Kind() == reflect.Pointer:
		return callbackTypePart(t.Elem(), ident) + "Pointer"
	case t.PkgPath() == "":
		kind := t.Kind().String()
		return strings.ToUpper(kind[:1]) + kind[1:]
	}
	return ident(t)
}

// ObjectField is one field the Buzz object mirror of a Go struct carries.
// Selector reaches it from the struct value: "Schema.Version" for a field
// of an embedded struct.
type ObjectField struct {
	reflect.StructField
	Selector string
}

// ObjectFields lists t's fields the way buzzgen's mirror declares them, in
// declaration order. It skips unexported fields and `buzz:"-"`. An untagged
// embedded struct contributes its own fields inline. t must be a struct.
func ObjectFields(t reflect.Type) []ObjectField {
	var out []ObjectField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() || f.Tag.Get("buzz") == "-" {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Tag.Get("buzz") == "" {
			for _, inner := range ObjectFields(f.Type) {
				inner.Selector = fmt.Sprintf("%s.%s", f.Name, inner.Selector)
				out = append(out, inner)
			}
			continue
		}
		out = append(out, ObjectField{StructField: f, Selector: f.Name})
	}
	return out
}
