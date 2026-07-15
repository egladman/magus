package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTarget(t *testing.T) {
	got, err := ParseTarget("build") // bare target
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "build"}, got)

	got, err = ParseTarget("lint:read")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "lint", Charms: []string{"read"}}, got)

	got, err = ParseTarget("lint:read,strict")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "lint", Charms: []string{"read", "strict"}}, got)

	got, err = ParseTarget("format:write")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "format", Charms: []string{"write"}}, got)

	// Project is positional now; this is target=api charm=build.
	got, err = ParseTarget("api:build")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "api", Charms: []string{"build"}}, got)
}

func TestParseTarget_NormalizesName(t *testing.T) {
	for _, in := range []string{"go-build", "go_build", "goBuild"} {
		got, err := ParseTarget(in)
		require.NoError(t, err)
		assert.Equalf(t, Target{Name: "go-build"}, got, "ParseTarget(%q)", in)
	}
}

func TestParseTarget_Errors(t *testing.T) {
	invalid := []string{
		"lint:",           // empty charm
		"lint:read,",      // empty charm in list
		"web/studio:test", // '/' not allowed in target
		"go::lint",        // '::' not a target char (spell filter stripped earlier)
		"",                // empty string
	}
	for _, in := range invalid {
		_, err := ParseTarget(in)
		assert.Errorf(t, err, "ParseTarget(%q) should error", in)
	}
}

func TestValidateTargetName(t *testing.T) {
	for _, n := range []string{"build", "test", "lint-fix", "gen_2", "ABC123", "a"} {
		assert.NoErrorf(t, ValidateTargetName(n), "ValidateTargetName(%q)", n)
	}
	for _, n := range []string{"", "lint:read", "go::lint", "foo@bar", "web/studio", "build prod", "test.unit"} {
		assert.Errorf(t, ValidateTargetName(n), "ValidateTargetName(%q) should error", n)
	}
}

func TestDefaultTargetNameNormalizer(t *testing.T) {
	norm := DefaultTargetNameNormalizer
	assert.Equal(t, "go-build", norm.NormalizeTargetName("go_build"))
	assert.Equal(t, "go-build", norm.NormalizeTargetName("goBuild"))
	assert.Equal(t, "go-build", norm.NormalizeTargetName("go-build"))
	assert.Equal(t, "build", norm.NormalizeTargetName("build"))
	assert.Equal(t, "image-build-static", norm.NormalizeTargetName("image_build_static"))
}

func TestKebabCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"FooBar", "foo-bar"},
		{"fooBarBaz", "foo-bar-baz"},
		{"HTTPServer", "http-server"},
		{"fmt", "fmt"},
		{"build2", "build-2"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, kebabCase(c.in), "kebabCase(%q)", c.in)
	}
}

func TestValidateCharmName(t *testing.T) {
	for _, n := range []string{"read", "write", "strict", "ci-only", "x"} {
		assert.NoErrorf(t, ValidateCharmName(n), "ValidateCharmName(%q)", n)
	}
	for _, n := range []string{"", "read:write", "a b", "fast@v2"} {
		assert.Errorf(t, ValidateCharmName(n), "ValidateCharmName(%q) should error", n)
	}
}
