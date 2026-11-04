package e2e

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
)

const (
	answers = "#!/bin/sh\necho program 1.2.3\n"
	refuses = "#!/bin/sh\necho no such flag\nexit 3\n"
)

type binarySuite struct {
	suite.Suite
	workspace *testkit.Workspace
}

func TestBinary(t *testing.T) { suite.Run(t, new(binarySuite)) }

func (s *binarySuite) SetupTest() {
	s.workspace = testkit.NewWorkspace(s.T())
	s.T().Setenv("PATH", s.workspace.Path("path"))
}

func (s *binarySuite) TestCandidateWinsOverPath() {
	s.workspace.WriteExecutable("path/program", answers)
	want := s.workspace.WriteExecutable("elsewhere/program", answers)

	got := LocateBinary("program", nil, Candidate{Path: want, Source: "SOME_VAR"})

	s.Equal(Binary{Name: "program", Path: want, Version: "program 1.2.3", Note: "from SOME_VAR"}, got)
	s.True(got.Found())
}

func (s *binarySuite) TestFallsBackToPathPastAnEmptyCandidate() {
	want := s.workspace.WriteExecutable("path/program", answers)

	got := LocateBinary("program", nil, Candidate{Source: "UNSET_VAR"})

	s.Equal(Binary{Name: "program", Path: want, Version: "program 1.2.3", Note: "from PATH"}, got)
}

func (s *binarySuite) TestNotesTheCandidateItPassedOver() {
	broken := s.workspace.WriteExecutable("elsewhere/program", refuses)
	want := s.workspace.WriteExecutable("path/program", answers)

	got := LocateBinary("program", nil, Candidate{Path: broken, Source: "SOME_VAR"})

	s.Equal(want, got.Path)
	s.Contains(got.Note, fmt.Sprintf("from PATH; passed over %s (SOME_VAR): ", broken))
	s.Contains(got.Note, "no such flag")
}

func (s *binarySuite) TestAbsentSaysWhatItTried() {
	missing := filepath.Join(s.workspace.Root(), "missing")

	got := LocateBinary("program", nil, Candidate{Path: missing, Source: "SOME_VAR"})

	s.False(got.Found())
	s.Contains(got.Note, fmt.Sprintf("absent: %s (SOME_VAR): ", missing))
	s.Contains(got.Note, "program not on PATH")
}

func (s *binarySuite) TestVersionReadsUnderTheGivenEnvironment() {
	path := s.workspace.WriteExecutable("env-program", "#!/bin/sh\necho \"$WHICH\"\n")

	got, err := BinaryVersion(path, []string{"WHICH=from-env"})

	s.Require().NoError(err)
	s.Equal("from-env", got)
}
