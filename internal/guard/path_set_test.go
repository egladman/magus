package guard

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"
)

// BypassClassSuite proves each bypass class closed over every spelling
// of a denied line, not one example. Each class keeps its allowed
// sibling. The seeds are the probes that found the bypasses 2026-11-18.
type BypassClassSuite struct {
	suite.Suite
	root string
}

func TestBypassClassSuite(t *testing.T) {
	suite.Run(t, new(BypassClassSuite))
}

func (s *BypassClassSuite) SetupTest() {
	s.root = canonicalPath(s.T().TempDir())
}

// deps is the strict rule set in a workspace at s.root, called there.
func (s *BypassClassSuite) deps() Dependencies {
	deps := strict(testDependencies())
	deps.scope = workspaceScope{root: s.root, dir: s.root, home: canonicalPath(s.T().TempDir())}
	return deps
}

func (s *BypassClassSuite) denies(command string) {
	s.T().Helper()
	v := Evaluate(s.T().Context(), s.deps(), command)
	s.NotEmpty(v.Deny, "%q must deny", command)
}

func (s *BypassClassSuite) passes(command string) {
	s.T().Helper()
	v := Evaluate(s.T().Context(), s.deps(), command)
	s.Empty(v.Deny, "%q must not deny (rule %s)", command, v.Rule.Name)
}

// wholeTreeSpellings name the whole tree, or cannot be read as
// narrower and so count as it.
func (s *BypassClassSuite) wholeTreeSpellings() []string {
	return []string{
		".", "./", "./.", "a/..", "./a/../.", "..", "../..", "*", "**", "*.go", "?*", "[a-z]*",
		":/", "':(top)'", "':(exclude)vendor'", ":!vendor", "''", s.root, s.root + "/", s.root + "/a/..", "/",
		filepath.Dir(s.root),
	}
}

// narrowSpellings name less than the tree.
var narrowSpellings = []string{"docs/x.md", "internal/guard", "a/../b", "internal/*.go", "./docs"}

// pathSetSites are the rules whose exemption or deny reads a path set.
var pathSetSites = []string{
	"git stash push -- %s", "git stash push -m wip -- %s", "git checkout -- %s",
	"git checkout HEAD -- %s", "git restore %s", "git restore --source=HEAD -- %s",
	"git add %s", "git add -- %s", "jj restore %s", "hg revert %s",
}

func (s *BypassClassSuite) TestAPathSetIsWholeTreeUnlessProvenNarrower() {
	for _, site := range pathSetSites {
		for _, spelling := range s.wholeTreeSpellings() {
			s.denies(fmt.Sprintf(site, spelling))
			s.denies(fmt.Sprintf(site, "docs/x.md "+spelling))
		}
		for _, spelling := range narrowSpellings {
			s.passes(fmt.Sprintf(site, spelling))
		}
	}
}

func (s *BypassClassSuite) TestAPathOutsideTheTreeIsNarrower() {
	outside := filepath.Join(filepath.Dir(s.root), "elsewhere", "x.go")
	s.passes("git restore " + outside)
	s.passes("git restore ../elsewhere/x.go")
	// After a cd the directory is unknown, so this is not provably
	// outside.
	s.denies("cd sub && git restore ../elsewhere/..")
}
