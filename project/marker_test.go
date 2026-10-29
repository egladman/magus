package project

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
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

type CheckoutRootSuite struct {
	suite.Suite
	root string
}

func TestCheckoutRootSuite(t *testing.T) { suite.Run(t, new(CheckoutRootSuite)) }

func (s *CheckoutRootSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.Require().NoError(os.Mkdir(filepath.Join(s.root, ".git"), 0o755))
	s.Require().NoError(os.MkdirAll(filepath.Join(s.root, "a", "b"), 0o755))
}

func (s *CheckoutRootSuite) TestClimbsToTheNearestMarker() {
	got, ok := CheckoutRoot(filepath.Join(s.root, "a", "b"))
	s.True(ok)
	s.Equal(s.root, got)
}

func (s *CheckoutRootSuite) TestNestedCheckoutWins() {
	nested := filepath.Join(s.root, "a")
	s.Require().NoError(os.Mkdir(filepath.Join(nested, ".jj"), 0o755))
	got, ok := CheckoutRoot(filepath.Join(nested, "b"))
	s.True(ok)
	s.Equal(nested, got)
}
