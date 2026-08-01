package std

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
