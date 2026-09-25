package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSortsNamesFromPrefixes(t *testing.T) {
	a, err := Parse([]string{"GOFLAGS", "MISE_*", "LC_*", "npm_config_*"})
	require.NoError(t, err)
	assert.Equal(t, Allowlist{Names: []string{"GOFLAGS"}, Prefixes: []string{"MISE_*", "LC_*", "npm_config_*"}}, a)

	a, err = Parse(nil)
	require.NoError(t, err)
	assert.Equal(t, Allowlist{}, a)
}

// Every malformed pattern is an error naming it, never a pattern quietly skipped.
// GO* is the case that set the minimum: it matches GOOGLE_APPLICATION_CREDENTIALS.
func TestParseRefusesMalformedPatterns(t *testing.T) {
	for _, bad := range []string{"*", "GO*", "M*", "X_*", "AWS*", "*_TOKEN", "A*B*", "FOO*BAR", "", "A=B"} {
		_, err := Parse([]string{"PATH", bad})
		require.ErrorIs(t, err, ErrInvalidPattern, bad)
		assert.ErrorContains(t, err, `"`+bad+`"`, "the error names the pattern")
	}
}

func TestParseReportsEveryBadPattern(t *testing.T) {
	_, err := Parse([]string{"GO*", "OK_*", "*"})
	assert.ErrorContains(t, err, `"GO*"`)
	assert.ErrorContains(t, err, `"*"`)
}

// Defence in depth: an Allowlist built by hand with a bare "*" still leaks nothing.
func TestScrubIgnoresABareWildcard(t *testing.T) {
	a := Allowlist{Names: []string{"PATH"}, Prefixes: []string{"*"}}
	kept, dropped := a.Scrub([]string{"PATH=/usr/bin", "AWS_SECRET_ACCESS_KEY=topsecret", "GITHUB_TOKEN=ghp_xxx", "malformed"})
	assert.Equal(t, []string{"PATH=/usr/bin"}, kept)
	assert.Equal(t, []string{"AWS_SECRET_ACCESS_KEY", "GITHUB_TOKEN"}, dropped)
}

func TestScrubKeepsAPrefixMatch(t *testing.T) {
	a := Allowlist{Prefixes: []string{"MISE_*"}}
	kept, dropped := a.Scrub([]string{"MISE_DATA_DIR=/x", "AWS_SECRET=y"})
	assert.Equal(t, []string{"MISE_DATA_DIR=/x"}, kept)
	assert.Equal(t, []string{"AWS_SECRET"}, dropped)
}

func TestDefaultAllowWithholdsTheMagusSockets(t *testing.T) {
	a := Allowlist{Names: DefaultAllow()}
	assert.True(t, a.Allows("MAGUS_RUN_ID"))
	assert.True(t, a.Allows("PATH"))
	for _, name := range []string{"MAGUS_PROC_SOCKET", "MAGUS_SERVER_ADDRESS", "GITHUB_TOKEN", "AWS_ACCESS_KEY_ID"} {
		assert.False(t, a.Allows(name), name)
	}
}
