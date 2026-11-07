package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type KnowledgeWorkspacesSuite struct {
	suite.Suite
	user, root string
}

func TestKnowledgeWorkspacesSuite(t *testing.T) { suite.Run(t, new(KnowledgeWorkspacesSuite)) }

func (s *KnowledgeWorkspacesSuite) SetupTest() {
	home := s.T().TempDir()
	s.T().Setenv("XDG_CONFIG_HOME", home)
	s.user = filepath.Join(home, "magus")
	s.root = s.T().TempDir()
	s.Require().NoError(os.MkdirAll(s.user, 0o755))
}

func (s *KnowledgeWorkspacesSuite) write(dir, body string) string {
	p := filepath.Join(dir, Filename)
	s.Require().NoError(os.WriteFile(p, []byte(body), 0o644))
	return p
}

func (s *KnowledgeWorkspacesSuite) load() (Config, error) { return LoadWithRoot("", s.root) }

func (s *KnowledgeWorkspacesSuite) TestTheUserConfigListsNamedEntriesWithTheirLines() {
	file := s.write(s.user, "knowledge:\n  workspaces:\n    - name: magus\n      path: /src/magus\n    - name: buzzed\n      path: ~/buzzed\n")
	cfg, err := s.load()
	s.Require().NoError(err)
	s.Equal([]KnowledgeWorkspace{
		{Name: "magus", Path: "/src/magus", File: file, Line: 3},
		{Name: "buzzed", Path: "~/buzzed", File: file, Line: 5},
	}, cfg.Knowledge.Workspaces)
}

func (s *KnowledgeWorkspacesSuite) TestARepositoryConfigRefusesWithMGS1055() {
	file := s.write(s.root, "knowledge:\n  workspaces:\n    - name: magus\n      path: /src/magus\n")
	_, err := s.load()
	s.Require().ErrorIs(err, types.WorkspacesCommitted)
	s.Contains(err.Error(), file+":3")
	s.Contains(err.Error(), "$XDG_CONFIG_HOME/magus/magus.yaml")
}

func (s *KnowledgeWorkspacesSuite) TestABareStringRefusesWithTheEntryToWrite() {
	s.write(s.user, "knowledge:\n  workspaces:\n    - /src/magus\n")
	_, err := s.load()
	s.Require().ErrorIs(err, types.WorkspaceEntryShape)
	s.Contains(err.Error(), "{name: <name>, path: /src/magus}")
}

func (s *KnowledgeWorkspacesSuite) TestAnUnknownKeyRefuses() {
	s.write(s.user, "knowledge:\n  workspaces:\n    - name: magus\n      path: /src/magus\n      share: [lint]\n")
	_, err := s.load()
	s.Require().ErrorIs(err, types.WorkspaceEntryShape)
	s.Contains(err.Error(), `"share"`)
}

func (s *KnowledgeWorkspacesSuite) TestABadNameRefuses() {
	s.write(s.user, "knowledge:\n  workspaces:\n    - name: Magus\n      path: /src/magus\n")
	_, err := s.load()
	s.Require().ErrorIs(err, types.WorkspaceEntryShape)
	s.Contains(err.Error(), "[a-z0-9-]+")
}

func (s *KnowledgeWorkspacesSuite) TestARepeatedNameRefusesNamingBothLines() {
	file := s.write(s.user, "knowledge:\n  workspaces:\n    - name: magus\n      path: /a\n    - name: magus\n      path: /b\n")
	_, err := s.load()
	s.Require().ErrorIs(err, types.WorkspaceEntryShape)
	s.Contains(err.Error(), file+":5")
	s.Contains(err.Error(), fmt.Sprintf("first at %s:3", file))
}

func (s *KnowledgeWorkspacesSuite) TestARelativePathRefuses() {
	s.write(s.user, "knowledge:\n  workspaces:\n    - name: magus\n      path: ../magus\n")
	_, err := s.load()
	s.Require().ErrorIs(err, types.WorkspaceEntryShape)
	s.Contains(err.Error(), "absolute path or one starting with ~")
}

func (s *KnowledgeWorkspacesSuite) TestRootExpandsTheHomeDirectory() {
	home, err := os.UserHomeDir()
	s.Require().NoError(err)
	got, err := KnowledgeWorkspace{Name: "magus", Path: "~/src/magus"}.Root()
	s.Require().NoError(err)
	s.Equal(filepath.Join(home, "src", "magus"), got)
}
