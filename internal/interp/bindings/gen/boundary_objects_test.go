package bindgen

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/egladman/magus/internal/interp/bindings/ffi"
	"github.com/egladman/magus/internal/interp/bindings/gen/decode"
	vm "github.com/egladman/magus/libs/gopherbuzz/vm"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The key names a magusfile reads. Pinned per type rather than left to
// TestEveryBoundaryTypeEncodes, which proves only that nothing arrives null: a field
// renamed on the Go side would still encode, under a key nobody annotates for.
//
// These moved here with the encoders. They were assertions about BuzzObject methods in
// package types until the two encoder generators collapsed into this one.
func TestObjectKeys(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  map[string]any
		want map[string]any
	}{
		{
			// serve's staleness stamp reads st["mtime"] and st["size"] by name, and
			// std/examples/fs/stat.buzz once implied a "modTime" key that never existed.
			// Exact equality is what keeps both claims true.
			name: "FileInfo",
			got:  objectMap(t, ObjectFileInfo(types.FileInfo{Size: 42, Mtime: 1000.5, Mode: 0o644, IsDir: true})),
			want: map[string]any{"size": int64(42), "mtime": 1000.5, "mode": int64(0o644), "is_dir": true},
		},
		{
			name: "HTTPResponse",
			got: objectMap(t, ObjectHTTPResponse(types.HTTPResponse{
				Status: 200, Body: "ok", Headers: map[string]string{"Content-Type": "text/plain"},
			})),
			want: map[string]any{
				"status":  int64(200),
				"body":    "ok",
				"headers": map[string]any{"Content-Type": "text/plain"},
			},
		},
		{
			name: "SemverVersion",
			got: objectMap(t, ObjectSemverVersion(types.SemverVersion{
				Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1", Metadata: "build5", Original: "1.2.3-rc1+build5",
			})),
			want: map[string]any{
				"major": int64(1), "minor": int64(2), "patch": int64(3),
				"prerelease": "rc1", "metadata": "build5", "original": "1.2.3-rc1+build5",
			},
		},
		{
			name: "SemverNext",
			got:  objectMap(t, ObjectSemverNext(types.SemverNext{Major: "v1.0.0", Minor: "v0.2.0", Patch: "v0.1.1"})),
			want: map[string]any{"major": "v1.0.0", "minor": "v0.2.0", "patch": "v0.1.1"},
		},
		{
			name: "URL",
			got: objectMap(t, ObjectURL(types.URL{
				Scheme: "https", Host: "example.com", Port: "8443", Path: "/a", Query: "x=1", Fragment: "top",
			})),
			want: map[string]any{
				"scheme": "https", "host": "example.com", "port": "8443",
				"path": "/a", "query": "x=1", "fragment": "top",
			},
		},
		{
			// The nested SemverVersion record and the RFC3339 date formatting in one case.
			name: "Tag",
			got: objectMap(t, ObjectVCSTag(types.VCSTag{
				Name:    "libs/gopherbuzz/v0.1.0",
				Prefix:  "libs/gopherbuzz/",
				Version: types.SemverVersion{Major: 0, Minor: 1, Patch: 0, Original: "0.1.0"},
				Date:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
				ID:      "deadbeef",
			})),
			want: map[string]any{
				"name":   "libs/gopherbuzz/v0.1.0",
				"prefix": "libs/gopherbuzz/",
				"version": map[string]any{
					"major": int64(0), "minor": int64(1), "patch": int64(0),
					"prerelease": "", "metadata": "", "original": "0.1.0",
				},
				"date": "2026-01-02T03:04:05Z",
				"id":   "deadbeef",
			},
		},
		{
			name: "ExecResult",
			got:  objectMap(t, ObjectExecResult(types.ExecResult{Stdout: "out", Stderr: "err", Code: 2, OK: false})),
			want: map[string]any{"stdout": "out", "stderr": "err", "code": int64(2), "ok": false},
		},
		{
			name: "ModuleMethodEntry",
			got: objectMap(t, ObjectModuleMethodEntry(types.ModuleMethodEntry{
				Name: "glob", Doc: "list files", Buzz: "fs.glob(pat)", BuzzStdlib: "glob(pat)",
			})),
			want: map[string]any{"name": "glob", "doc": "list files", "buzz": "fs.glob(pat)", "buzzStdlib": "glob(pat)"},
		},
		{
			name: "ModuleFieldEntry",
			got:  objectMap(t, ObjectModuleFieldEntry(types.ModuleFieldEntry{Name: "name", Type: "string", Doc: "repo name"})),
			want: map[string]any{"name": "name", "type": "string", "doc": "repo name"},
		},
		{
			name: "ModuleEntry",
			got: objectMap(t, ObjectModuleEntry(types.ModuleEntry{
				Name:    "vcs",
				Doc:     "version control",
				Fields:  []types.ModuleFieldEntry{{Name: "name", Type: "string"}},
				Methods: []types.ModuleMethodEntry{{Name: "commit", Buzz: "vcs.commit()"}},
			})),
			want: map[string]any{
				"name":    "vcs",
				"doc":     "version control",
				"fields":  []any{map[string]any{"name": "name", "type": "string", "doc": ""}},
				"methods": []any{map[string]any{"name": "commit", "doc": "", "buzz": "vcs.commit()", "buzzStdlib": ""}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

// A summary ModuleEntry carries its list fields EMPTY, never absent: a magusfile
// iterating entry.fields on a summary must get a list to iterate, not null.
func TestObjectModuleEntryEmptyLists(t *testing.T) {
	assert.Equal(t, map[string]any{
		"name":    "fs",
		"doc":     "",
		"fields":  []any{},
		"methods": []any{},
	}, objectMap(t, ObjectModuleEntry(types.ModuleEntry{Name: "fs"})))
}

// A field the declarations type as an enum crosses as the case the session's resolver
// hands back, never as its str: a str compares unequal to every case, so
// `check.status == CheckStatus.fail` in a magusfile would never match. Asserted on the
// encoder because routing the field through the resolver is the generator's contract.
func TestObjectEnumFieldsCrossAsTheResolversCase(t *testing.T) {
	enums := namingEnums{}
	for _, tc := range []struct {
		name  string
		got   map[string]any
		field string
		want  string
	}{
		{"Check.status", objectMap(t, ObjectCheck(types.Check{Status: types.CheckFail}, enums)), "status", "CheckStatus.fail"},
		{"TargetRun.state", objectMap(t, ObjectStatusTargetRun(types.StatusTargetRun{State: types.TargetRunPassed}, enums)), "state", "TargetRunState.passed"},
		{"ModelTierRequest.kind", objectMap(t, ObjectModelTierRequest(types.ModelTierRequest{Kind: types.JobKindSymbol}, enums)), "kind", "JobKind.symbol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := tc.got[tc.field]
			require.True(t, ok, "field must be present")
			assert.Equal(t, tc.want, got)
		})
	}
}

// namingEnums stands in for a session: it answers each enum field with the enum and
// value it was asked for, so a test reads which resolver call an encoder made.
type namingEnums struct{}

func (namingEnums) Case(enum, value string) vm.Value {
	return vm.StringValue(fmt.Sprintf("%s.%s", enum, value))
}

func (namingEnums) Instance(object string, names []string, fields []vm.Value) vm.Value {
	return ffi.EnumBacking.Instance(object, names, fields)
}

func objectMap(t *testing.T, v vm.Value) map[string]any {
	t.Helper()
	m, ok := ffi.ValueToAny(v).(map[string]any)
	require.True(t, ok, "encoder produced %T, not a map", ffi.ValueToAny(v))
	return m
}

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
			populateInbound(v)
			want := v.Interface()
			got, err = it.Decode(it.Encode(want))
			require.NoError(t, err)
			assert.Equal(t, want, got, "populated value")
		})
	}
}

// populateInbound fills every exported field with a non-zero value, recursing into structs,
// one-element slices, one-entry maps and pointers.
func populateInbound(v reflect.Value) {
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
		populateInbound(v.Index(0))
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		elem := reflect.New(v.Type().Elem()).Elem()
		populateInbound(elem)
		m.SetMapIndex(reflect.ValueOf("k").Convert(v.Type().Key()), elem)
		v.Set(m)
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		populateInbound(v.Elem())
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				populateInbound(v.Field(i))
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
		_, err := decode.DecodeLanguage(lang(comments))
		require.EqualError(t, err, "language.syntax.comments.blockComments[1].close: want str, got int")
	})

	t.Run("a wrong container kind names the Buzz type", func(t *testing.T) {
		_, err := decode.DecodeLanguage(record("extensions", stringValue(".go")))
		require.EqualError(t, err, "language.extensions: want [str], got str")
	})

	t.Run("a record where a scalar belongs", func(t *testing.T) {
		_, err := decode.DecodeLanguage(stringValue("go"))
		require.EqualError(t, err, "language: want Language, got str")
	})

	t.Run("an unknown member is refused", func(t *testing.T) {
		_, err := decode.DecodeLanguage(record("name", stringValue("go"), "extension", vm.ListValue(nil)))
		require.EqualError(t, err, `language: Language has no member "extension"`)
	})

	t.Run("a null where a value belongs is a wrong kind", func(t *testing.T) {
		_, err := decode.DecodeLanguage(record("name", vm.Null))
		require.EqualError(t, err, "language.name: want str, got null")
	})

	t.Run("an absent or null optional stays nil", func(t *testing.T) {
		got, err := decode.DecodeLanguage(record("name", stringValue("go")))
		require.NoError(t, err)
		assert.Equal(t, spells.Language{Name: "go"}, got)

		got, err = decode.DecodeLanguage(record("name", stringValue("go"), "syntax", vm.Null))
		require.NoError(t, err)
		assert.Nil(t, got.Syntax)

		got, err = decode.DecodeLanguage(lang(vm.Null))
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
		got, err := decode.DecodeLanguage(lang(comments))
		require.NoError(t, err)
		assert.Equal(t, spells.Language{Name: "go", Syntax: &spells.Syntax{Comments: &spells.CommentSyntax{
			LineComments:  []string{"//"},
			BlockComments: []spells.CommentBlock{{Open: "/*", Close: "*/"}},
			Quotes:        []spells.Quote{{Open: "`", Close: "`", IgnoreEscape: true}},
		}}}, got)
	})
}

func stringList(ss ...string) vm.Value {
	items := make([]vm.Value, len(ss))
	for i, s := range ss {
		items[i] = stringValue(s)
	}
	return vm.ListValue(items)
}

func TestDecodeCommand(t *testing.T) {
	t.Run("a command decodes in full", func(t *testing.T) {
		got, err := decode.DecodeCommand(record("bin", stringValue("go"), "args", stringList("vet", "./...")))
		require.NoError(t, err)
		assert.Equal(t, spells.Command{Bin: "go", Args: []string{"vet", "./..."}}, got)
	})

	t.Run("an unknown member is refused", func(t *testing.T) {
		_, err := decode.DecodeCommand(record("bin", stringValue("go"), "argv", stringList("vet")))
		require.EqualError(t, err, `command: Command has no member "argv"`)
	})
}

func TestDecodeSandbox(t *testing.T) {
	t.Run("a sandbox decodes in full", func(t *testing.T) {
		allow := record("base", stringValue("xdgData"), "path", stringValue("x"), "mode", stringValue("rw"))
		got, err := decode.DecodeSandbox(record("allow", vm.ListValue([]vm.Value{allow})))
		require.NoError(t, err)
		assert.Equal(t, spells.Sandbox{Allow: []spells.SandboxAllow{
			{Base: "xdgData", Path: "x", Mode: spells.SandboxAccess("rw")},
		}}, got)
	})

	t.Run("an unknown member is refused", func(t *testing.T) {
		_, err := decode.DecodeSandbox(record("allows", vm.ListValue(nil)))
		require.EqualError(t, err, `sandbox: Sandbox has no member "allows"`)
	})
}

func TestDecodeService(t *testing.T) {
	t.Run("a service decodes in full", func(t *testing.T) {
		got, err := decode.DecodeService(record(
			"command", record("bin", stringValue("docker"), "args", stringList("run", "postgres:16")),
			"readiness", record("bin", stringValue("pg_isready")),
			"stop", record("bin", stringValue("docker"), "args", stringList("stop", "magus-pg")),
		))
		require.NoError(t, err)
		assert.Equal(t, spells.Service{
			Command:   spells.Command{Bin: "docker", Args: []string{"run", "postgres:16"}},
			Readiness: spells.Command{Bin: "pg_isready"},
			Stop:      spells.Command{Bin: "docker", Args: []string{"stop", "magus-pg"}},
		}, got)
	})

	t.Run("an unknown member is refused", func(t *testing.T) {
		_, err := decode.DecodeService(record("command", record("bin", stringValue("docker")), "ready", record("bin", stringValue("true"))))
		require.EqualError(t, err, `service: Service has no member "ready"`)
	})
}
