package std

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The covImpl* functions below are Method Impls and Field Resolvers of every shape
// validateMethod and validateField accept or reject. They are package-level rather
// than closures because implName and implPackage read the runtime function name,
// which a closure spells as ".funcN".

func covImplStrStr(_ context.Context, _ string) (string, error) { return "", nil }

func covImplVariadic(_ context.Context, _ ...string) (string, error) { return "", nil }

func covImplNoCtx(_ string) (string, error) { return "", nil }

func covImplNoArgs() error { return nil }

func covImplNoReturn(_ context.Context) {}

func covImplNoError(_ context.Context) string { return "" }

func covImplTwoStrings(_ context.Context) (string, string) { return "", "" }

func covResolverCtx(_ context.Context) (string, error) { return "", nil }

func covResolverBare() (string, error) { return "", nil }

// TestValidateModuleAcceptsWellFormedDeclarations covers the shapes Register lets
// through: a plain method, a variadic one, an Extern with no Impl, a namespace of
// Externs, and both Field Resolver arities.
func TestValidateModuleAcceptsWellFormedDeclarations(t *testing.T) {
	tests := []struct {
		name string
		mod  Module
	}{
		{
			name: "method with one arg and one return",
			mod: Module{Name: "covok", Methods: []Method{{
				Name: "f", Args: []Arg{{Name: "a", Type: TypeString}},
				Returns: []Ret{{Type: TypeString}}, Impl: covImplStrStr,
			}}},
		},
		{
			name: "variadic method",
			mod: Module{Name: "covok", Methods: []Method{{
				Name: "f", Args: []Arg{{Name: "parts", Type: TypeString, Variadic: true}},
				Returns: []Ret{{Type: TypeString}}, Impl: covImplVariadic,
			}}},
		},
		{
			name: "extern method carries no Impl",
			mod:  Module{Name: "covok", Methods: []Method{{Name: "f", Extern: true}}},
		},
		{
			name: "namespace of externs",
			mod: Module{Name: "covok", Namespaces: []Namespace{{
				Name: "ns", Methods: []Method{{Name: "f", Extern: true}},
			}}},
		},
		{
			name: "field resolver taking a context",
			mod:  Module{Name: "covok", Fields: []Field{{Name: "v", Type: TypeString, Resolver: covResolverCtx}}},
		},
		{
			name: "field resolver taking nothing",
			mod:  Module{Name: "covok", Fields: []Field{{Name: "v", Type: TypeString, Resolver: covResolverBare}}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.NoError(t, ValidateModule(tc.mod))
		})
	}
}

// TestValidateModuleRejectsMismatchedDeclarations walks every rejection
// validateModule can produce. Each case asserts the message names WHICH member is
// wrong as well as what is wrong with it: the error surfaces from a package init(),
// where the member name is the only thing locating the mistake.
func TestValidateModuleRejectsMismatchedDeclarations(t *testing.T) {
	method := func(m Method) Module { return Module{Name: "covbad", Methods: []Method{m}} }
	field := func(f Field) Module { return Module{Name: "covbad", Fields: []Field{f}} }

	tests := []struct {
		name string
		mod  Module
		want string
	}{
		{
			name: "extern carrying an Impl",
			mod:  method(Method{Name: "f", Extern: true, Impl: covImplStrStr}),
			want: "method is Extern but carries an Impl",
		},
		{
			name: "nil Impl",
			mod:  method(Method{Name: "f"}),
			want: "method Impl must not be nil",
		},
		{
			name: "Impl is not a function",
			mod:  method(Method{Name: "f", Impl: 42}),
			want: "method Impl must be a function, got int",
		},
		{
			name: "Impl takes no arguments at all",
			mod:  method(Method{Name: "f", Impl: covImplNoArgs}),
			want: "method Impl must take context.Context as first arg",
		},
		{
			name: "Impl first arg is not a context",
			mod:  method(Method{Name: "f", Args: []Arg{{Name: "a", Type: TypeString}}, Returns: []Ret{{Type: TypeString}}, Impl: covImplNoCtx}),
			want: "method Impl first arg must be context.Context, got string",
		},
		{
			name: "declared variadic but Impl is not",
			mod:  method(Method{Name: "f", Args: []Arg{{Name: "a", Type: TypeString, Variadic: true}}, Returns: []Ret{{Type: TypeString}}, Impl: covImplStrStr}),
			want: "declaration says variadic but Impl is not variadic",
		},
		{
			name: "Impl is variadic but the declaration is not",
			mod:  method(Method{Name: "f", Args: []Arg{{Name: "a", Type: TypeString}}, Returns: []Ret{{Type: TypeString}}, Impl: covImplVariadic}),
			want: "method Impl is variadic but declaration has no variadic arg",
		},
		{
			name: "arg count disagrees",
			mod:  method(Method{Name: "f", Returns: []Ret{{Type: TypeString}}, Impl: covImplStrStr}),
			want: "method Impl takes 2 args, declaration has 1 (incl. ctx)",
		},
		{
			name: "Impl returns nothing",
			mod:  method(Method{Name: "f", Impl: covImplNoReturn}),
			want: "method Impl must return error as last value",
		},
		{
			name: "last return is not an error",
			mod:  method(Method{Name: "f", Impl: covImplNoError}),
			want: "method Impl last return must be error, got string",
		},
		{
			name: "return count disagrees",
			mod:  method(Method{Name: "f", Args: []Arg{{Name: "a", Type: TypeString}}, Impl: covImplStrStr}),
			want: "method Impl has 1 non-error returns, declaration has 0",
		},
		{
			name: "namespace with no methods",
			mod:  Module{Name: "covbad", Namespaces: []Namespace{{Name: "ns"}}},
			want: `namespace "ns" declares no methods`,
		},
		{
			name: "namespace method that is not Extern",
			mod: Module{Name: "covbad", Namespaces: []Namespace{{
				Name: "ns", Methods: []Method{{Name: "f", Args: []Arg{{Name: "a", Type: TypeString}}, Returns: []Ret{{Type: TypeString}}, Impl: covImplStrStr}},
			}}},
			want: `namespace "ns" method "f" must be Extern`,
		},
		{
			name: "namespace method that is Extern and carries an Impl",
			mod: Module{Name: "covbad", Namespaces: []Namespace{{
				Name: "ns", Methods: []Method{{Name: "f", Extern: true, Impl: covImplStrStr}},
			}}},
			want: `namespace "ns" method "f": method is Extern but carries an Impl`,
		},
		{
			name: "nil field resolver",
			mod:  field(Field{Name: "v", Type: TypeString}),
			want: `field "v": field Resolver must not be nil`,
		},
		{
			name: "field resolver is not a function",
			mod:  field(Field{Name: "v", Type: TypeString, Resolver: "nope"}),
			want: "field Resolver must be a function, got string",
		},
		{
			name: "field resolver takes two arguments",
			mod:  field(Field{Name: "v", Type: TypeString, Resolver: covImplStrStr}),
			want: "field Resolver must take 0 or 1 args (ctx), got 2",
		},
		{
			name: "field resolver's single argument is not a context",
			mod:  field(Field{Name: "v", Type: TypeString, Resolver: covImplNoCtx}),
			want: "field Resolver single arg must be context.Context, got string",
		},
		{
			name: "field resolver returns one value",
			mod:  field(Field{Name: "v", Type: TypeString, Resolver: covImplNoError}),
			want: "field Resolver must return (T, error), got 1 returns",
		},
		{
			name: "field resolver's second return is not an error",
			mod:  field(Field{Name: "v", Type: TypeString, Resolver: covImplTwoStrings}),
			want: "field Resolver second return must be error, got string",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateModule(tc.mod)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestRegisterPanicsRatherThanStoringABadModule covers both of Register's refusals.
// Neither case reaches the registry, so this leaves the global module set untouched
// - which matters, since nothing unregisters.
func TestRegisterPanicsRatherThanStoringABadModule(t *testing.T) {
	assert.PanicsWithValue(t, `host: duplicate module registration: "uuid"`, func() {
		Register(Module{Name: "uuid"})
	}, "a second module under a live name must not silently replace it")

	assert.Panics(t, func() {
		Register(Module{Name: "zzz-cov-never-registered", Methods: []Method{{Name: "f"}}})
	}, "a method with no Impl and no Extern must not reach the registry")

	_, ok := Get("zzz-cov-never-registered")
	assert.False(t, ok, "the rejected module must not be stored")
}

func TestGetAndAllReadTheRegistry(t *testing.T) {
	got, ok := Get("uuid")
	require.True(t, ok, "uuid registers from its own init")
	assert.Equal(t, "uuid", got.Name)

	_, ok = Get("zzz-cov-no-such-module")
	assert.False(t, ok)

	all := All()
	require.NotEmpty(t, all)
	names := make([]string, 0, len(all))
	for _, m := range all {
		names = append(names, m.Name)
	}
	assert.Contains(t, names, "uuid")
	assert.Contains(t, names, "env")
}

func TestModuleImportPath(t *testing.T) {
	assert.Equal(t, "encoding/json", Module{Name: "json", Path: "encoding/json"}.ImportPath(),
		"a module with a Path is imported by the full path, not the short name")
	assert.Equal(t, "fs", Module{Name: "fs"}.ImportPath(),
		"no Path means the name is the import spelling")
}

func TestTypeTagGoType(t *testing.T) {
	for _, tc := range []struct {
		tag  TypeTag
		want string
	}{
		{TypeString, "string"},
		{TypeInt, "int"},
		{TypeIndex, "int"},
		{TypeFloat, "float64"},
		{TypeBool, "bool"},
		{TypeStringSlice, "[]string"},
		{TypeFloatSlice, "[]float64"},
		{TypeByteSlice, "[]byte"},
		{TypeStringSliceSlice, "[][]string"},
		{TypeStringMapMap, "map[string]map[string]string"},
		{TypeStringMap, "map[string]string"},
		{TypeAnyMap, "map[string]any"},
		{TypeFunc, "Callback"},
		{TypeAny, "any"},
		{TypeInvalid, "<invalid>"},
		{TypeTag(9999), "<invalid>"},
	} {
		assert.Equalf(t, tc.want, tc.tag.GoType(), "TypeTag(%d).GoType()", int(tc.tag))
	}
}

// covArrow is signature.go's return separator, surrounding spaces included.
const covArrow = " -> "

// TestBuzzSignature pins the call form docs and `magus describe module` render.
func TestBuzzSignature(t *testing.T) {
	mod := Module{Name: "env"}

	for _, tc := range []struct {
		name   string
		method Method
		want   string
	}{
		{
			name:   "snake_case name camelCases, types render the returns",
			method: Method{Name: "get_or", Args: []Arg{{Name: "name", Type: TypeString}, {Name: "def", Type: TypeString}}, Returns: []Ret{{Type: TypeString}}},
			want:   "env\\getOr(name, def)" + covArrow + "string",
		},
		{
			name:   "several returns are comma-joined",
			method: Method{Name: "lookup", Args: []Arg{{Name: "name", Type: TypeString}}, Returns: []Ret{{Type: TypeString}, {Type: TypeBool}}},
			want:   "env\\lookup(name)" + covArrow + "string, bool",
		},
		{
			name:   "no declared return means no suffix",
			method: Method{Name: "set", Args: []Arg{{Name: "name", Type: TypeString}, {Name: "value", Type: TypeString}}},
			want:   "env\\set(name, value)",
		},
		{
			name:   "BuzzName overrides the camelCase derivation",
			method: Method{Name: "has_charm", BuzzName: "has_charm", Args: []Arg{{Name: "name", Type: TypeString}}, Returns: []Ret{{Type: TypeBool}}},
			want:   "env\\has_charm(name)" + covArrow + "bool",
		},
		{
			name:   "a variadic arg trails dots and is never also bracketed",
			method: Method{Name: "join", Args: []Arg{{Name: "parts", Type: TypeString, Variadic: true, Optional: true}}, Returns: []Ret{{Type: TypeString}}},
			want:   "env\\join(parts...)" + covArrow + "string",
		},
		{
			name:   "an optional arg is bracketed",
			method: Method{Name: "arch", Args: []Arg{{Name: "name", Type: TypeString}, {Name: "style", Type: TypeString, Optional: true}}, Returns: []Ret{{Type: TypeString}}},
			want:   "env\\arch(name, [style])" + covArrow + "string",
		},
		{
			name:   "a named return prints its name",
			method: Method{Name: "size", Returns: []Ret{{Name: "width", Type: TypeInt}, {Name: "height", Type: TypeInt}}},
			want:   "env\\size()" + covArrow + "width, height",
		},
		{
			name:   "an object return prints the object, not the map it marshals to",
			method: Method{Name: "stat", Args: []Arg{{Name: "path", Type: TypeString}}, Returns: []Ret{{Type: TypeAnyMap, Object: "FileInfo"}}},
			want:   "env\\stat(path)" + covArrow + "FileInfo",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, BuzzSignature(mod, tc.method))
		})
	}
}

// TestBuzzMethodName: the declared name and the callable one differ, and only the
// callable one resolves.
func TestBuzzMethodName(t *testing.T) {
	assert.Equal(t, "readFile", BuzzMethodName(Method{Name: "read_file"}))
	assert.Equal(t, "glob", BuzzMethodName(Method{Name: "glob"}))
	assert.Equal(t, "has_charm", BuzzMethodName(Method{Name: "has_charm", BuzzName: "has_charm"}))
}

func TestBuzzStdlibEquiv(t *testing.T) {
	got, ok := BuzzStdlibEquiv("fs", "mkdir_all")
	assert.True(t, ok)
	assert.Equal(t, "fs.makeDirectory", got)

	// os.exit, os.sleep and crypto.*_file are deliberately absent: the Buzz stdlib
	// call does something materially different in each case.
	for _, tc := range [][2]string{{"os", "exit"}, {"os", "sleep"}, {"crypto", "sha256_file"}, {"fs", "no_such_method"}} {
		got, ok := BuzzStdlibEquiv(tc[0], tc[1])
		assert.Falsef(t, ok, "BuzzStdlibEquiv(%q, %q)", tc[0], tc[1])
		assert.Empty(t, got)
	}
}

// TestMethodSource resolves an Impl back to the file and line it is defined at,
// which is what links a generated doc page to the code.
func TestMethodSource(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller")
	repoRoot := filepath.Dir(filepath.Dir(thisFile))

	file, line := MethodSource(Method{Impl: FsGlob}, repoRoot)
	if file == "" {
		t.Skip("built with -trimpath: no absolute path to relativize")
	}
	assert.Equal(t, "std/fs.go", file)
	assert.Positive(t, line)

	// A file outside repoRoot relativizes to a "..' path and is reported as unknown
	// rather than as a path escaping the tree.
	outside, outsideLine := MethodSource(Method{Impl: FsGlob}, t.TempDir())
	assert.Empty(t, outside)
	assert.Zero(t, outsideLine)

	for _, impl := range []any{nil, 42} {
		f, l := MethodSource(Method{Impl: impl}, repoRoot)
		assert.Emptyf(t, f, "MethodSource(Impl=%v)", impl)
		assert.Zerof(t, l, "MethodSource(Impl=%v)", impl)
	}
}

func TestFieldFuncName(t *testing.T) {
	assert.Equal(t, "covResolverCtx", FieldFuncName(Field{Resolver: covResolverCtx}))
	assert.Empty(t, FieldFuncName(Field{}))
}

func TestFieldResolverTakesCtx(t *testing.T) {
	assert.True(t, FieldResolverTakesCtx(Field{Resolver: covResolverCtx}))
	assert.False(t, FieldResolverTakesCtx(Field{Resolver: covResolverBare}))
	assert.False(t, FieldResolverTakesCtx(Field{}))
}

// TestImplPackage covers the qualifier codegen writes at the call site. It stops
// assuming every Impl lives in package std, so the assertion is on the pair rather
// than on the identifier alone.
func TestImplPackage(t *testing.T) {
	path, ident := MethodImplPackage(Method{Impl: FsGlob})
	assert.True(t, strings.HasSuffix(path, "/std"), "import path %q should end at the std package", path)
	assert.Equal(t, "std", ident)

	path, ident = FieldResolverPackage(Field{Resolver: covResolverCtx})
	assert.True(t, strings.HasSuffix(path, "/std"), "import path %q should end at the std package", path)
	assert.Equal(t, "std", ident)

	for _, impl := range []any{nil, 42} {
		p, i := MethodImplPackage(Method{Impl: impl})
		assert.Emptyf(t, p, "MethodImplPackage(Impl=%v) path", impl)
		assert.Emptyf(t, i, "MethodImplPackage(Impl=%v) ident", impl)
	}
	p, i := FieldResolverPackage(Field{})
	assert.Empty(t, p)
	assert.Empty(t, i)
}
