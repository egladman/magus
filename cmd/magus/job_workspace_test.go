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
	s.root = s.T().TempDir()
}

func (s *UnloadedWorkspaceSuite) placeBinary() string {
	own := filepath.Join(s.root, "magus")
	s.Require().NoError(os.WriteFile(own, []byte("#!/bin/sh\n"), 0o755))
	return own
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadNamesTheCheckoutsOwnBinary() {
	own := s.placeBinary()
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root)
	s.Contains(got, types.WorkspaceNeedsNewerMagus.Error())
	s.Contains(got, fmt.Sprintf("`%s job apply`", own))
}

func (s *UnloadedWorkspaceSuite) TestAStaleLoadWithNoBinaryNamesNone() {
	got := workspaceLoadFailure(types.WorkspaceNeedsNewerMagus, s.root)
	s.Equal(types.WorkspaceNeedsNewerMagus.Error(), got)
}

func (s *UnloadedWorkspaceSuite) TestAnotherFailureNamesNoBinary() {
	s.placeBinary()
	s.Equal("a typo", workspaceLoadFailure(errors.New("a typo"), s.root))
}
