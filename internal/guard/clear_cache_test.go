package guard

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type CacheClearSuite struct {
	suite.Suite
}

func TestCacheClearSuite(t *testing.T) {
	suite.Run(t, new(CacheClearSuite))
}

func (s *CacheClearSuite) denied(line string) string {
	return denyCacheClear(line, DialectBash)
}

func (s *CacheClearSuite) TestDeniesGoCleanCache() {
	s.Contains(s.denied("go clean -cache"), types.ReclaimDiskCommand)
}

func (s *CacheClearSuite) TestDeniesRmOnTheGoBuildCache() {
	s.Contains(s.denied("rm -rf /Users/me/Library/Caches/go-build"), types.ReclaimDiskCommand)
}

func (s *CacheClearSuite) TestDeniesRmInsideACache() {
	s.NotEmpty(s.denied("rm -rf ~/.cache/go-build/ab"))
}

func (s *CacheClearSuite) TestDeniesFindDeleteOnACache() {
	s.NotEmpty(s.denied("find /c/go-build -mtime +1 -delete"))
}

func (s *CacheClearSuite) TestDeniesAClearInsideShC() {
	s.NotEmpty(s.denied(`sh -c "go clean -cache"`))
}

func (s *CacheClearSuite) TestDeniesAClearLaterOnALine() {
	s.NotEmpty(s.denied("echo hi && npm cache clean --force"))
}

func (s *CacheClearSuite) TestPassesGoCleanOfATestCache() {
	s.Empty(s.denied("go clean -testcache"))
}

func (s *CacheClearSuite) TestPassesRmOfASiblingName() {
	s.Empty(s.denied("rm -rf /tmp/go-build-notes"))
}

func (s *CacheClearSuite) TestPassesFindReadingACache() {
	s.Empty(s.denied("find /c/go-build -mtime +1"))
}

func (s *CacheClearSuite) TestPassesACommandThatOnlyNamesOne() {
	s.Empty(s.denied(`echo "rm -rf go-build"`))
}

func (s *CacheClearSuite) TestRankFillsASilence() {
	got := rankCacheClear(ShellVerdict{}, "no")
	s.Equal("no", got.Deny)
	s.Equal(denyRuleCacheClear, got.Rule.Name)
}

func (s *CacheClearSuite) TestRankOutranksTheRawToolDeny() {
	s.Equal("no", rankCacheClear(ShellVerdict{Deny: "raw tool"}, "no").Deny)
}

func (s *CacheClearSuite) TestRankWithNoReasonKeepsTheVerdict() {
	s.Equal("raw tool", rankCacheClear(ShellVerdict{Deny: "raw tool"}, "").Deny)
}
