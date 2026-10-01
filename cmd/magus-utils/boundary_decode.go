package main

//go:generate go run . boundarydecode -out ../../internal/interp/bindings/gen/decode/decode.go -list ../../internal/interp/bindings/gen/decode/inbound_list_test.go

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"reflect"
	"slices"
	"time"

	"github.com/egladman/magus/internal/generate/emit"
	"github.com/egladman/magus/libs/gopherbuzz/buzzgen"
)

// inboundTypes names the boundary types Buzz hands TO magus, each of which gets a strict
// generated decoder, Decode<T>(vm.Value) (T, error). It is the counterpart of
// RuntimeObject, which marks the types magus hands to Buzz. Every name must be registered
// in boundaryTypes, and a struct an inbound type carries must be inbound itself, so each
// nested decoder is declared here and round-trip tested like its parent.
//
// An inbound type also gets a Go-to-Buzz encoder (boundaryobjects reads this list), which
// is what lets a test prove Decode(Object(v)) == v for every entry.
var inboundTypes = []string{
	"CommentBlock",
	"Quote",
	"CommentSyntax",
	"Language",
}

// inboundBoundaryTypes resolves inboundTypes against the registry, in list order.
func inboundBoundaryTypes() ([]boundaryType, error) {
	out := make([]boundaryType, 0, len(inboundTypes))
	for _, name := range inboundTypes {
		entry, ok := boundaryTypeNamed(name)
		if !ok {
			return nil, fmt.Errorf("inbound type %s is not registered in boundaryTypes (cmd/magus-utils/boundary_types.go)", name)
		}
		out = append(out, entry)
	}
	return out, nil
}

// runBoundaryDecode emits a strict Buzz-to-Go decoder per inbound type into
// internal/interp/bindings/gen/decode, plus the list of inbound types its round-trip
// test iterates.
//
// The decoders replace walking string keys by hand, where a wrong-typed scalar read as
// absent and decoded to a silent default. Generated from the same registry the mirror
// and the encoder read, a decoder cannot accept a field the mirror does not declare or
// miss one it does.
//
// The decode package imports only vm, spells and types, so internal/spell can import it.
func runBoundaryDecode(args []string) error {
	fs := flag.NewFlagSet("boundarydecode", flag.ExitOnError)
	outPath := fs.String("out", "", "output Go file path for the decoders")
	listPath := fs.String("list", "", "output Go test file path for the inbound type list")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *outPath == "" || *listPath == "" {
		return fmt.Errorf("usage: magus-utils boundarydecode -out <decode.go> -list <inbound_list_test.go>")
	}
	entries, err := inboundBoundaryTypes()
	if err != nil {
		return err
	}

	e := newDecodeEmitter(entries)
	for _, entry := range entries {
		if err := e.emitStruct(entry); err != nil {
			return fmt.Errorf("inbound type %s: %w", entry.Name, err)
		}
	}
	src, err := e.file()
	if err != nil {
		return err
	}
	if err := emit.File(*outPath, src); err != nil {
		return err
	}
	list, err := e.listFile(entries)
	if err != nil {
		return err
	}
	return emit.File(*listPath, list)
}

// decodeEmitter renders decoders the way buzzValueEmitter renders encoders: plain field
// reads, loops and vm accessors, with all the reflection spent at generate time.
type decodeEmitter struct {
	funcs   bytes.Buffer
	inbound map[reflect.Type]string
	enums   map[string]boundaryEnum
	pkgs    map[string]bool
}

func newDecodeEmitter(entries []boundaryType) *decodeEmitter {
	e := &decodeEmitter{inbound: map[reflect.Type]string{}, enums: map[string]boundaryEnum{}, pkgs: map[string]bool{}}
	for _, entry := range entries {
		e.inbound[entry.Type] = entry.Name
	}
	return e
}

func (e *decodeEmitter) qualify(t reflect.Type) string {
	pkg := t.PkgPath()[len("github.com/egladman/magus/"):]
	e.pkgs[pkg] = true
	return pkg + "." + t.Name()
}

func (e *decodeEmitter) emitStruct(entry boundaryType) error {
	t := entry.Type
	goType := e.qualify(t)
	var body bytes.Buffer
	fmt.Fprintf(&body, "// Decode%s decodes a Buzz %s, an object instance or a map, into a %s.\n", t.Name(), entry.Name, goType)
	fmt.Fprintf(&body, "func Decode%s(v vm.Value) (%s, error) {\n", t.Name(), goType)
	fmt.Fprintf(&body, "\treturn decode%s(v, %q)\n}\n\n", t.Name(), buzzgen.LowerFirstWord(entry.Name))

	fmt.Fprintf(&body, "func decode%s(v vm.Value, path string) (%s, error) {\n", t.Name(), goType)
	fmt.Fprintf(&body, "\tvar out %s\n", goType)
	fmt.Fprintln(&body, "\tfields, ok := v.MapView()")
	fmt.Fprintln(&body, "\tif !ok {")
	fmt.Fprintf(&body, "\t\treturn out, mismatch(path, %q, v)\n", entry.Name)
	fmt.Fprintln(&body, "\t}")
	fmt.Fprintln(&body, "\tfor _, key := range fields.MapKeys() {")
	fmt.Fprintln(&body, "\t\titem, _ := fields.MapGet(key)")
	fmt.Fprintln(&body, "\t\tswitch key {")
	for _, bf := range boundaryFields(t) {
		f := bf.StructField
		key := buzzVMFieldName(f)
		fmt.Fprintf(&body, "\t\tcase %q:\n", key)
		if err := e.assign(&body, "item", fmt.Sprintf("path+%q", "."+key), "out."+bf.selector, f.Name, f.Type, "\t\t\t"); err != nil {
			return fmt.Errorf("%s.%s: %w", t.Name(), f.Name, err)
		}
	}
	fmt.Fprintln(&body, "\t\tdefault:")
	fmt.Fprintf(&body, "\t\t\treturn out, unknownMember(path, %q, key)\n", entry.Name)
	fmt.Fprintln(&body, "\t\t}")
	fmt.Fprintln(&body, "\t}")
	fmt.Fprintln(&body, "\treturn out, nil")
	fmt.Fprintln(&body, "}")
	fmt.Fprintln(&body)
	e.funcs.Write(body.Bytes())
	return nil
}

// assign renders statements decoding the vm.Value expression src into the Go lvalue dst.
// path is a Go expression for the field path an error names, and name seeds the
// temporaries, which are named after the field path as buzzValueEmitter's are.
func (e *decodeEmitter) assign(w *bytes.Buffer, src, path, dst, name string, t reflect.Type, indent string) error {
	ret := func(expr string) {
		fmt.Fprintf(w, "%sif err != nil {\n%s\treturn out, err\n%s}\n", indent, indent, indent)
		fmt.Fprintf(w, "%s%s = %s\n", indent, dst, expr)
	}
	if t == reflect.TypeFor[time.Time]() {
		s, at := "s"+name, "at"+name
		fmt.Fprintf(w, "%s%s, err := decodeStr(%s, %s)\n", indent, s, src, path)
		fmt.Fprintf(w, "%sif err != nil {\n%s\treturn out, err\n%s}\n", indent, indent, indent)
		fmt.Fprintf(w, "%sif %s != \"\" {\n", indent, s)
		fmt.Fprintf(w, "%s\t%s, err := time.Parse(time.RFC3339Nano, %s)\n", indent, at, s)
		fmt.Fprintf(w, "%s\tif err != nil {\n%s\t\treturn out, fmt.Errorf(\"%%s: %%w\", %s, err)\n%s\t}\n", indent, indent, path, indent)
		fmt.Fprintf(w, "%s\t%s = %s\n", indent, dst, at)
		fmt.Fprintf(w, "%s}\n", indent)
		e.pkgs["time"] = true
		return nil
	}
	if t == reflect.TypeFor[time.Duration]() {
		return fmt.Errorf("time.Duration has no decoder: the mirror declares it str and the encoder writes int")
	}
	if en, ok := buzzEnum(t); ok {
		e.enums[en.Name] = en
		s := "s" + name
		fmt.Fprintf(w, "%s%s, err := decodeEnum(%s, %s, %q, enum%sValues)\n", indent, s, src, path, en.Name, en.Name)
		ret(e.qualify(t) + "(" + s + ")")
		return nil
	}
	switch t.Kind() {
	case reflect.String:
		s := "s" + name
		fmt.Fprintf(w, "%s%s, err := decodeStr(%s, %s)\n", indent, s, src, path)
		if t != reflect.TypeFor[string]() {
			ret(e.qualify(t) + "(" + s + ")")
		} else {
			ret(s)
		}
	case reflect.Bool:
		b := "b" + name
		fmt.Fprintf(w, "%s%s, err := decodeBool(%s, %s)\n", indent, b, src, path)
		ret(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n := "n" + name
		fmt.Fprintf(w, "%s%s, err := decodeInt(%s, %s)\n", indent, n, src, path)
		ret(e.convert(t, n))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n := "n" + name
		fmt.Fprintf(w, "%s%s, err := decodeUint(%s, %s)\n", indent, n, src, path)
		ret(e.convert(t, n))
	case reflect.Float32, reflect.Float64:
		n := "n" + name
		fmt.Fprintf(w, "%s%s, err := decodeFloat(%s, %s)\n", indent, n, src, path)
		ret(e.convert(t, n))
	case reflect.Struct:
		if _, ok := e.inbound[t]; !ok {
			return fmt.Errorf("%s is not inbound: add %s to inboundTypes (cmd/magus-utils/boundary_decode.go)", t, buzzName(t))
		}
		x := "x" + name
		fmt.Fprintf(w, "%s%s, err := decode%s(%s, %s)\n", indent, x, t.Name(), src, path)
		ret(x)
	case reflect.Pointer:
		x := "opt" + name
		fmt.Fprintf(w, "%sif !%s.IsNull() {\n", indent, src)
		fmt.Fprintf(w, "%s\tvar %s %s\n", indent, x, e.goType(t.Elem()))
		if err := e.assign(w, src, path, x, name, t.Elem(), indent+"\t"); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\t%s = &%s\n", indent, dst, x)
		fmt.Fprintf(w, "%s}\n", indent)
	case reflect.Slice:
		want, err := e.buzzType(t)
		if err != nil {
			return err
		}
		list, items, index, item := "list"+name, "items"+name, "index"+name, "item"+name
		fmt.Fprintf(w, "%s%s, err := decodeList(%s, %s, %q)\n", indent, list, src, path, want)
		fmt.Fprintf(w, "%sif err != nil {\n%s\treturn out, err\n%s}\n", indent, indent, indent)
		fmt.Fprintf(w, "%sif len(%s) > 0 {\n", indent, list)
		fmt.Fprintf(w, "%s\t%s := make(%s, len(%s))\n", indent, items, e.goType(t), list)
		fmt.Fprintf(w, "%s\tfor %s, %s := range %s {\n", indent, index, item, list)
		elemPath := fmt.Sprintf("fmt.Sprintf(\"%%s[%%d]\", %s, %s)", path, index)
		if err := e.assign(w, item, elemPath, items+"["+index+"]", name+"Item", t.Elem(), indent+"\t\t"); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\t}\n", indent)
		fmt.Fprintf(w, "%s\t%s = %s\n", indent, dst, items)
		fmt.Fprintf(w, "%s}\n", indent)
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return fmt.Errorf("map key %s is not a string", t.Key())
		}
		want, err := e.buzzType(t)
		if err != nil {
			return err
		}
		entries, mapped, key, elem, value := "entries"+name, "mapped"+name, "key"+name, "elem"+name, "value"+name
		fmt.Fprintf(w, "%s%s, err := decodeMap(%s, %s, %q)\n", indent, entries, src, path, want)
		fmt.Fprintf(w, "%sif err != nil {\n%s\treturn out, err\n%s}\n", indent, indent, indent)
		fmt.Fprintf(w, "%sif keys := %s.MapKeys(); len(keys) > 0 {\n", indent, entries)
		fmt.Fprintf(w, "%s\t%s := make(%s, len(keys))\n", indent, mapped, e.goType(t))
		fmt.Fprintf(w, "%s\tfor _, %s := range keys {\n", indent, key)
		fmt.Fprintf(w, "%s\t\t%s, _ := %s.MapGet(%s)\n", indent, elem, entries, key)
		fmt.Fprintf(w, "%s\t\tvar %s %s\n", indent, value, e.goType(t.Elem()))
		elemPath := fmt.Sprintf("fmt.Sprintf(\"%%s[%%q]\", %s, %s)", path, key)
		if err := e.assign(w, elem, elemPath, value, name+"Value", t.Elem(), indent+"\t\t"); err != nil {
			return err
		}
		fmt.Fprintf(w, "%s\t\t%s[%s] = %s\n", indent, mapped, key, value)
		fmt.Fprintf(w, "%s\t}\n", indent)
		fmt.Fprintf(w, "%s\t%s = %s\n", indent, dst, mapped)
		fmt.Fprintf(w, "%s}\n", indent)
	default:
		return fmt.Errorf("unsupported field type %s", t)
	}
	return nil
}

// convert renders n as t, omitting the conversion when n already has t's type, which the
// unconvert linter would otherwise flag.
func (e *decodeEmitter) convert(t reflect.Type, n string) string {
	switch t {
	case reflect.TypeFor[int64](), reflect.TypeFor[uint64](), reflect.TypeFor[float64]():
		return n
	}
	return e.goType(t) + "(" + n + ")"
}

// goType renders t as the generated file spells it.
func (e *decodeEmitter) goType(t reflect.Type) string {
	if t.Name() != "" {
		if t.PkgPath() == "" {
			return t.Name()
		}
		if t.PkgPath() == "time" {
			e.pkgs["time"] = true
			return "time." + t.Name()
		}
		return e.qualify(t)
	}
	switch t.Kind() {
	case reflect.Pointer:
		return "*" + e.goType(t.Elem())
	case reflect.Slice:
		return "[]" + e.goType(t.Elem())
	case reflect.Map:
		return "map[" + e.goType(t.Key()) + "]" + e.goType(t.Elem())
	}
	return t.String()
}

// buzzType names t as the mirror declares it, for the "want" half of an error.
func (e *decodeEmitter) buzzType(t reflect.Type) (string, error) {
	name, _, err := buzzgen.FieldType(t, mirrorOptions())
	return name, err
}

func (e *decodeEmitter) file() ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintln(&b, "// Code generated by magus-utils boundarydecode. DO NOT EDIT.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "// Package decode holds the strict Buzz-to-Go decoders for the boundary types Buzz")
	fmt.Fprintln(&b, "// hands to magus, one Decode<T>(vm.Value) (T, error) per type cmd/magus-utils lists in")
	fmt.Fprintln(&b, "// inboundTypes, and the spell contract's signature table.")
	fmt.Fprintln(&b, "//")
	fmt.Fprintln(&b, "// Strict means every mismatch is an error naming the field path")
	fmt.Fprintln(&b, "// (`language.comments.blockComments[1].close: want str, got int`): a wrong kind, a member")
	fmt.Fprintln(&b, "// the type does not declare, an enum string outside its cases. An absent member keeps")
	fmt.Fprintln(&b, "// its zero value, a null optional stays nil, and an empty list or map decodes to nil, so")
	fmt.Fprintln(&b, "// Decode(Object(v)) == v holds for every value, zero included.")
	fmt.Fprintln(&b, "//")
	fmt.Fprintln(&b, "// It imports only vm, spells and types, so internal/spell can import it.")
	fmt.Fprintln(&b, "package decode")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "import (")
	fmt.Fprintln(&b, `	"fmt"`)
	if e.pkgs["time"] {
		fmt.Fprintln(&b, `	"time"`)
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `	vm "github.com/egladman/magus/libs/gopherbuzz/vm"`)
	if e.pkgs["spells"] {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/spells"`)
	}
	if e.pkgs["types"] {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/types"`)
	}
	fmt.Fprintln(&b, ")")
	fmt.Fprintln(&b)
	b.Write(e.funcs.Bytes())

	names := make([]string, 0, len(e.enums))
	for name := range e.enums {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		fmt.Fprintf(&b, "var enum%sValues = []string{", name)
		for _, c := range e.enums[name].Cases {
			fmt.Fprintf(&b, "%q, ", c.Value)
		}
		fmt.Fprintln(&b, "}")
	}
	b.WriteString(decodeHelpers)

	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt: %w\n--- source ---\n%s", err, b.String())
	}
	return out, nil
}

// listFile renders the inbound type list the round-trip test iterates, each entry paired
// with its encoder and decoder, plus every enum's cases so the test can populate an enum
// field with a value the decoder accepts.
func (e *decodeEmitter) listFile(entries []boundaryType) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintln(&b, "// Code generated by magus-utils boundarydecode. DO NOT EDIT.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "package decode_test")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "import (")
	fmt.Fprintln(&b, `	"reflect"`)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, `	"github.com/egladman/magus/internal/interp/bindings/gen"`)
	fmt.Fprintln(&b, `	"github.com/egladman/magus/internal/interp/bindings/gen/decode"`)
	fmt.Fprintln(&b, `	vm "github.com/egladman/magus/libs/gopherbuzz/vm"`)
	if e.pkgs["spells"] {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/spells"`)
	}
	// e.pkgs is exact here as well: every struct the decoders reach is an inbound entry,
	// and every enum they reach is listed below.
	if e.pkgs["types"] {
		fmt.Fprintln(&b, `	"github.com/egladman/magus/types"`)
	}
	fmt.Fprintln(&b, ")")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "var inboundTypes = []inboundType{")
	for _, entry := range entries {
		t := entry.Type
		q := e.qualify(t)
		fmt.Fprintf(&b, "\t{Name: %q, Zero: %s{}, Encode: func(v any) vm.Value { return gen.Object%s(v.(%s)) }, Decode: func(v vm.Value) (any, error) { return decode.Decode%s(v) }},\n",
			entry.Name, q, t.Name(), q, t.Name())
	}
	fmt.Fprintln(&b, "}")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "var enumCases = map[reflect.Type][]string{")
	names := make([]string, 0, len(e.enums))
	for name := range e.enums {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		en := e.enums[name]
		fmt.Fprintf(&b, "\treflect.TypeFor[%s](): {", e.qualify(en.Type))
		for _, c := range en.Cases {
			fmt.Fprintf(&b, "%q, ", c.Value)
		}
		fmt.Fprintln(&b, "},")
	}
	fmt.Fprintln(&b, "}")

	out, err := format.Source(b.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt: %w\n--- source ---\n%s", err, b.String())
	}
	return out, nil
}

// decodeHelpers are the leaf readers every generated decoder calls. Each takes the path
// so the error it returns names the field.
const decodeHelpers = `
// mismatch names an object by its type, which says more than "object".
func mismatch(path, want string, got vm.Value) error {
	kind := got.Kind()
	if name := got.ObjectTypeName(); name != "" {
		kind = name
	}
	return fmt.Errorf("%s: want %s, got %s", path, want, kind)
}

func unknownMember(path, typ, key string) error {
	return fmt.Errorf("%s: %s has no member %q", path, typ, key)
}

func decodeStr(v vm.Value, path string) (string, error) {
	if !v.IsStr() {
		return "", mismatch(path, "str", v)
	}
	return v.AsString(), nil
}

// decodeEnum accepts an enum case or its backing string: a spell writes the case, and
// the encoder writes the string.
func decodeEnum(v vm.Value, path, typ string, cases []string) (string, error) {
	if backing, ok := v.EnumValue(); ok {
		v = backing
	}
	if !v.IsStr() {
		return "", mismatch(path, typ, v)
	}
	s := v.AsString()
	for _, c := range cases {
		if s == c {
			return s, nil
		}
	}
	return "", fmt.Errorf("%s: %q is not a %s case", path, s, typ)
}

func decodeBool(v vm.Value, path string) (bool, error) {
	if !v.IsBool() {
		return false, mismatch(path, "bool", v)
	}
	return v.AsBool(), nil
}

func decodeInt(v vm.Value, path string) (int64, error) {
	if !v.IsInt() {
		return 0, mismatch(path, "int", v)
	}
	return v.AsInt(), nil
}

func decodeUint(v vm.Value, path string) (uint64, error) {
	if !v.IsInt() {
		return 0, mismatch(path, "int", v)
	}
	n := v.AsInt()
	if n < 0 {
		return 0, fmt.Errorf("%s: want a non-negative int, got %d", path, n)
	}
	return uint64(n), nil
}

// decodeFloat widens an int, which Buzz writes for a whole-number double literal.
func decodeFloat(v vm.Value, path string) (float64, error) {
	switch {
	case v.IsFloat():
		return v.AsFloat(), nil
	case v.IsInt():
		return float64(v.AsInt()), nil
	}
	return 0, mismatch(path, "double", v)
}

func decodeList(v vm.Value, path, want string) ([]vm.Value, error) {
	if !v.IsList() {
		return nil, mismatch(path, want, v)
	}
	return v.ListItems(), nil
}

// decodeMap refuses an object instance: a {str: T} field takes a map, and an object's
// members are not entries.
func decodeMap(v vm.Value, path, want string) (vm.Value, error) {
	if !v.IsMap() {
		return vm.Null, mismatch(path, want, v)
	}
	return v, nil
}
`
