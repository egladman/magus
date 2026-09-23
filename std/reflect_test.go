package std

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImplName(t *testing.T) {
	topName := implName(Method{Impl: FsGlob})
	assert.Equal(t, "std.FsGlob", topName)

	var recv methodReceiver
	mvName := implName(Method{Impl: recv.Method})
	assert.Contains(t, mvName, "methodReceiver")
	assert.True(t, strings.HasSuffix(mvName, "-fm"), "method value name %q should end in -fm", mvName)

	assert.Contains(t, implName(Method{Impl: makeClosure()}), ".func")
	assert.Contains(t, implName(Method{Impl: genericFunc[int]}), "[...]")
	assert.Equal(t, "", implName(Method{Impl: nil}))
	assert.Equal(t, "", implName(Method{Impl: 42}))
}

func TestBareName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"std.FsGlob", "FsGlob"}, {"std.FsGlob-fm", "FsGlob-fm"},
		{"std.glob.func1", "glob.func1"}, {"std.GenericFunc[...]", "GenericFunc[...]"},
		{"NoQualifier", "NoQualifier"}, {"", ""},
	} {
		assert.Equalf(t, tc.want, bareName(tc.in), "bareName(%q)", tc.in)
	}
}

func TestMethodFuncName(t *testing.T) {
	assert.Equal(t, "FsGlob", MethodFuncName(Method{Impl: FsGlob}))
	assert.Equal(t, "", MethodFuncName(Method{Impl: nil}))
}

func TestCamelCase(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"glob", "glob"}, {"read_file", "readFile"}, {"has_charm", "hasCharm"},
		{"a_b_c", "aBC"}, {"hmac_sha256_hex", "hmacSha256Hex"},
		{"trailing_", "trailing"}, {"double__under", "doubleUnder"}, {"", ""},
	} {
		assert.Equalf(t, tc.want, CamelCase(tc.in), "CamelCase(%q)", tc.in)
	}
}

type methodReceiver struct{}

func (methodReceiver) Method() {}

func makeClosure() func() { return func() {} }

func genericFunc[T any]() {}

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
