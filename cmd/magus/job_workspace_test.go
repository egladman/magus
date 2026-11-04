package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

// UnloadedWorkspaceSuite pins what a binary too old for the tree says.
type UnloadedWorkspaceSuite struct {
	suite.Suite
	root string
}

func TestUnloadedWorkspaceSuite(t *testing.T) {
	suite.Run(t, new(UnloadedWorkspaceSuite))
}

func (s *UnloadedWorkspaceSuite) SetupTest() {
	root, err := filepath.EvalSymlinks(s.T().TempDir())
	s.Require().NoError(err)
	s.root = root
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magus.yaml"), []byte("{}\n"), 0o644))
}

func (s *UnloadedWorkspaceSuite) placeBinary() string {
	own := filepath.Join(s.root, "magus")
	s.Require().NoError(os.WriteFile(own, []byte("#!/bin/sh\n"), 0o755))
	return own
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadNamesTheCheckoutsOwnBinary() {
	own := s.placeBinary()
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root, "/elsewhere/magus")
	s.Contains(got, types.WorkspaceNeedsNewerMagus.Error())
	s.Contains(got, fmt.Sprintf("rerun the command with `%s`", own))
	s.NotContains(got, "job apply")
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadByThatVeryBinaryNamesNone() {
	own := s.placeBinary()
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root, own)
	s.Equal(types.WorkspaceNeedsNewerMagus.Error(), got)
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadWithANonExecutableFileNamesNone() {
	own := s.placeBinary()
	s.Require().NoError(os.Chmod(own, 0o644))
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root, "/elsewhere/magus")
	s.Equal(types.WorkspaceNeedsNewerMagus.Error(), got)
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadWithNoBinaryNamesNone() {
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root, "/elsewhere/magus")
	s.Equal(types.WorkspaceNeedsNewerMagus.Error(), got)
}

func (s *UnloadedWorkspaceSuite) TestAnotherFailureNamesNoBinary() {
	s.placeBinary()
	s.Equal("a typo", workspaceLoadFailure(errors.New("a typo"), s.root, "/elsewhere/magus"))
}
