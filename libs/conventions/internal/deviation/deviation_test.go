package deviation

import (
	"os"
	"testing"

	"github.com/stretchr/testify/suite"
)

type LedgerSuite struct {
	suite.Suite
}

func TestLedger(t *testing.T) { suite.Run(t, new(LedgerSuite)) }

const file = `inlineliteral:
  exits:
    - {path: a/b.go, symbol: defaults, reason: "a named defaults literal"}
    - {path: a/b.go, symbol: config, reason: "read by a table"}
noThrow:
  anything: [1, 2]
`

// policy writes body as analyzers.yaml in a fresh working directory.
func (s *LedgerSuite) policy(body string) {
	s.T().Chdir(s.T().TempDir())
	s.Require().NoError(os.WriteFile(File, []byte(body), 0o600))
}

func (s *LedgerSuite) TestReadsOneAnalyzersKey() {
	s.policy(file)
	l, err := Load("inlineliteral", ".", false)
	s.Require().NoError(err)
	s.True(l.Accepts("a/b.go", "defaults"))
	s.False(l.Accepts("a/c.go", "defaults"))
	s.Equal([]Exit{
		{Path: "a/b.go", Symbol: "config", Reason: "read by a table"},
		{Path: "a/b.go", Symbol: "defaults", Reason: "a named defaults literal"},
	}, l.In("a/b.go"))
}

func (s *LedgerSuite) TestAnExitWithoutAReasonIsRefused() {
	s.policy("inlineliteral:\n  exits:\n    - {path: a.go, symbol: f}\n")
	_, err := Load("inlineliteral", ".", false)
	s.ErrorContains(err, "the exit for f in a.go gives no reason")
}

func (s *LedgerSuite) TestAnUnknownFieldIsRefused() {
	s.policy("inlineliteral:\n  exits:\n    - {path: a.go, symbol: f, resaon: x}\n")
	_, err := Load("inlineliteral", ".", false)
	s.ErrorContains(err, "field resaon not found")
}

func (s *LedgerSuite) TestABaselineIsRefused() {
	s.policy("inlineliteral:\n  baseline:\n    - {path: a.go, symbol: f}\n")
	_, err := Load("inlineliteral", ".", false)
	s.ErrorContains(err, "field baseline not found")
}

func (s *LedgerSuite) TestNoFileHoldsNoExits() {
	s.T().Chdir(s.T().TempDir())
	l, err := Load("inlineliteral", ".", false)
	s.Require().NoError(err)
	s.Empty(l.In("a/b.go"))
}

func (s *LedgerSuite) TestNoKeyHoldsNoExits() {
	s.policy("passthrough: {}\n")
	l, err := Load("inlineliteral", ".", false)
	s.Require().NoError(err)
	s.Empty(l.In("a/b.go"))
}

func (s *LedgerSuite) TestAnExitNamingAMissingFileIsALoadError() {
	s.policy(file)
	_, err := Load("inlineliteral", ".", true)
	s.ErrorContains(err, "the exit for defaults names a/b.go, which is not a file under .")
}
