package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsIgnoreDir(t *testing.T) {
	// Any dot-directory is skipped, plus the well-known non-dot build/dep dirs.
	for _, name := range []string{
		".git", ".hg", ".jj", ".magus", ".build", ".claude", ".idea", ".vscode",
		"vendor", "node_modules", "target", "gen",
	} {
		assert.True(t, IsIgnoreDir(name), "IsIgnoreDir(%q) should be true", name)
	}
	for _, name := range []string{"src", "cmd", "pkg", "internal", "starter"} {
		assert.False(t, IsIgnoreDir(name), "IsIgnoreDir(%q) should be false", name)
	}
}

func TestIgnoreDirs_ContainsExpected(t *testing.T) {
	// The list holds only non-dot names; dot-dirs are covered by the prefix rule.
	for _, d := range []string{"vendor", "node_modules", "target", "gen"} {
		assert.Contains(t, IgnoreDirs, d, "IgnoreDirs missing %q", d)
	}
	for _, d := range IgnoreDirs {
		assert.False(t, d[0] == '.', "IgnoreDirs should not list dot-dirs (%q); the prefix rule covers them", d)
	}
}
