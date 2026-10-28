package ward

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type goRunBinarySuite struct {
	suite.Suite
}

func TestGoRunBinary(t *testing.T) {
	suite.Run(t, new(goRunBinarySuite))
}

func (s *goRunBinarySuite) TestBuildCacheEntryIsGoRun() {
	id := "ab" + strings.Repeat("0", 62)
	s.True(GoRunBinary(filepath.Join(s.T().TempDir(), "go-cache", "ab", id+"-d", "magus")))
}

func (s *goRunBinarySuite) TestLinkDirectoryIsGoRun() {
	s.True(GoRunBinary(filepath.Join(s.T().TempDir(), "go-build123", "b001", "exe", "magus")))
}

func (s *goRunBinarySuite) TestWorkspaceBinaryIsNotGoRun() {
	s.False(GoRunBinary(filepath.Join(s.T().TempDir(), "checkout", "magus")))
}

func (s *goRunBinarySuite) TestExeDirectoryOutsideGoBuildIsNotGoRun() {
	s.False(GoRunBinary(filepath.Join(s.T().TempDir(), "tools", "exe", "magus")))
}

func (s *goRunBinarySuite) TestEntryUnderTheWrongShardIsNotGoRun() {
	id := "ab" + strings.Repeat("0", 62)
	s.False(GoRunBinary(filepath.Join(s.T().TempDir(), "cd", id+"-d", "magus")))
}

func (s *goRunBinarySuite) TestShortHexEntryIsNotGoRun() {
	s.False(GoRunBinary(filepath.Join(s.T().TempDir(), "ab", "abcd-d", "magus")))
}
