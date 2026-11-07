package ward

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type ResolveUseSuite struct {
	suite.Suite
	root string
}

func TestResolveUseSuite(t *testing.T) { suite.Run(t, new(ResolveUseSuite)) }

func (s *ResolveUseSuite) SetupTest() {
	parent := s.T().TempDir()
	s.root = filepath.Join(parent, "b")
	s.workspace("b")
	s.workspace("a")
}

func (s *ResolveUseSuite) workspace(name string) string {
	dir := filepath.Join(filepath.Dir(s.root), name)
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "magus.yaml"), nil, 0o644))
	return dir
}

func (s *ResolveUseSuite) refused(path string) *types.DiagnosticError {
	_, d := ResolveUse(s.root, "a", path, []types.ShareKind{types.ShareLint})
	s.Require().NotNil(d)
	s.Equal(types.UseWorkspaceUnavailable, d.Code)
	return d
}

func (s *ResolveUseSuite) TestARelativePathResolvesAgainstTheRoot() {
	got, d := ResolveUse(s.root, "a", "../a", []types.ShareKind{types.ShareLint})
	s.Require().Nil(d)
	s.Equal(types.UsedWorkspace{Name: "a", Root: filepath.Join(filepath.Dir(s.root), "a"),
		Share: []types.ShareKind{types.ShareLint}}, got)
}

func (s *ResolveUseSuite) TestAnAbsolutePathIsKept() {
	abs := filepath.Join(filepath.Dir(s.root), "a")
	got, d := ResolveUse(s.root, "a", abs, nil)
	s.Require().Nil(d)
	s.Equal(abs, got.Root)
}

func (s *ResolveUseSuite) TestAMissingPathIsRefused() {
	s.Contains(s.refused("../absent").Error(), "does not exist")
}

func (s *ResolveUseSuite) TestAFileIsRefused() {
	s.Require().NoError(os.WriteFile(filepath.Join(filepath.Dir(s.root), "f"), nil, 0o644))
	s.Contains(s.refused("../f").Error(), "is not a directory")
}

func (s *ResolveUseSuite) TestADirectoryHoldingNoWorkspaceIsRefused() {
	s.Require().NoError(os.MkdirAll(filepath.Join(filepath.Dir(s.root), "plain"), 0o755))
	s.Contains(s.refused("../plain").Error(), "holds no magus.yaml")
}

func (s *ResolveUseSuite) TestTheWorkspaceItselfIsRefused() {
	s.Contains(s.refused(".").Error(), "is this workspace")
}

func (s *ResolveUseSuite) TestTheRefusalNamesTheEntryAndTheFix() {
	msg := s.refused("../absent").Error()
	s.Contains(msg, `use "a"`)
	s.Contains(msg, "remove the entry")
}
