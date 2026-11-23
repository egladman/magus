package emit

import (
	"bytes"
	"fmt"
	"reflect"
	"strings"

	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
	"github.com/egladman/magus/types"
)

// BuzzEnum renders the closed enum or string-backed enum declared by a Go type.
func BuzzEnum(name string, closed bool, cases []buzzgen.EnumCase) []byte {
	var b bytes.Buffer
	backing := "<str>"
	if closed {
		backing = ""
	}
	fmt.Fprintf(&b, "export enum%s %s {\n", backing, name)
	for _, member := range cases {
		fmt.Fprintf(&b, "    %s", member.Name)
		if !closed {
			fmt.Fprintf(&b, " = %q", member.Value)
		}
		fmt.Fprintln(&b, ",")
	}
	fmt.Fprintln(&b, "}")
	return b.Bytes()
}

// NamedStringArgument checks membership when decoding a closed enum.
func NamedStringArgument(w *bytes.Buffer, name, goType string, index int, enum string, cases []buzzgen.EnumCase, closed bool) {
	if !closed {
		fmt.Fprintf(w, "\t\t%s := %s(ffi.String(bzArgs, %d))\n", name, goType, index)
		return
	}
	fmt.Fprintf(w, "\t\t%s, err := ffi.EnumArgument(bzArgs, %d, %q, []%s{", name, index, enum, goType)
	for _, member := range cases {
		fmt.Fprintf(w, "%q,", member.Value)
	}
	fmt.Fprintln(w, "})")
	fmt.Fprintln(w, "\t\tif err != nil { return vm.Null, ffi.Error(err) }")
}

// ResultArgument returns the payload type of an instantiated types.Result.
func ResultArgument(t reflect.Type) (reflect.Type, bool) {
	if t.PkgPath() != reflect.TypeFor[types.Result[any]]().PkgPath() || !strings.HasPrefix(t.Name(), "Result[") {
		return nil, false
	}
	value, ok := t.FieldByName("Value")
	return value.Type, ok
}

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
// declaration order, with buzzgen.MirroredFields' visibility rule. t must be a
// struct.
func ObjectFields(t reflect.Type) []ObjectField {
	fields := buzzgen.MirroredFields(t)
	out := make([]ObjectField, 0, len(fields))
	for _, f := range fields {
		out = append(out, ObjectField{StructField: f.StructField, Selector: f.Selector})
	}
	return out
}
