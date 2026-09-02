package ward

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testOverride = Override{
	Name:     "magus thing --loud",
	Silences: "switches off the check nobody else can re-run",
	Spelling: `magus thing --loud --reason "<why>"`,
	Records:  "is kept with the verdict",
}

// TestRequireReasonRefusesABareOverride pins the whole point: the switch on its own is
// an error, and the error carries the exact replacement spelling, because the caller it
// breaks is often a script whose author is not reading the docs.
func TestRequireReasonRefusesABareOverride(t *testing.T) {
	t.Parallel()
	err := RequireReason(testOverride, true, "")
	require.Error(t, err, "a bare override was accepted")
	assert.Contains(t, err.Error(), testOverride.Spelling, "the refusal must name the accepted spelling verbatim")
	assert.Contains(t, err.Error(), testOverride.Silences, "the refusal must say what the override switches off")
	assert.Contains(t, err.Error(), testOverride.Records, "a reason nobody keeps is a form field; the refusal says where it lands")

	assert.Error(t, RequireReason(testOverride, true, "  \t "), "whitespace is not prose")
}

// TestRequireReasonAdmitsTheReasonedForm covers both ways there is nothing to refuse:
// prose was given, or the override was never asked for.
func TestRequireReasonAdmitsTheReasonedForm(t *testing.T) {
	t.Parallel()
	assert.NoError(t, RequireReason(testOverride, true, "the generator writes these and no target claims them"))
	assert.NoError(t, RequireReason(testOverride, false, ""), "an override nobody set has nothing to explain")
}
