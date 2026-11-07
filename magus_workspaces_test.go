package magus

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

type ReconcileWorkspacesLoadSuite struct {
	suite.Suite
	a, b string
}

func TestReconcileWorkspacesLoadSuite(t *testing.T) {
	suite.Run(t, new(ReconcileWorkspacesLoadSuite))
}

func (s *ReconcileWorkspacesLoadSuite) SetupTest() {
	parent := s.T().TempDir()
	s.a, s.b = filepath.Join(parent, "a"), filepath.Join(parent, "b")
	for _, dir := range []string{s.a, s.b} {
		s.Require().NoError(os.MkdirAll(dir, 0o755))
		s.Require().NoError(os.WriteFile(filepath.Join(dir, "magus.yaml"), nil, 0o644))
	}
}

func (s *ReconcileWorkspacesLoadSuite) git(dir string, ignore string) {
	cmd := exec.Command("git", "init", "-q", dir)
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, string(out))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(ignore), 0o644))
}

func (s *ReconcileWorkspacesLoadSuite) entries() []config.KnowledgeWorkspace {
	return []config.KnowledgeWorkspace{
		{Name: "magus", Path: s.a, File: "u.yaml", Line: 3},
		{Name: "buzzed", Path: s.b, File: "u.yaml", Line: 5},
	}
}

func (s *ReconcileWorkspacesLoadSuite) reconcile() ([]types.ListedWorkspace, string, error) {
	var stderr bytes.Buffer
	got, err := reconcileWorkspaces(context.Background(), s.a, s.entries(), types.VCSOptions{}, &stderr)
	return got, stderr.String(), err
}

func (s *ReconcileWorkspacesLoadSuite) TestEachChangePrintsOneLine() {
	s.git(s.a, ".magus/\n")
	got, stderr, err := s.reconcile()
	s.Require().NoError(err)
	s.Len(got, 2)
	s.Contains(stderr, "workspace buzzed: linked .magus/workspaces/buzzed -> ")
	_, stderr, err = s.reconcile()
	s.Require().NoError(err)
	s.Empty(stderr, "a load that changes nothing prints nothing")
}

func (s *ReconcileWorkspacesLoadSuite) TestAVCSThatDoesNotIgnoreThemRefusesWithMGS1057() {
	s.git(s.a, "")
	_, _, err := s.reconcile()
	s.Require().ErrorIs(err, types.WorkspacesTracked)
	s.NoDirExists(filepath.Join(s.a, ".magus"), "nothing is made before the refusal")
}

func (s *ReconcileWorkspacesLoadSuite) TestARootNoVCSClaimsNeedsNoIgnore() {
	_, _, err := s.reconcile()
	s.Require().NoError(err)
}
