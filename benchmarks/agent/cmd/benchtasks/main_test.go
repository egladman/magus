package main

import (
	"io"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/stretchr/testify/suite"
)

type MainSuite struct{ suite.Suite }

func TestMainSuite(t *testing.T) { suite.Run(t, new(MainSuite)) }

// benchtasks runs the command line args name, returning its error.
func (s *MainSuite) benchtasks(args ...string) error {
	_, err := command.Invoke(s.T().Context(), root(), args,
		command.Streams{Stdin: strings.NewReader(""), Stdout: io.Discard, Stderr: io.Discard})
	return err
}

func (s *MainSuite) TestRootDeclarationPassesCheck() {
	s.NoError(command.Check(root()))
}

func (s *MainSuite) TestRefusesAnUnknownSuite() {
	s.ErrorContains(s.benchtasks("verified"), "verified")
}

func (s *MainSuite) TestSWEBenchRequiresAnOutputFile() {
	s.ErrorContains(s.benchtasks("swebench"), "-o is required")
}

func (s *MainSuite) TestHistoryNamesEveryMissingFlag() {
	s.ErrorContains(s.benchtasks("history", "--repo", "."), "--after, --ref, -o required")
}
