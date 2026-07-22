package types

import "testing"

func TestIsDevMagusVersion(t *testing.T) {
	for _, tc := range []struct {
		ver string
		dev bool
	}{
		{"", true},               // unstamped (bare library caller)
		{"unknown", true},        // linker default dev sentinel
		{"v0.1.0-5-gabc123", true}, // git-describe dev build past a tag
		{"v0.1.0", false},        // clean tagged release (version of record)
		{"v1.2.3", false},
	} {
		if got := IsDevMagusVersion(tc.ver); got != tc.dev {
			t.Errorf("IsDevMagusVersion(%q) = %v, want %v", tc.ver, got, tc.dev)
		}
	}
}
