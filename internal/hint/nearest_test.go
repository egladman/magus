package hint

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"abc", "abc", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"kitten", "sitting", 3},
		{"lib", "libx", 1},
		{"flaw", "lawn", 2},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, Distance(c.a, c.b), "Distance(%q, %q)", c.a, c.b)
		// Edit distance is symmetric.
		assert.Equalf(t, c.want, Distance(c.b, c.a), "Distance(%q, %q) reversed", c.b, c.a)
	}
}

// TestDistanceCountsRunesNotBytes pins the bug that made three of the four
// hand-rolled copies this replaced disagree with the fourth. Each ranged over
// the strings - which yields BYTE offsets with RUNE values - while indexing a
// row sized in bytes, so the outer loop seeded the wrong row cell and the inner
// one read initialization that was never overwritten.
//
// The damage surfaces as an ASYMMETRY, which is why every case is measured both
// ways: dropping the trailing rune off "cafes" with an accent scored 1 deleting
// forwards and 2 deleting backwards, and the CJK case scored 1 against 3.
func TestDistanceCountsRunesNotBytes(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// A trailing deletion out of a multi-byte string: the shape the broken
		// copies got wrong outright.
		{"cafés", "café", 1},
		{"日本語", "日本", 1},
		// One accented rune substituted for its ASCII twin, mid-string.
		{"hello", "héllo", 1},
		{"naïve", "naive", 1},
		// Three-byte runes: a one-character edit is one edit.
		{"日本語", "中本語", 1},
		// A multi-byte insertion is one edit, not the rune's byte width.
		{"ab", "aéb", 1},
		// Two accents are two typos, not the four bytes they encode to.
		{"résumé", "resume", 2},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, Distance(c.a, c.b), "Distance(%q, %q)", c.a, c.b)
		assert.Equalf(t, c.want, Distance(c.b, c.a), "Distance(%q, %q) reversed", c.b, c.a)
	}
}

// TestNearestSuggestsThroughMultibyteInput is the same fix seen from the
// suggestion surface: a byte-counted distance pushed a name one rune away past
// the threshold, so the reader got no suggestion at all.
func TestNearestSuggestsThroughMultibyteInput(t *testing.T) {
	assert.Equal(t, "日本", Nearest("日本語", []string{"日本", "英語"}))
}

func TestNearest_Exact(t *testing.T) {
	assert.Equal(t, "run", Nearest("run", []string{"run", "list", "doctor"}))
}

func TestNearest_Typo(t *testing.T) {
	// One transposition / substitution away.
	assert.Equal(t, "run", Nearest("rnu", []string{"run", "list", "doctor"}))
}

func TestNearest_TooFar(t *testing.T) {
	// "zzzz" is too far from any real subcommand to suggest.
	assert.Empty(t, Nearest("zzzz", []string{"run", "list", "doctor"}))
}

func TestNearest_LongerThreshold(t *testing.T) {
	// "selfupdate" is 1 deletion away from "self-update" (8+ chars
	// allows threshold 3, but distance is 1).
	assert.Equal(t, "self-update", Nearest("selfupdate", []string{"self-update", "doctor", "run"}))
}

// TestNearestIsCaseInsensitive pins the fix for a suggestion that failed
// exactly where it was most needed. Project paths resolve exactly (they are
// filesystem paths), so `magus run build API` misses project `api` - and before
// this, API->api scored three substitutions against a threshold of two, so the
// user got "unknown project" with no suggestion at all. Case differences are not
// typos, but the suggestion must still come back in its real casing to be
// copy-pasteable.
func TestNearestIsCaseInsensitive(t *testing.T) {
	cands := []string{"api", "web/studio", "docs", "console"}
	assert.Equal(t, "api", Nearest("API", cands), "all-caps must still find api")
	assert.Equal(t, "api", Nearest("Api", cands))
	assert.Equal(t, "docs", Nearest("DOCS", cands))
	assert.Equal(t, "api", Nearest("apo", cands), "ordinary typos still work")
	assert.Equal(t, "console", Nearest("consle", cands))
	assert.Empty(t, Nearest("zzzzzz", cands), "nothing close enough stays unsuggested")
}

func TestThreshold(t *testing.T) {
	assert.Equal(t, 2, Threshold("run"))
	assert.Equal(t, 3, Threshold("selfupdate"))
	// Counted in runes, so an 8-rune accented name gets the same tolerance a
	// bare-ASCII one of the same length does.
	assert.Equal(t, 3, Threshold("évaluate"))
}
