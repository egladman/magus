package gitdiff

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type GitDiffSuite struct {
	suite.Suite
}

func TestGitDiff(t *testing.T) {
	suite.Run(t, new(GitDiffSuite))
}

func (s *GitDiffSuite) TestHunksSpanTheWorkingCopyLines() {
	diff := "@@ -3,0 +4,2 @@\n+a\n+b\n@@ -9,2 +11 @@\n@@ -20,3 +21,0 @@\n"
	s.Equal([]Span{{4, 5}, {11, 11}, {21, 22}}, hunks(diff))
}

func (s *GitDiffSuite) TestOverlapsMeetsAClosedRange() {
	spans := []Span{{4, 5}}
	s.True(Overlaps(spans, 5, 9))
	s.False(Overlaps(spans, 6, 9))
}
