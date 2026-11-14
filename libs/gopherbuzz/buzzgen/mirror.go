// Package buzzgen mirrors Go types into Buzz. Given a reflect.Type it renders the
// `export object` declaration a Buzz checker reads, and the Go methods that hand the
// value across at run time.
//
// It lives beside the language rather than in any one host because the knowledge it
// needs is the language's: which identifiers are reserved, how a field name is spelled,
// which Go shapes have a Buzz equivalent. The generator that grew this code originally
// sat in a host program and reached back here for every one of those facts, which is
// the usual sign a boundary is drawn in the wrong place.
//
// It knows nothing about any particular host. A caller supplies a Namer saying what a
// struct type is CALLED in Buzz, because that mapping is the host's registry, not a
// property of the Go type.
package buzzgen

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode"

	buzz "github.com/egladman/magus/libs/gopherbuzz"
	"github.com/egladman/magus/libs/gopherbuzz/token"
)

// Namer resolves a Go struct type to the name its Buzz mirror is declared under.
//
// It is injected rather than derived because the two names routinely differ: a host
// may mirror StatusTargetRun as TargetRun, or ProjectsOutput as Projects. Deriving the
// name from the Go type emits references to objects that were never declared, and the
// failure surfaces at check time in a magusfile rather than here.
type Namer func(reflect.Type) string

// EnumType resolves a Go type to the Buzz enum it mirrors as: the enum's declared
// name, and the zero-case reference (e.g. "VersionComponent.patch") an unset field
// defaults to. It is injected for the same reason Namer is: which named-string Go
// types have a registered Buzz enum is the host's registry, not a property the Go
// type carries itself.
type EnumType func(reflect.Type) (name, zero string, ok bool)

// Required names the Buzz fields of a struct that every value sets.
// Its mirror declares them with no default, so a literal that leaves
// one out fails the check. Which fields those are is the host's contract.
type Required func(reflect.Type) []string

// Options configure a rendering pass.
type Options struct {
	// Namer resolves struct-valued fields to their declared Buzz object names. A nil
	// Namer falls back to the Go type's own name, which is correct only for a host
	// whose two vocabularies agree everywhere.
	Namer Namer
	// EnumType resolves named-string fields to a registered Buzz enum. A nil EnumType
	// mirrors every string-kinded field as bare `str`, which is correct for a host
	// with no enum registry.
	EnumType EnumType
	// Required names the fields declared with no default. A nil
	// Required gives every field its zero value.
	Required Required
	// TimeAsString mirrors time.Time as an RFC3339 `str` rather than descending into
	// its fields. Every known host encodes a timestamp that way, so it defaults on;
	// set it false only if yours genuinely hands Buzz a structured time.
	TimeAsString bool
}

func (o Options) required(t reflect.Type) []string {
	if o.Required == nil || t.Kind() != reflect.Struct {
		return nil
	}
	return o.Required(t)
}

func (o Options) name(t reflect.Type) string {
	if o.Namer == nil {
		return t.Name()
	}
	return o.Namer(t)
}

// DefaultOptions is the conventional configuration: timestamps as strings, names
// derived from the Go type.
func DefaultOptions() Options { return Options{TimeAsString: true} }

// ObjectDecl renders rt as `export object <name> { … }`. Each exported field becomes a
// Buzz field with its mapped type and zero-value default. A field
// opts.Required names has no default; one rt does not declare is an error.
//
// It emits the declaration alone, with no generated-file banner, so a caller bundling
// many mirrors into one module source carries one header instead of repeating it per
// type.
//
// A generic Go struct is mirrored through an instantiation at placeholder types such as
// [T]: Result[T] renders as `export object Result::<T> { … }`, with a field of type T
// declared `T?` because Buzz has no zero value to default a type parameter to.
func ObjectDecl(name string, rt reflect.Type, opts Options) ([]byte, error) {
	var b bytes.Buffer
	if params := TypeParams(rt); len(params) > 0 {
		name += "::<" + strings.Join(params, ", ") + ">"
	}
	fmt.Fprintf(&b, "export object %s {\n", name)
	declared := map[string]bool{}
	if err := renderFields(&b, rt, opts, declared); err != nil {
		return nil, err
	}
	for _, field := range opts.required(rt) {
		if !declared[field] {
			return nil, fmt.Errorf("%s requires field %s, which it does not declare", rt, field)
		}
	}
	fmt.Fprintln(&b, "}")
	return b.Bytes(), nil
}

// T is a placeholder for a generic struct's type parameter. reflect has no type for an
// uninstantiated generic, so a host registers the instantiation at T (Result[buzzgen.T])
// and the mirror declares the parameter T in its place.
type T struct{ typeParam }

// typeParam marks a placeholder. Its method is unexported so only this package can
// declare one.
type typeParam struct{}

func (typeParam) buzzTypeParam() {}

var typeParamType = reflect.TypeFor[interface{ buzzTypeParam() }]()

func isTypeParam(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && t.Implements(typeParamType)
}

// TypeParams returns the placeholder names rt's exported fields reference, directly or
// as an element, in field order and deduplicated. Empty for a non-generic struct.
func TypeParams(rt reflect.Type) []string {
	var out []string
	seen := map[string]bool{}
	var walk func(reflect.Type)
	walk = func(t reflect.Type) {
		for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map {
			t = t.Elem()
		}
		if isTypeParam(t) && !seen[t.Name()] {
			seen[t.Name()] = true
			out = append(out, t.Name())
		}
	}
	for i := 0; i < rt.NumField(); i++ {
		if f := rt.Field(i); f.IsExported() && f.Tag.Get("buzz") != "-" {
			walk(f.Type)
		}
	}
	return out
}

// renderFields writes one Buzz field line per exported field of rt, INLINING the fields
// of an embedded struct rather than nesting it under the embedded type's name.
//
// Inlining is not a style preference: encoding/json promotes an anonymous field's keys
// to the outer object, so nesting here would make the Buzz mirror and the JSON encoding
// of the same value disagree about their shape.
func renderFields(b *bytes.Buffer, rt reflect.Type, opts Options, declared map[string]bool) error {
	required := opts.required(rt)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() || f.Tag.Get("buzz") == "-" {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Tag.Get("buzz") == "" {
			if err := renderFields(b, f.Type, opts, declared); err != nil {
				return err
			}
			continue
		}
		bt, def, err := FieldType(f.Type, opts)
		if err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
		name, member := FieldName(f), memberName(f)
		declared[member] = true
		if slices.Contains(required, member) {
			fmt.Fprintf(b, "    %s: %s,\n", name, bt)
			continue
		}
		// A struct with a required field has no zero literal to default to.
		if f.Type.Kind() == reflect.Struct && len(opts.required(f.Type)) > 0 {
			return fmt.Errorf("field %s: %s requires %s, so %s{} is no default: require %s too",
				f.Name, bt, strings.Join(opts.required(f.Type), ", "), bt, member)
		}
		fmt.Fprintf(b, "    %s: %s = %s,\n", name, bt, def)
	}
	return nil
}

// FieldType maps a Go type to its Buzz type name and zero-value default.
func FieldType(t reflect.Type, opts Options) (typeName, zero string, err error) {
	// Checked ahead of the Kind switch because a Duration IS an Int64: left to the
	// integer case it would mirror as a bare `int`, handing a caller a nanosecond count
	// with nothing in the type or the value saying so. It crosses as its own string form
	// ("1.5s") for the same reason a timestamp does: the unit travels WITH the value.
	if t == reflect.TypeOf(time.Duration(0)) {
		return "str", `""`, nil
	}
	if isTypeParam(t) {
		return t.Name() + "?", "null", nil
	}
	// Checked ahead of the Kind switch, which would see only String: a registered
	// named-string type mirrors as its enum, so a magusfile writes
	// VersionComponent.patch and a typo is a compile error rather than a value that
	// decodes to nothing.
	if opts.EnumType != nil {
		if name, zero, ok := opts.EnumType(t); ok {
			return name, zero, nil
		}
	}
	switch t.Kind() {
	case reflect.String:
		return "str", `""`, nil
	case reflect.Bool:
		return "bool", "false", nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int", "0", nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		// Buzz has no unsigned integer, so an unsigned Go field mirrors as `int`. Safe
		// for every width up to Uint32; a Uint64 above math.MaxInt64 cannot be
		// represented and would wrap.
		return "int", "0", nil
	case reflect.Float32, reflect.Float64:
		return "double", "0.0", nil
	case reflect.Pointer:
		// A pointer is Go's "optional", so it mirrors as the pointed-to type made
		// nullable, defaulting to null rather than to that type's zero. Mirroring it as
		// the bare type would claim a value is always present when nil is exactly what
		// the pointer exists to express.
		elem, _, eerr := FieldType(t.Elem(), opts)
		if eerr != nil {
			return "", "", eerr
		}
		if strings.HasSuffix(elem, "?") {
			return elem, "null", nil
		}
		return elem + "?", "null", nil
	case reflect.Array, reflect.Slice:
		// A fixed-size array carries no length information Buzz could enforce, so it
		// mirrors exactly as a slice does.
		elem, _, eerr := FieldType(t.Elem(), opts)
		if eerr != nil {
			return "", "", eerr
		}
		return fmt.Sprintf("[%s]", elem), "[]", nil
	case reflect.Struct:
		if opts.TimeAsString && t == reflect.TypeOf(time.Time{}) {
			return "str", `""`, nil
		}
		// reflect names an instance only as Object[pkg/path.Arg], so the Buzz
		// spelling is rebuilt from that string.
		if strings.Contains(t.Name(), "[") {
			name, err := genericInstanceName(t.Name())
			if err != nil {
				return "", "", fmt.Errorf("generic instance %s: %w", t, err)
			}
			return name, name + "{}", nil
		}
		// A struct field maps to the Buzz object the host declares for it, which is NOT
		// always the Go type's own name. Resolving through the Namer is what keeps a
		// renamed mirror referenceable; using t.Name() emits a type nothing declared.
		name := opts.name(t)
		return name, name + "{}", nil
	case reflect.Map:
		keyT, _, kerr := FieldType(t.Key(), opts)
		if kerr != nil {
			return "", "", fmt.Errorf("map key: %w", kerr)
		}
		if t.Key().Kind() != reflect.String {
			return "", "", fmt.Errorf("map key %s is not a string", t.Key())
		}
		valT, _, verr := FieldType(t.Elem(), opts)
		if verr != nil {
			return "", "", fmt.Errorf("map value: %w", verr)
		}
		return fmt.Sprintf("{%s: %s}", keyT, valT), "{}", nil
	case reflect.Interface:
		// An interface field holds whatever JSON-shaped value the Go side put there,
		// nil included, so it mirrors as a nullable `any` rather than claiming a type.
		return "any?", "null", nil
	default:
		return "", "", fmt.Errorf("unsupported Go type %s", t)
	}
}

// FieldName returns the Buzz field name for a Go struct field. An explicit `buzz:"name"`
// tag wins (needed for initialisms and renames: OK -> ok); otherwise the leading
// capital is lowercased to match Buzz's camelCase convention.
//
// A name colliding with a reserved word is emitted as a FREE IDENTIFIER (@"type"), which
// is what free identifiers exist for: those words are reserved in binding positions
// only, so `@"type": str` declares the field and `entry.type` still reads it back.
//
// Two distinct lists, and BOTH matter. IsReservedIdent covers the words upstream reserves
// but the lexer treats as plain identifiers; token.IsKeyword covers the ones the lexer
// really does tokenize. A field named `out` hits the first; a field named `in` hits only
// the second, and missing it emits a file that does not parse.
func FieldName(f reflect.StructField) string {
	name := memberName(f)
	if buzz.IsReservedIdent(name) || token.IsKeyword(name) {
		return fmt.Sprintf(`@"%s"`, name)
	}
	return name
}

// memberName is the name a Buzz value carries the field under, before
// FieldName escapes a reserved word for the declaration.
func memberName(f reflect.StructField) string {
	if name := f.Tag.Get("buzz"); name != "" {
		return name
	}
	return LowerFirstWord(f.Name)
}

// LowerFirstWord lowercases a Go field name's first WORD rather than its first rune, so an
// initialism survives the crossing: URL becomes url and HTTPServer becomes httpServer,
// where a naive first-rune fold produces uRL and hTTPServer.
//
// This is the transform Go itself applies in reverse (the commonInitialisms list revive
// and protoc-gen-go's JSON naming both key off). It is a floor, not a replacement for the
// `buzz` tag: the rule catches what nobody thought to tag (ID mirrored as iD until this
// existed) while a tag states the intended name outright, which is what a reader sees
// and what survives someone renaming the Go field.
func LowerFirstWord(name string) string {
	r := []rune(name)
	if len(r) == 0 {
		return name
	}
	n := 0
	for n < len(r) && unicode.IsUpper(r[n]) {
		n++
	}
	switch {
	case n == 0:
		return name
	case n == len(r):
		// All caps, so the whole name is the word: URL, OK, ID.
		return strings.ToLower(name)
	case n == 1:
		return string(unicode.ToLower(r[0])) + string(r[1:])
	default:
		// The last upper rune starts the NEXT word (HTTPServer -> httpServer), so it is
		// the one capital that stays.
		return strings.ToLower(string(r[:n-1])) + string(r[n-1:])
	}
}

var builtinTypeNames = map[string]string{
	"string": "str", "bool": "bool", "float32": "double", "float64": "double",
	"int": "int", "int8": "int", "int16": "int", "int32": "int", "int64": "int",
	"uint": "int", "uint8": "int", "uint16": "int", "uint32": "int", "uint64": "int",
	"any": "any?",
}

// genericInstanceName spells reflect's Object[example.com/types.JobSpec] as
// Object::<JobSpec>. Arguments keep their Go names, because a Namer needs
// types and reflect gives only this string. A map argument is refused.
func genericInstanceName(reflected string) (string, error) {
	open := strings.Index(reflected, "[")
	if open <= 0 || !strings.HasSuffix(reflected, "]") {
		return "", fmt.Errorf("cannot read type arguments from %q", reflected)
	}
	var args []string
	for _, arg := range splitTypeArguments(reflected[open+1 : len(reflected)-1]) {
		buzzArg, err := typeArgumentName(arg)
		if err != nil {
			return "", err
		}
		args = append(args, buzzArg)
	}
	return reflected[:open] + "::<" + strings.Join(args, ", ") + ">", nil
}

func splitTypeArguments(list string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range list {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, list[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, list[start:])
}

func typeArgumentName(arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	switch {
	case strings.HasPrefix(arg, "[]"):
		elem, err := typeArgumentName(arg[2:])
		return "[" + elem + "]", err
	case strings.HasPrefix(arg, "*"):
		elem, err := typeArgumentName(arg[1:])
		if err != nil || strings.HasSuffix(elem, "?") {
			return elem, err
		}
		return elem + "?", nil
	case strings.HasPrefix(arg, "map["):
		return "", fmt.Errorf("type argument %q is a map", arg)
	}
	if open := strings.Index(arg, "["); open > 0 {
		return genericInstanceName(arg[strings.LastIndex(arg[:open], ".")+1:])
	}
	if name, ok := builtinTypeNames[arg]; ok {
		return name, nil
	}
	return arg[strings.LastIndex(arg, ".")+1:], nil
}
