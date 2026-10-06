package types

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMagusVersionContext covers the round-trip: an unstamped context reads "",
// and a stamped one reads back what WithMagusVersion stored.
func TestMagusVersionContext(t *testing.T) {
	assert.Empty(t, MagusVersionFromContext(context.Background()), "an unstamped context reads empty")

	ctx := WithMagusVersion(context.Background(), "v1.2.3")
	assert.Equal(t, "v1.2.3", MagusVersionFromContext(ctx))
}

func TestIsDevMagusVersion(t *testing.T) {
	for _, tc := range []struct {
		ver string
		dev bool
	}{
		{"", true},                 // unstamped (bare library caller)
		{"unknown", true},          // linker default dev sentinel
		{"v0.1.0-5-gabc123", true}, // git-describe dev build past a tag
		{"v0.1.0", false},          // clean tagged release (version of record)
		{"v1.2.3", false},
	} {
		if got := IsDevMagusVersion(tc.ver); got != tc.dev {
			t.Errorf("IsDevMagusVersion(%q) = %v, want %v", tc.ver, got, tc.dev)
		}
	}
}

// The cases are the tag shapes release-publish flags with semver\isStable, so the GitHub
// Release flag, the release index and self update agree on every one.
func TestParseVersionIsOkOnlyForAStableRelease(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"v0.4.0":              true,
		"0.4.0":               true,
		"v1.2.3+build-1":      true,
		"v0.5.0-rc.1":         false,
		"v1.0.0-beta":         false,
		"v1.2.3-rc.1+build-9": false,
		"0.5.0-rc.1":          false,
		"":                    false,
		"not-a-version":       false,
		"v1.2.3.4":            false,
	}
	for version, want := range cases {
		_, ok := ParseVersion(version)
		assert.Equal(t, want, ok, version)
	}
}

func TestParseVersionFillsEveryField(t *testing.T) {
	t.Parallel()
	v, ok := ParseVersion("v1.2.3-rc.1+build-9")
	assert.False(t, ok)
	assert.Equal(t, SemverVersion{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc.1", Metadata: "build-9", Original: "v1.2.3-rc.1+build-9"}, v)
}
