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
	"strings"
	"time"

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
// defaults to. It is injected for the same reason Namer is - which named-string Go
// types have a registered Buzz enum is the host's registry, not a property the Go
// type carries itself.
type EnumType func(reflect.Type) (name, zero string, ok bool)

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
	// TimeAsString mirrors time.Time as an RFC3339 `str` rather than descending into
	// its fields. Every known host encodes a timestamp that way, so it defaults on;
	// set it false only if yours genuinely hands Buzz a structured time.
	TimeAsString bool
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
// Buzz field with its mapped type and zero-value default.
//
// It emits the declaration alone, with no generated-file banner, so a caller bundling
// many mirrors into one module source carries one header instead of repeating it per
// type.
func ObjectDecl(name string, rt reflect.Type, opts Options) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "export object %s {\n", name)
	if err := renderFields(&b, rt, opts); err != nil {
		return nil, err
	}
	fmt.Fprintln(&b, "}")
	return b.Bytes(), nil
}

// renderFields writes one Buzz field line per exported field of rt, INLINING the fields
// of an embedded struct rather than nesting it under the embedded type's name.
//
// Inlining is not a style preference: encoding/json promotes an anonymous field's keys
// to the outer object, so nesting here would make the Buzz mirror and the JSON encoding
// of the same value disagree about their shape.
func renderFields(b *bytes.Buffer, rt reflect.Type, opts Options) error {
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() || f.Tag.Get("buzz") == "-" {
			continue
		}
		if f.Anonymous && f.Type.Kind() == reflect.Struct && f.Tag.Get("buzz") == "" {
			if err := renderFields(b, f.Type, opts); err != nil {
				return err
			}
			continue
		}
		bt, def, err := FieldType(f.Type, opts)
		if err != nil {
			return fmt.Errorf("field %s: %w", f.Name, err)
		}
		fmt.Fprintf(b, "    %s: %s = %s,\n", FieldName(f), bt, def)
	}
	return nil
}

// FieldType maps a Go type to its Buzz type name and zero-value default.
func FieldType(t reflect.Type, opts Options) (typeName, zero string, err error) {
	// Checked ahead of the Kind switch because a Duration IS an Int64: left to the
	// integer case it would mirror as a bare `int`, handing a caller a nanosecond count
	// with nothing in the type or the value saying so. It crosses as its own string form
	// ("1.5s") for the same reason a timestamp does - the unit travels WITH the value.
	if t == reflect.TypeOf(time.Duration(0)) {
		return "str", `""`, nil
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
		return elem + "?", "null", nil
	case reflect.Array, reflect.Slice:
		// A fixed-size array carries no length information Buzz could enforce, so it
		// mirrors exactly as a slice does.
		elem, _, eerr := FieldType(t.Elem(), opts)
		if eerr != nil {
			return "", "", eerr
		}
		return "[" + elem + "]", "[]", nil
	case reflect.Struct:
		if opts.TimeAsString && t == reflect.TypeOf(time.Time{}) {
			return "str", `""`, nil
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
		return "{" + keyT + ": " + valT + "}", "{}", nil
	default:
		return "", "", fmt.Errorf("unsupported Go type %s", t)
	}
}

// FieldName returns the Buzz field name for a Go struct field. An explicit `buzz:"name"`
// tag wins (needed for initialisms and renames - OK -> ok); otherwise the leading
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
	name := f.Tag.Get("buzz")
	if name == "" {
		name = strings.ToLower(f.Name[:1]) + f.Name[1:]
	}
	if buzz.IsReservedIdent(name) || token.IsKeyword(name) {
		return `@"` + name + `"`
	}
	return name
}
