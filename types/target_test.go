package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTargetString(t *testing.T) {
	assert.Equal(t, "api:build", Target{Path: "api", Name: "build"}.String())
	// An empty path (all-projects target) still renders the leading colon.
	assert.Equal(t, ":test", Target{Name: "test"}.String())
}

func TestExecResultBuzzObject(t *testing.T) {
	r := ExecResult{Stdout: "out", Stderr: "err", Code: 2, OK: false}
	want := BuzzObject{
		"stdout": "out",
		"stderr": "err",
		"code":   2,
		"ok":     false,
	}
	assert.Equal(t, want, r.BuzzObject())
}

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

// Declared records the raw spelling ONLY when normalization rewrote it, so a
// caller can react to a non-canonical name without the parser printing anything.
func TestParseTarget_NormalizesName(t *testing.T) {
	canonical, err := ParseTarget("go-build")
	require.NoError(t, err)
	assert.Equal(t, Target{Name: "go-build"}, canonical, "canonical input carries no provenance")

	for _, in := range []string{"go_build", "goBuild"} {
		got, err := ParseTarget(in)
		require.NoErrorf(t, err, "ParseTarget(%q)", in)
		assert.Equalf(t, Target{Name: "go-build", Declared: in}, got, "ParseTarget(%q)", in)
	}

	// Charms carry the same provenance, and only the rewritten ones.
	mixed, err := ParseTarget("lint:rw,NoCache")
	require.NoError(t, err)
	assert.Equal(t, Target{
		Name:           "lint",
		Charms:         []string{"rw", "no-cache"},
		DeclaredCharms: []string{"NoCache"},
	}, mixed, "only the charm that was rewritten is recorded")
}

func TestParseTarget_Errors(t *testing.T) {
	invalid := []string{
		"lint:",           // empty charm
		"lint:read,",      // empty charm in list
		"web/studio:test", // '/' not allowed in target
		"golang::lint",    // '::' not a target char (spell filter stripped earlier)
		"",                // empty string
	}
	for _, in := range invalid {
		_, err := ParseTarget(in)
		assert.Errorf(t, err, "ParseTarget(%q) should error", in)
	}
}

// TestParseTarget_ColonFormExplainsGrammar pins the fix for a misdirecting
// error: typing a "path:target" pair (the classic footgun once documented as
// the describe target grammar) fails target-name validation on the part
// before ':' with no hint that ':' means something else entirely. The error
// must now say so.
func TestParseTarget_ColonFormExplainsGrammar(t *testing.T) {
	_, err := ParseTarget(".:build")
	require.Error(t, err)
	assert.ErrorContains(t, err, `target name "."`)
	assert.ErrorContains(t, err, "introduces a charm list")
	assert.ErrorContains(t, err, "never a project path")

	// No colon: the plain ValidateTargetName message carries no such caveat -
	// there is no charm-vs-path ambiguity to explain.
	_, err = ParseTarget("web/studio")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "charm list")
}

func TestValidateTargetName(t *testing.T) {
	for _, n := range []string{"build", "test", "lint-fix", "gen_2", "ABC123", "a"} {
		assert.NoErrorf(t, ValidateTargetName(n), "ValidateTargetName(%q)", n)
	}
	for _, n := range []string{"", "lint:read", "golang::lint", "foo@bar", "web/studio", "build prod", "test.unit"} {
		assert.Errorf(t, ValidateTargetName(n), "ValidateTargetName(%q) should error", n)
	}
}

// Normalize is the one canonicalizer for every entity kind, so the same spellings
// must converge whether they name a target, a charm or a spell op.
//
// These cases ARE the table published in docs/concepts/targets.md ("Name
// normalization"). Documented behavior that nothing asserts is documented
// intent, not documented behavior - if a row here changes, that page is wrong
// and this test is the thing that says so.
func TestNormalize(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"go-build", "go-build"},      // already canonical
		{"go_build", "go-build"},      // '_' is a delimiter
		{"goBuild", "go-build"},       // camelCase boundary
		{"GoBuild", "go-build"},       // PascalCase boundary
		{"Go_Build", "go-build"},      // both at once
		{"HTTPServer", "http-server"}, // acronym breaks before its last letter
		{"build2", "build-2"},         // letter/digit boundary
		{"go--build", "go-build"},     // delimiter runs collapse
		{"build", "build"},            // single word untouched
		{"image_build_static", "image-build-static"},
		{"no_cache", "no-cache"}, // the charm cases
		{"NoCache", "no-cache"},
		{"WRITE", "write"},
	} {
		assert.Equalf(t, c.want, Normalize(c.in), "Normalize(%q)", c.in)
	}
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
