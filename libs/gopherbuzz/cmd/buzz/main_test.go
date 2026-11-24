package main

import (
	"bytes"
	"context"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type ParseSuite struct {
	suite.Suite
	stdout bytes.Buffer
}

func TestParseSuite(t *testing.T) { suite.Run(t, new(ParseSuite)) }

func (s *ParseSuite) SetupTest() { s.stdout.Reset() }

// parse runs argv through the declared command line and returns the opts its
// Run would get.
func (s *ParseSuite) parse(argv ...string) (opts, error) {
	var got opts
	capture := func(_ context.Context, o opts) error {
		got = o
		return nil
	}
	_, err := command.Invoke(context.Background(), buzzCommand(capture), argv,
		command.Streams{Stdin: strings.NewReader(""), Stdout: &s.stdout, Stderr: io.Discard},
		command.WithVersion("buzz test"))
	return got, err
}

func (s *ParseSuite) requireOptions(want opts, argv ...string) {
	got, err := s.parse(argv...)
	s.Require().NoError(err)
	s.Equal(want, got)
}

func (s *ParseSuite) TestBareFile() { s.requireOptions(opts{args: []string{"x.buzz"}}, "x.buzz") }

func (s *ParseSuite) TestFileThenScriptArgs() {
	s.requireOptions(opts{args: []string{"x.buzz", "a", "b"}}, "x.buzz", "a", "b")
}

func (s *ParseSuite) TestCheckLong() {
	s.requireOptions(opts{check: true, args: []string{"x.buzz"}}, "--check", "x.buzz")
}

func (s *ParseSuite) TestCheckShort() {
	s.requireOptions(opts{check: true, args: []string{"x.buzz"}}, "-c", "x.buzz")
}

func (s *ParseSuite) TestTestShort() {
	s.requireOptions(opts{test: true, args: []string{"x.buzz"}}, "-t", "x.buzz")
}

func (s *ParseSuite) TestTestLong() {
	s.requireOptions(opts{test: true, args: []string{"x.buzz"}}, "--test", "x.buzz")
}

func (s *ParseSuite) TestEval() { s.requireOptions(opts{eval: "code"}, "-e", "code") }

func (s *ParseSuite) TestEvalEquals() { s.requireOptions(opts{eval: "code"}, "--eval=code") }

func (s *ParseSuite) TestAST() {
	s.requireOptions(opts{dumpAST: true, args: []string{"x.buzz"}}, "--ast", "x.buzz")
}

func (s *ParseSuite) TestVersionPrintsAndRunsNothing() {
	got, err := s.parse("-v")
	s.Require().NoError(err)
	s.Equal(opts{}, got)
	s.Equal("buzz test\n", s.stdout.String())
}

func (s *ParseSuite) TestHelpPrintsAndRunsNothing() {
	got, err := s.parse("--help")
	s.Require().ErrorIs(err, flag.ErrHelp)
	s.Equal(opts{}, got)
	s.Contains(s.stdout.String(), "-e, --eval code")
}

func (s *ParseSuite) TestRepeatableLibraryPath() {
	s.requireOptions(opts{libDirs: []string{"a", "b"}, args: []string{"x.buzz"}}, "-L", "a", "--library-path", "b", "x.buzz")
}

func (s *ParseSuite) TestStdinDash() { s.requireOptions(opts{args: []string{"-"}}, "-") }

func (s *ParseSuite) TestOptionsStopAtTheScript() {
	s.requireOptions(opts{args: []string{"x.buzz", "-c"}}, "x.buzz", "-c")
}

func (s *ParseSuite) TestDoubleDashEndsOptions() {
	s.requireOptions(opts{args: []string{"-c"}}, "--", "-c")
}

func (s *ParseSuite) TestUnknownOption() {
	_, err := s.parse("--bogus")
	s.Error(err)
}

func (s *ParseSuite) TestEvalWithNoValue() {
	_, err := s.parse("-e")
	s.Error(err)
}

func (s *ParseSuite) TestLibraryPathWithNoValue() {
	_, err := s.parse("--library-path")
	s.Error(err)
}

func TestSourceRejectsEvalWithFile(t *testing.T) {
	_, _, err := source(opts{eval: "x", args: []string{"f.buzz"}})
	assert.Error(t, err, "source(-e + file): want error")
}
