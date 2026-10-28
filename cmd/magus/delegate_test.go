package main

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

func (s *goRunBinarySuite) cacheEntry(id string) string {
	return filepath.Join(s.T().TempDir(), "go-cache", id[:2], id+"-d", "magus")
}

func (s *goRunBinarySuite) TestBuildCacheEntryIsGoRun() {
	s.True(goRunBinary(s.cacheEntry("ab" + strings.Repeat("0", 62))))
}

func (s *goRunBinarySuite) TestLinkDirectoryIsGoRun() {
	s.True(goRunBinary(filepath.Join(s.T().TempDir(), "go-build123", "b001", "exe", "magus")))
}

func (s *goRunBinarySuite) TestWorkspaceBinaryIsNotGoRun() {
	s.False(goRunBinary(filepath.Join(s.T().TempDir(), "checkout", "magus")))
}

func (s *goRunBinarySuite) TestExeDirectoryOutsideGoBuildIsNotGoRun() {
	s.False(goRunBinary(filepath.Join(s.T().TempDir(), "tools", "exe", "magus")))
}

func (s *goRunBinarySuite) TestEntryUnderTheWrongShardIsNotGoRun() {
	id := "ab" + strings.Repeat("0", 62)
	s.False(goRunBinary(filepath.Join(s.T().TempDir(), "cd", id+"-d", "magus")))
}

func (s *goRunBinarySuite) TestShortHexEntryIsNotGoRun() {
	s.False(goRunBinary(filepath.Join(s.T().TempDir(), "ab", "abcd-d", "magus")))
}
