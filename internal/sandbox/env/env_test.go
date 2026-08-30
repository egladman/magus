package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestValidateGlobs_RejectsBareWildcard ensures a bare "*" is rejected: it has
// an empty prefix and would otherwise match every variable name, passing the
// entire environment (secrets included) through Scrub.
func TestValidateGlobs_RejectsBareWildcard(t *testing.T) {
	t.Run("bare wildcard", func(t *testing.T) {
		assert.ErrorIs(t, ValidateGlobs([]string{"*"}), ErrInvalidGlob)
	})
	t.Run("bare wildcard among valid", func(t *testing.T) {
		err := ValidateGlobs([]string{"MISE_*", "*"})
		assert.ErrorIs(t, err, ErrInvalidGlob)
		assert.ErrorContains(t, err, `"*"`, "the error names the offending pattern")
	})
	t.Run("valid prefix glob", func(t *testing.T) {
		assert.NoError(t, ValidateGlobs([]string{"MISE_*"}))
	})
	t.Run("valid single-char prefix", func(t *testing.T) {
		assert.NoError(t, ValidateGlobs([]string{"M*"}))
	})
	t.Run("interior wildcard", func(t *testing.T) {
		assert.ErrorContains(t, ValidateGlobs([]string{"A*B*"}), `"A*B*"`)
	})
	t.Run("no wildcard", func(t *testing.T) {
		assert.ErrorContains(t, ValidateGlobs([]string{"PATH"}), `"PATH"`)
	})
	t.Run("empty", func(t *testing.T) {
		assert.NoError(t, ValidateGlobs(nil))
	})
}

// TestValidateGlobs_EmptyStringPatternIsInvalid keeps the case that forced the
// signature: an empty-string glob is invalid, and while the result was a string it
// could not say so - the report and the all-valid sentinel were both "", so a
// caller checking `!= ""` read it as valid.
func TestValidateGlobs_EmptyStringPatternIsInvalid(t *testing.T) {
	assert.ErrorIs(t, ValidateGlobs([]string{""}), ErrInvalidGlob)

	t.Run("empty pattern among valid ones", func(t *testing.T) {
		assert.ErrorIs(t, ValidateGlobs([]string{"MISE_*", ""}), ErrInvalidGlob)
	})
}

// TestScrub_BareWildcardDoesNotLeakEnv is the defence-in-depth check: even if a
// bare "*" glob slips past ValidateGlobs, matchGlobs must not treat it as
// matching everything — that would defeat the secret-stripping allowlist.
func TestScrub_BareWildcardDoesNotLeakEnv(t *testing.T) {
	a := Allowlist{Allow: []string{"PATH"}, Globs: []string{"*"}}
	env := []string{
		"PATH=/usr/bin",
		"AWS_SECRET_ACCESS_KEY=topsecret",
		"GITHUB_TOKEN=ghp_xxx",
	}
	kept, dropped := a.Scrub(env)

	assert.Contains(t, kept, "PATH=/usr/bin", "PATH should be kept (exact allow)")
	assert.NotContains(t, kept, "AWS_SECRET_ACCESS_KEY=topsecret", "bare-wildcard glob leaked secret through Scrub")
	assert.NotContains(t, kept, "GITHUB_TOKEN=ghp_xxx", "bare-wildcard glob leaked secret through Scrub")
	assert.Contains(t, dropped, "AWS_SECRET_ACCESS_KEY", "secrets should be dropped")
	assert.Contains(t, dropped, "GITHUB_TOKEN", "secrets should be dropped")
}

// TestScrub_ValidGlobStillMatches confirms the fix does not break legitimate
// prefix globs.
func TestScrub_ValidGlobStillMatches(t *testing.T) {
	a := Allowlist{Globs: []string{"MISE_*"}}
	kept, _ := a.Scrub([]string{"MISE_DATA_DIR=/x", "AWS_SECRET=y"})
	assert.Contains(t, kept, "MISE_DATA_DIR=/x", "MISE_* glob should keep MISE_DATA_DIR")
	assert.NotContains(t, kept, "AWS_SECRET=y", "MISE_* glob should not keep AWS_SECRET")
}
