package main

import (
	"flag"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/commands"
	"github.com/egladman/magus/libs/cmdline"
	"github.com/egladman/magus/libs/cmdline/exit"
)

type outputMetadataSuite struct{ suite.Suite }

func TestOutputMetadata(t *testing.T) { suite.Run(t, new(outputMetadataSuite)) }

func (s *outputMetadataSuite) SetupTest() {
	original := commands.Root
	s.T().Cleanup(func() { commands.Root = original })
	commands.Root = cmdline.Command{Children: []cmdline.Command{
		{Name: "raw-example", UnstructuredOutput: true, Children: []cmdline.Command{{Name: "record"}}},
		{Name: "server", Children: []cmdline.Command{{Name: "start"}}},
	}}
}

func (s *outputMetadataSuite) TestANewDeclarationRefusesOutputBeforeTheVerb() {
	err := refuseOutputOnPath("json", []string{"raw-example"})

	s.EqualError(err, "magus raw-example: -o is not supported by this command")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *outputMetadataSuite) TestANewDeclarationRefusesTheShortFlagAfterTheVerb() {
	fs := flag.NewFlagSet("raw-example", flag.ContinueOnError)
	fs.String("o", "", "format")
	s.Require().NoError(fs.Parse([]string{"-o", "json"}))

	err := checkOutputSupported("raw-example", fs)

	s.EqualError(err, "magus raw-example: -o is not supported by this command")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *outputMetadataSuite) TestANewDeclarationRefusesTheLongFlagAfterTheVerb() {
	fs := flag.NewFlagSet("raw-example", flag.ContinueOnError)
	fs.String("output", "", "format")
	s.Require().NoError(fs.Parse([]string{"--output", "json"}))

	s.Error(checkOutputSupported("raw-example", fs))
}

func (s *outputMetadataSuite) TestTheDeclarationOverridesAnOldExclusion() {
	s.NoError(refuseOutputOnPath("json", []string{"server", "start"}))
	fs := flag.NewFlagSet("server start", flag.ContinueOnError)
	fs.String("o", "", "format")
	s.Require().NoError(fs.Parse([]string{"-o", "json"}))
	s.NoError(checkOutputSupported("server start", fs))
}

func (s *outputMetadataSuite) TestAChildDoesNotInheritItsParentsRawOutput() {
	s.NoError(refuseOutputOnPath("json", []string{"raw-example", "record"}))
}

func (s *outputMetadataSuite) TestAnAmbientDefaultDoesNotCountAsAnExplicitFlag() {
	fs := flag.NewFlagSet("raw-example", flag.ContinueOnError)
	fs.String("o", "json", "format")

	s.NoError(checkOutputSupported("raw-example", fs))
	s.NoError(refuseOutputOnPath("", []string{"raw-example"}))
}
