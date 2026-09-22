package std

import (
	"context"
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

// TestValidateModuleRejectsMalformedMCPTools covers the agent-surface half of
// validation. The Member case is the one worth the test: a tool wrapping a member
// that was renamed still registers and still answers, so nothing observable breaks
// and only this refusal says the catalog stopped deriving from the descriptor.
func TestValidateModuleRejectsMalformedMCPTools(t *testing.T) {
	base := Method{Name: "look", Doc: "d", Impl: covImplStrStr, Args: []Arg{{Name: "s", Type: TypeString}}, Returns: []Ret{{Type: TypeString}}}
	tests := []struct {
		name string
		tool MCPTool
		want string
	}{
		{"member must exist", MCPTool{Name: "m_a", Doc: "d", Member: "nosuch"}, `member "nosuch" is not a method or namespace`},
		{"name is required", MCPTool{Doc: "d"}, "empty Name"},
		{"doc is required", MCPTool{Name: "m_a"}, "empty Doc"},
		{
			"param type must be a schema scalar",
			MCPTool{Name: "m_a", Doc: "d", Params: []MCPParam{{Name: "p", Type: TypeStringSlice}}},
			"has no supported JSON schema shape",
		},
		{
			"param names are unique",
			MCPTool{Name: "m_a", Doc: "d", Params: []MCPParam{{Name: "p", Type: TypeString}, {Name: "p", Type: TypeString}}},
			`param "p" declared twice`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateModule(Module{Name: "covmcp", Methods: []Method{base}, MCPTools: []MCPTool{tc.tool}})
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	t.Run("a namespace is a valid member", func(t *testing.T) {
		ns := Namespace{Name: "grp", Methods: []Method{{Name: "one", Doc: "d", Extern: true}}}
		err := ValidateModule(Module{
			Name:       "covmcp",
			Methods:    []Method{base},
			Namespaces: []Namespace{ns},
			MCPTools:   []MCPTool{{Name: "m_a", Doc: "d", Member: "grp"}, {Name: "m_b", Doc: "d", Member: "look"}},
		})
		assert.NoError(t, err)
	})

	t.Run("duplicate tool names are refused", func(t *testing.T) {
		err := ValidateModule(Module{
			Name:     "covmcp",
			MCPTools: []MCPTool{{Name: "m_a", Doc: "d"}, {Name: "m_a", Doc: "d"}},
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `mcp tool "m_a": declared twice`)
	})
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

// TestNoModuleDeclaresFields keeps the host surface to members Buzz can declare.
//
// A Method becomes `export extern fun name() > str` in the generated declarations,
// so the checker knows its type and a caller's mistake is a compile error. A Field
// becomes a plain value on the module map and gets NO declaration at all: Buzz's
// parser accepts `extern` only before `fun` (parser.go), and upstream Buzz declares
// its whole native stdlib the same way, so there is no syntax for an extern value to
// generate. The checker therefore cannot type a Field, and nothing about it is
// checkable.
//
// That is not theoretical. vcs.name and vcs.base were Fields; four of the module's
// own doc-strings described `vcs.name()` with call parens, the docs site, the buzz
// reference and the editor hovers all render from those doc-strings, and a magusfile
// written against them compiled clean and failed at RUNTIME with "str is not
// callable", inside a branch that only executes in CI. Both are Methods now, and
// `vcs.name()` is what the surface both documents and accepts.
//
// The Field machinery is still wired (magus-docs, langservice-manifest, and the
// ModuleFieldEntry boundary type all render it) because removing it would change a
// Buzz-visible introspection shape for no functional gain. This gate is what keeps it
// unused: a constant that cannot be type-checked is not worth the parens it saves.
func TestNoModuleDeclaresFields(t *testing.T) {
	for _, m := range All() {
		assert.Emptyf(t, m.Fields,
			"module %q declares Fields, which generate no extern declaration and so cannot be\n"+
				"type-checked - a caller writing %s\\%s() compiles clean and fails at runtime.\n"+
				"Declare it as a Method returning the value instead.",
			m.Name, m.Name, fieldNames(m))
	}
}

func fieldNames(m Module) string {
	if len(m.Fields) == 0 {
		return "<field>"
	}
	return m.Fields[0].Name
}
