package vcs

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

type AttrsSpaceSuite struct {
	suite.Suite
	dir string
}

func TestAttrsSpaceSuite(t *testing.T) { suite.Run(t, new(AttrsSpaceSuite)) }

func (s *AttrsSpaceSuite) SetupTest() {
	s.dir = testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n"}).Root()
}

func (s *AttrsSpaceSuite) attr(name, path string) string {
	out, err := exec.Command("git", "-C", s.dir, "check-attr", name, "--", path).Output()
	s.Require().NoError(err)
	return strings.TrimSpace(string(out))
}

func (s *AttrsSpaceSuite) TestPatternWithASpaceIsQuoted() {
	body := gitAttrsBody(types.MergeDriverGlobs{Outputs: []string{"docs/reference/config *.md"}})

	s.Contains(body, "\"docs/reference/config *.md\" merge=magus linguist-generated\n")
}

func (s *AttrsSpaceSuite) TestPatternWithoutASpaceStaysBare() {
	body := gitAttrsBody(types.MergeDriverGlobs{Outputs: []string{"docs/reference/config.md"}})

	s.Contains(body, "\ndocs/reference/config.md merge=magus linguist-generated\n")
}

func (s *AttrsSpaceSuite) TestGitMatchesAQuotedPatternAndCheckPasses() {
	globs := types.MergeDriverGlobs{Outputs: []string{"docs/reference/config *.md"}}
	s.Require().NoError(gitVCS{}.InstallMergeDriver(s.T().Context(), s.dir, globs))

	got := s.attr("merge", "docs/reference/config x.md")
	ok, err := gitVCS{}.CheckMergeDriver(s.T().Context(), s.dir)

	s.Equal("docs/reference/config x.md: merge: magus", got)
	s.Require().NoError(err)
	s.True(ok)
	_, statErr := os.Stat(filepath.Join(s.dir, ".gitattributes"))
	s.NoError(statErr)
}
