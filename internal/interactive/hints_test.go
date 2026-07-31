package interactive

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSuggestNearest_Exact(t *testing.T) {
	got := SuggestNearest("run", []string{"run", "list", "doctor"})
	assert.Equal(t, "run", got)
}

func TestSuggestNearest_Typo(t *testing.T) {
	// One transposition / substitution away.
	got := SuggestNearest("rnu", []string{"run", "list", "doctor"})
	assert.Equal(t, "run", got)
}

func TestSuggestNearest_TooFar(t *testing.T) {
	// "zzzz" is too far from any real subcommand to suggest.
	got := SuggestNearest("zzzz", []string{"run", "list", "doctor"})
	assert.Empty(t, got)
}

func TestSuggestNearest_LongerThreshold(t *testing.T) {
	// "selfupdate" is 1 deletion away from "self-update" (8+ chars
	// allows threshold 3, but distance is 1).
	got := SuggestNearest("selfupdate", []string{"self-update", "doctor", "run"})
	assert.Equal(t, "self-update", got)
}

func TestEmit_DefaultOn(t *testing.T) {
	var buf bytes.Buffer
	Emit(&buf, "try `magus run` instead")
	assert.True(t, strings.HasPrefix(buf.String(), "hint: "), "Emit output = %q; want prefix %q", buf.String(), "hint: ")
}

func TestEmit_SetHintsEnabledFalse(t *testing.T) {
	SetHintsEnabled(false)
	t.Cleanup(func() { SetHintsEnabled(true) })
	var buf bytes.Buffer
	Emit(&buf, "try `magus run` instead")
	assert.Zero(t, buf.Len(), "Emit wrote %q with hints disabled; want nothing", buf.String())
}

func TestEmit_SetHintsEnabledTrue(t *testing.T) {
	SetHintsEnabled(true)
	var buf bytes.Buffer
	Emit(&buf, "try `magus run` instead")
	assert.True(t, strings.HasPrefix(buf.String(), "hint: "), "Emit output = %q; want prefix %q", buf.String(), "hint: ")
}

// TestSuggestNearestIsCaseInsensitive pins the fix for a suggestion that failed
// exactly where it was most needed. Project paths resolve exactly (they are
// filesystem paths), so `magus run build API` misses project `api` - and before
// this, API->api scored three substitutions against a threshold of two, so the
// user got "unknown project" with no suggestion at all. Case differences are not
// typos, but the suggestion must still come back in its real casing to be
// copy-pasteable.
func TestSuggestNearestIsCaseInsensitive(t *testing.T) {
	cands := []string{"api", "web/studio", "docs", "console"}
	assert.Equal(t, "api", SuggestNearest("API", cands), "all-caps must still find api")
	assert.Equal(t, "api", SuggestNearest("Api", cands))
	assert.Equal(t, "docs", SuggestNearest("DOCS", cands))
	assert.Equal(t, "api", SuggestNearest("apo", cands), "ordinary typos still work")
	assert.Equal(t, "console", SuggestNearest("consle", cands))
	assert.Empty(t, SuggestNearest("zzzzzz", cands), "nothing close enough stays unsuggested")
}
