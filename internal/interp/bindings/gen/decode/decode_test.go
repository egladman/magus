package decode

import (
	"reflect"
	"testing"

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

func stringValue(s string) vm.Value { return vm.StringValue(s) }

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
		return record("name", stringValue("go"), "syntax", record("comments", comments))
	}

	t.Run("a wrong kind names the field path", func(t *testing.T) {
		comments := record("blockComments", vm.ListValue([]vm.Value{
			block(stringValue("/*"), stringValue("*/")),
			block(stringValue("/*"), vm.IntValue(1)),
		}))
		_, err := DecodeLanguage(lang(comments))
		require.EqualError(t, err, "language.syntax.comments.blockComments[1].close: want str, got int")
	})

	t.Run("a wrong container kind names the Buzz type", func(t *testing.T) {
		_, err := DecodeLanguage(record("extensions", stringValue(".go")))
		require.EqualError(t, err, "language.extensions: want [str], got str")
	})

	t.Run("a record where a scalar belongs", func(t *testing.T) {
		_, err := DecodeLanguage(stringValue("go"))
		require.EqualError(t, err, "language: want Language, got str")
	})

	t.Run("an unknown member is refused", func(t *testing.T) {
		_, err := DecodeLanguage(record("name", stringValue("go"), "extension", vm.ListValue(nil)))
		require.EqualError(t, err, `language: Language has no member "extension"`)
	})

	t.Run("a null where a value belongs is a wrong kind", func(t *testing.T) {
		_, err := DecodeLanguage(record("name", vm.Null))
		require.EqualError(t, err, "language.name: want str, got null")
	})

	t.Run("an absent or null optional stays nil", func(t *testing.T) {
		got, err := DecodeLanguage(record("name", stringValue("go")))
		require.NoError(t, err)
		assert.Equal(t, spells.Language{Name: "go"}, got)

		got, err = DecodeLanguage(record("name", stringValue("go"), "syntax", vm.Null))
		require.NoError(t, err)
		assert.Nil(t, got.Syntax)

		got, err = DecodeLanguage(lang(vm.Null))
		require.NoError(t, err)
		require.NotNil(t, got.Syntax)
		assert.Nil(t, got.Syntax.Comments)
	})

	t.Run("nested records decode in full", func(t *testing.T) {
		comments := record(
			"lineComments", vm.ListValue([]vm.Value{stringValue("//")}),
			"blockComments", vm.ListValue([]vm.Value{block(stringValue("/*"), stringValue("*/"))}),
			"quotes", vm.ListValue([]vm.Value{record("open", stringValue("`"), "close", stringValue("`"), "ignoreEscape", vm.True)}),
		)
		got, err := DecodeLanguage(lang(comments))
		require.NoError(t, err)
		assert.Equal(t, spells.Language{Name: "go", Syntax: &spells.Syntax{Comments: &spells.CommentSyntax{
			LineComments:  []string{"//"},
			BlockComments: []spells.CommentBlock{{Open: "/*", Close: "*/"}},
			Quotes:        []spells.Quote{{Open: "`", Close: "`", IgnoreEscape: true}},
		}}}, got)
	})
}

func strings(ss ...string) vm.Value {
	items := make([]vm.Value, len(ss))
	for i, s := range ss {
		items[i] = stringValue(s)
	}
	return vm.ListValue(items)
}

func TestDecodeCommand(t *testing.T) {
	t.Run("a command decodes in full", func(t *testing.T) {
		got, err := DecodeCommand(record("bin", stringValue("go"), "args", strings("vet", "./...")))
		require.NoError(t, err)
		assert.Equal(t, spells.Command{Bin: "go", Args: []string{"vet", "./..."}}, got)
	})

	t.Run("an unknown member is refused", func(t *testing.T) {
		_, err := DecodeCommand(record("bin", stringValue("go"), "argv", strings("vet")))
		require.EqualError(t, err, `command: Command has no member "argv"`)
	})
}

func TestDecodeSandbox(t *testing.T) {
	t.Run("a sandbox decodes in full", func(t *testing.T) {
		allow := record("base", stringValue("xdgData"), "path", stringValue("x"), "mode", stringValue("rw"))
		got, err := DecodeSandbox(record("allow", vm.ListValue([]vm.Value{allow})))
		require.NoError(t, err)
		assert.Equal(t, spells.Sandbox{Allow: []spells.SandboxAllow{
			{Base: "xdgData", Path: "x", Mode: spells.SandboxAccess("rw")},
		}}, got)
	})

	t.Run("an unknown member is refused", func(t *testing.T) {
		_, err := DecodeSandbox(record("allows", vm.ListValue(nil)))
		require.EqualError(t, err, `sandbox: Sandbox has no member "allows"`)
	})
}

func TestDecodeService(t *testing.T) {
	t.Run("a service decodes in full", func(t *testing.T) {
		got, err := DecodeService(record(
			"command", record("bin", stringValue("docker"), "args", strings("run", "postgres:16")),
			"readiness", record("bin", stringValue("pg_isready")),
			"stop", record("bin", stringValue("docker"), "args", strings("stop", "magus-pg")),
		))
		require.NoError(t, err)
		assert.Equal(t, spells.Service{
			Command:   spells.Command{Bin: "docker", Args: []string{"run", "postgres:16"}},
			Readiness: spells.Command{Bin: "pg_isready"},
			Stop:      spells.Command{Bin: "docker", Args: []string{"stop", "magus-pg"}},
		}, got)
	})

	t.Run("an unknown member is refused", func(t *testing.T) {
		_, err := DecodeService(record("command", record("bin", stringValue("docker")), "ready", record("bin", stringValue("true"))))
		require.EqualError(t, err, `service: Service has no member "ready"`)
	})
}
