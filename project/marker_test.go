package project

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsIgnoreDir(t *testing.T) {
	for _, name := range []string{".git", ".hg", ".jj", ".magus", ".build", "vendor", "node_modules", "target", "gen"} {
		assert.True(t, IsIgnoreDir(name), "IsIgnoreDir(%q) should be true", name)
	}
	for _, name := range []string{"src", "cmd", "pkg", "internal", "starter"} {
		assert.False(t, IsIgnoreDir(name), "IsIgnoreDir(%q) should be false", name)
	}
}

func TestIgnoreDirs_ContainsExpected(t *testing.T) {
	for _, d := range []string{".git", "vendor", "node_modules"} {
		assert.Contains(t, IgnoreDirs, d, "IgnoreDirs missing %q", d)
	}
}
