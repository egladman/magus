package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSemverVersionString(t *testing.T) {
	for _, tc := range []struct {
		name string
		v    SemverVersion
		want string
	}{
		{"bare", SemverVersion{Major: 1, Minor: 2, Patch: 3}, "v1.2.3"},
		{"prerelease", SemverVersion{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1"}, "v1.2.3-rc1"},
		{"metadata", SemverVersion{Major: 1, Minor: 2, Patch: 3, Metadata: "build5"}, "v1.2.3+build5"},
		{"prerelease and metadata", SemverVersion{Major: 1, Minor: 2, Patch: 3, Prerelease: "rc1", Metadata: "build5"}, "v1.2.3-rc1+build5"},
		{"zero value", SemverVersion{}, "v0.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.v.String())
		})
	}
}

// String is the canonical rendering, not a round-trip of what the user wrote:
// a non-canonical Original (a leading zero, a missing "v") must NOT survive it.
func TestSemverVersionStringNotOriginal(t *testing.T) {
	v := SemverVersion{Major: 1, Minor: 2, Patch: 3, Original: "1.02.3"}
	assert.Equal(t, "v1.2.3", v.String())
	assert.NotEqual(t, v.Original, v.String())
}

