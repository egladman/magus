package spells

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The versions here are the shape ExtractVersion emits (canonical "vX.Y.Z") and the
// bounds are the shape an author writes (no "v", often partial). That mismatch is the
// whole reason normalizeBound exists, so the table exercises it rather than pre-
// normalizing both sides.
func TestVersionBoundsCheck(t *testing.T) {
	for _, tc := range []struct {
		name    string
		bounds  VersionBounds
		version string
		want    Verdict
	}{
		{"no bounds accepts anything", VersionBounds{}, "v1.0.0", VerdictInside},
		{"above the floor", VersionBounds{Min: "1.21"}, "v1.26.5", VerdictInside},
		{"exactly the floor", VersionBounds{Min: "1.21"}, "v1.21.0", VerdictInside},
		{"partial floor means dot zero", VersionBounds{Min: "1.21"}, "v1.21.4", VerdictInside},
		{"below the floor", VersionBounds{Min: "2.0"}, "v1.4.2", VerdictTooOld},

		// below is the first version REJECTED. An inclusive max would make the second
		// case pass and the third fail, which is the off-by-one the name exists to stop.
		{"under the ceiling", VersionBounds{Below: "25"}, "v24.19.0", VerdictInside},
		{"exactly the ceiling", VersionBounds{Below: "25"}, "v25.0.0", VerdictTooNew},
		{"over the ceiling", VersionBounds{Below: "25"}, "v25.9.0", VerdictTooNew},

		{"inside both", VersionBounds{Min: "22", Below: "25"}, "v24.19.0", VerdictInside},
		{"under both", VersionBounds{Min: "22", Below: "25"}, "v20.1.0", VerdictTooOld},
		{"over both", VersionBounds{Min: "22", Below: "25"}, "v26.0.0", VerdictTooNew},

		// Unknown is never a violation. A window magus cannot evaluate must not fail a
		// build; the alternative is a typo silently blocking every op that uses the tool.
		{"unparseable version", VersionBounds{Min: "1.0"}, "not-a-version", VerdictUnknown},
		{"unparseable floor", VersionBounds{Min: "latest"}, "v1.0.0", VerdictUnknown},
		{"unparseable ceiling", VersionBounds{Below: "next"}, "v1.0.0", VerdictUnknown},

		// A prerelease sorts below its release, so a ceiling of 25 rejects 25.0.0-rc1
		// and a floor of 25 does too. Both follow from semver ordering rather than from
		// anything this type decides.
		{"prerelease under the ceiling is still the ceiling line", VersionBounds{Below: "25"}, "v25.0.0-rc1", VerdictInside},
		{"prerelease below the floor", VersionBounds{Min: "25"}, "v25.0.0-rc1", VerdictTooOld},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.bounds.Check(tc.version))
		})
	}
}

// Intersect is what stops a spell and a workspace from loosening each other. Narrower
// wins on each bound independently, so a workspace that sets only a ceiling keeps the
// spell's floor rather than replacing the whole window.
func TestVersionBoundsIntersect(t *testing.T) {
	for _, tc := range []struct {
		name  string
		spell VersionBounds
		works VersionBounds
		want  VersionBounds
	}{
		{"workspace adds a ceiling to a spell floor",
			VersionBounds{Min: "1.21"}, VersionBounds{Below: "1.28"},
			VersionBounds{Min: "1.21", Below: "1.28"}},
		{"workspace raises the floor",
			VersionBounds{Min: "1.21"}, VersionBounds{Min: "1.26"},
			VersionBounds{Min: "1.26"}},
		{"workspace cannot lower the floor",
			VersionBounds{Min: "1.26"}, VersionBounds{Min: "1.21"},
			VersionBounds{Min: "1.26"}},
		{"workspace cannot raise the ceiling",
			VersionBounds{Below: "25"}, VersionBounds{Below: "30"},
			VersionBounds{Below: "25"}},
		{"empty workspace changes nothing",
			VersionBounds{Min: "1.21", Below: "2"}, VersionBounds{},
			VersionBounds{Min: "1.21", Below: "2"}},
		{"empty spell takes the workspace whole",
			VersionBounds{}, VersionBounds{Min: "22", Below: "25"},
			VersionBounds{Min: "22", Below: "25"}},
		// Keeping the unparseable bound is what surfaces it: dropping it would widen
		// the window silently, where Check turns it into VerdictUnknown a reader sees.
		{"an unparseable candidate never wins",
			VersionBounds{Min: "1.21"}, VersionBounds{Min: "latest"},
			VersionBounds{Min: "1.21"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.spell.Intersect(tc.works))
		})
	}
}

func TestVersionBoundsIsZero(t *testing.T) {
	assert.True(t, VersionBounds{}.IsZero())
	assert.False(t, VersionBounds{Min: "1"}.IsZero())
	assert.False(t, VersionBounds{Below: "2"}.IsZero())
}
