package decode_test

import (
	"reflect"
	"testing"

	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	"github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/spells"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// inboundType pairs one inbound boundary type with its generated encoder and decoder.
// inbound_list_test.go is generated from the same list the decoders are, so a type is
// covered here the moment it is marked inbound.
type inboundType struct {
	Name   string
	Zero   any
	Encode func(any) vm.Value
	Decode func(vm.Value) (any, error)
}

// Decode(Object(v)) == v for every inbound type, both zero-valued and with every field
// populated. A field the decoder skips, or reads under another key than the encoder
// writes, comes back zero and fails the populated case.
func TestEveryInboundTypeRoundTrips(t *testing.T) {
	require.NotEmpty(t, inboundTypes)
	for _, it := range inboundTypes {
		rt := reflect.TypeOf(it.Zero)
		t.Run(it.Name, func(t *testing.T) {
			got, err := it.Decode(it.Encode(it.Zero))
			require.NoError(t, err)
			assert.Equal(t, it.Zero, got, "zero value")

			v := reflect.New(rt).Elem()
			populate(v)
			want := v.Interface()
			got, err = it.Decode(it.Encode(want))
			require.NoError(t, err)
			assert.Equal(t, want, got, "populated value")
		})
	}
}

// populate fills every exported field with a non-zero value, recursing into structs,
// one-element slices, one-entry maps and pointers.
func populate(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		if cases, ok := enumCases[v.Type()]; ok {
			v.SetString(cases[len(cases)-1])
			return
		}
		v.SetString("x")
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v.SetInt(1)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v.SetUint(1)
	case reflect.Float32, reflect.Float64:
		v.SetFloat(1.5)
	case reflect.Slice:
		v.Set(reflect.MakeSlice(v.Type(), 1, 1))
		populate(v.Index(0))
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		elem := reflect.New(v.Type().Elem()).Elem()
		populate(elem)
		m.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), elem)
		v.Set(m)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		populate(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				populate(v.Field(i))
			}
		}
	}
}

func str(s string) vm.Value { return vm.StrValue(s) }

func record(kv ...any) vm.Value {
	m := vm.NewMap()
	for i := 0; i < len(kv); i += 2 {
		m.MapSet(kv[i].(string), kv[i+1].(vm.Value))
	}
	return m
}

func TestDecodeIsStrict(t *testing.T) {
	block := func(open, close vm.Value) vm.Value { return record("open", open, "close", close) }
	lang := func(comments vm.Value) vm.Value {
		return record("name", str("go"), "syntax", record("comments", comments))
	}

	t.Run("a wrong kind names the field path", func(t *testing.T) {
		comments := record("blockComments", vm.ListValue([]vm.Value{
			block(str("/*"), str("*/")),
			block(str("/*"), vm.IntValue(1)),
		}))
		_, err := decode.DecodeLanguage(lang(comments))
		require.EqualError(t, err, "language.syntax.comments.blockComments[1].close: want str, got int")
	})

	t.Run("a wrong container kind names the Buzz type", func(t *testing.T) {
		_, err := decode.DecodeLanguage(record("extensions", str(".go")))
		require.EqualError(t, err, "language.extensions: want [str], got str")
	})

	t.Run("a record where a scalar belongs", func(t *testing.T) {
		_, err := decode.DecodeLanguage(str("go"))
		require.EqualError(t, err, "language: want Language, got str")
	})

	t.Run("an unknown member is refused", func(t *testing.T) {
		_, err := decode.DecodeLanguage(record("name", str("go"), "extension", vm.ListValue(nil)))
		require.EqualError(t, err, `language: Language has no member "extension"`)
	})

	t.Run("a null where a value belongs is a wrong kind", func(t *testing.T) {
		_, err := decode.DecodeLanguage(record("name", vm.Null))
		require.EqualError(t, err, "language.name: want str, got null")
	})

	t.Run("an absent or null optional stays nil", func(t *testing.T) {
		got, err := decode.DecodeLanguage(record("name", str("go")))
		require.NoError(t, err)
		assert.Equal(t, spells.Language{Name: "go"}, got)

		got, err = decode.DecodeLanguage(record("name", str("go"), "syntax", vm.Null))
		require.NoError(t, err)
		assert.Nil(t, got.Syntax)

		got, err = decode.DecodeLanguage(lang(vm.Null))
		require.NoError(t, err)
		require.NotNil(t, got.Syntax)
		assert.Nil(t, got.Syntax.Comments)
	})

	t.Run("nested records decode in full", func(t *testing.T) {
		comments := record(
			"lineComments", vm.ListValue([]vm.Value{str("//")}),
			"blockComments", vm.ListValue([]vm.Value{block(str("/*"), str("*/"))}),
			"quotes", vm.ListValue([]vm.Value{record("open", str("`"), "close", str("`"), "ignoreEscape", vm.True)}),
		)
		got, err := decode.DecodeLanguage(lang(comments))
		require.NoError(t, err)
		assert.Equal(t, spells.Language{Name: "go", Syntax: &spells.Syntax{Comments: &spells.CommentSyntax{
			LineComments:  []string{"//"},
			BlockComments: []spells.CommentBlock{{Open: "/*", Close: "*/"}},
			Quotes:        []spells.Quote{{Open: "`", Close: "`", IgnoreEscape: true}},
		}}}, got)
	})
}
