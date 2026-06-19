package host

import (
	"strings"
	"testing"

	"github.com/egladman/magus/std"
	"github.com/stretchr/testify/assert"
)

// TestImplName covers the runtime-name parsing that drives checked-in codegen.
// The runtime.FuncForPC().Name() shapes exercised are: a top-level package
// function, a method value (name ends in "-fm"), a closure (name carries a
// ".funcN" suffix), and a generic instantiation (name contains "[...]").
func TestImplName(t *testing.T) {
	// topLevel exercises the ordinary "pkg.Func" shape via a real std function.
	topName := implName(std.Method{Impl: std.FsGlob})
	assert.Equal(t, "std.FsGlob", topName)

	// A method value carries a "-fm" suffix; implName drops the import path but
	// keeps the suffix (bareName strips the qualifier later).
	var recv methodReceiver
	mvName := implName(std.Method{Impl: recv.Method})
	assert.Contains(t, mvName, "methodReceiver")
	assert.True(t, strings.HasSuffix(mvName, "-fm"), "method value name %q should end in -fm", mvName)

	// A closure carries a ".funcN" suffix.
	clName := implName(std.Method{Impl: makeClosure()})
	assert.Contains(t, clName, ".func")

	// A generic instantiation carries "[...]" in its runtime name.
	genName := implName(std.Method{Impl: genericFunc[int]})
	assert.Contains(t, genName, "[...]")

	// Nil and non-func Impls yield "".
	assert.Equal(t, "", implName(std.Method{Impl: nil}))
	assert.Equal(t, "", implName(std.Method{Impl: 42}))
}

// TestBareName pins the "strip everything up to and including the first dot"
// transform across the same shapes implName produces.
func TestBareName(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"std.FsGlob", "FsGlob"},
		{"std.FsGlob-fm", "FsGlob-fm"},
		{"std.glob.func1", "glob.func1"},
		{"std.GenericFunc[...]", "GenericFunc[...]"},
		{"NoQualifier", "NoQualifier"},
		{"", ""},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, bareName(c.in), "bareName(%q)", c.in)
	}
}

// TestMethodFuncName checks the implName→bareName composition that names
// generated trampolines.
func TestMethodFuncName(t *testing.T) {
	assert.Equal(t, "FsGlob", MethodFuncName(std.Method{Impl: std.FsGlob}))
	assert.Equal(t, "", MethodFuncName(std.Method{Impl: nil}))
}

// TestCamelCase pins the snake_case→camelCase transform that is the single
// source of truth for the Buzz map keys.
func TestCamelCase(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"glob", "glob"},
		{"read_file", "readFile"},
		{"has_charm", "hasCharm"},
		{"a_b_c", "aBC"},
		{"hmac_sha256_hex", "hmacSha256Hex"},
		// Trailing/double underscores leave empty segments, which are skipped.
		{"trailing_", "trailing"},
		{"double__under", "doubleUnder"},
		{"", ""},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, CamelCase(c.in), "CamelCase(%q)", c.in)
	}
}

// --- helpers exercising the runtime-name shapes ---

type methodReceiver struct{}

func (methodReceiver) Method() {}

func makeClosure() func() {
	return func() {}
}

func genericFunc[T any]() {}
