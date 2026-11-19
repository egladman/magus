package bench

import (
	"io"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/stretchr/testify/suite"
)

type cliSuite struct{ suite.Suite }

func TestCLISuite(t *testing.T) { suite.Run(t, new(cliSuite)) }

func (s *cliSuite) run(args ...string) error {
	_, err := command.Invoke(s.T().Context(), ReportCommand(), args,
		command.Streams{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	return err
}

func (s *cliSuite) TestDeclarationPassesCheck() {
	s.NoError(command.Check(ReportCommand()))
}

func (s *cliSuite) TestStageWantsOneOperand() {
	err := s.run("report", "-o", "out.md")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *cliSuite) TestExtractFailureExitsTwo() {
	err := s.run("extract", s.T().TempDir())
	s.ErrorContains(err, "-o is required")
	s.Equal(2, exit.Code(err))
}

func (s *cliSuite) TestOtherStageFailureExitsOne() {
	err := s.run("analyze", "metrics.jsonl")
	s.ErrorContains(err, "-o is required")
	s.Equal(exit.Failure, exit.Code(err))
}
