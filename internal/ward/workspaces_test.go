package ward

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type ReconcileWorkspacesSuite struct {
	suite.Suite
	a, b string
}

func TestReconcileWorkspacesSuite(t *testing.T) { suite.Run(t, new(ReconcileWorkspacesSuite)) }

func (s *ReconcileWorkspacesSuite) SetupTest() {
	parent := s.T().TempDir()
	s.a, s.b = s.workspace(parent, "a"), s.workspace(parent, "b")
}

func (s *ReconcileWorkspacesSuite) workspace(parent, name string) string {
	dir := filepath.Join(parent, name)
	s.Require().NoError(os.MkdirAll(dir, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "magus.yaml"), nil, 0o644))
	return dir
}

func (s *ReconcileWorkspacesSuite) both() []ListedEntry {
	return []ListedEntry{{Name: "magus", Root: s.a, Where: "u.yaml:3"}, {Name: "buzzed", Root: s.b, Where: "u.yaml:5"}}
}

func (s *ReconcileWorkspacesSuite) at(tree, name string) string {
	return filepath.Join(tree, filepath.FromSlash(types.WorkspacesDir), name)
}

func (s *ReconcileWorkspacesSuite) target(tree, name string) string {
	got, err := os.Readlink(s.at(tree, name))
	s.Require().NoError(err)
	return got
}

func (s *ReconcileWorkspacesSuite) gone(tree, name string) {
	_, err := os.Lstat(s.at(tree, name))
	s.True(os.IsNotExist(err), "%s holds no %s", tree, name)
}

func (s *ReconcileWorkspacesSuite) TestAListedPairPointsEachTreeAtTheOther() {
	got, lines, err := ReconcileWorkspaces(s.a, s.both())
	s.Require().NoError(err)
	s.Equal([]types.ListedWorkspace{{Name: "magus", Root: s.a}, {Name: "buzzed", Root: s.b, Dir: ".magus/workspaces/buzzed"}}, got)
	s.Equal(s.b, s.target(s.a, "buzzed"))
	s.Equal(s.a, s.target(s.b, "magus"))
	s.Len(lines, 2)
	s.Equal("workspace buzzed: linked .magus/workspaces/buzzed -> "+s.b, lines[0])
}

func (s *ReconcileWorkspacesSuite) TestASecondLoadChangesNothingAndSaysNothing() {
	_, _, err := ReconcileWorkspaces(s.a, s.both())
	s.Require().NoError(err)
	_, lines, err := ReconcileWorkspaces(s.a, s.both())
	s.Require().NoError(err)
	s.Empty(lines)
}

func (s *ReconcileWorkspacesSuite) TestARemovedEntryLeavesNothingInEitherTree() {
	_, _, err := ReconcileWorkspaces(s.a, s.both())
	s.Require().NoError(err)
	_, lines, err := ReconcileWorkspaces(s.a, s.both()[:1])
	s.Require().NoError(err)
	s.Len(lines, 2)
	s.gone(s.a, "buzzed")
	s.gone(s.b, "magus")
	s.NoDirExists(filepath.Join(s.a, filepath.FromSlash(types.WorkspacesDir)))
	s.NoDirExists(filepath.Join(s.b, filepath.FromSlash(types.WorkspacesDir)))
	_, lines, err = ReconcileWorkspaces(s.a, s.both()[:1])
	s.Require().NoError(err)
	s.Empty(lines, "a second load after the removal does nothing")
}

func (s *ReconcileWorkspacesSuite) TestAnUnlistedRootRemovesItsOwnAndTheirCounterparts() {
	_, _, err := ReconcileWorkspaces(s.a, s.both())
	s.Require().NoError(err)
	got, _, err := ReconcileWorkspaces(s.b, s.both()[:1])
	s.Require().NoError(err)
	s.Nil(got)
	s.gone(s.b, "magus")
	s.gone(s.a, "buzzed")
}

func (s *ReconcileWorkspacesSuite) TestAnUnlistedRootMakesNothing() {
	_, lines, err := ReconcileWorkspaces(s.b, nil)
	s.Require().NoError(err)
	s.Empty(lines)
	s.NoDirExists(filepath.Join(s.b, ".magus"))
}

func (s *ReconcileWorkspacesSuite) TestAMissingCheckoutRefusesWithMGS1054() {
	entries := append(s.both(), ListedEntry{Name: "gone", Root: filepath.Join(s.b, "..", "gone"), Where: "u.yaml:7"})
	_, _, err := ReconcileWorkspaces(s.a, entries)
	s.Require().ErrorIs(err, types.WorkspaceMissing)
	s.Contains(err.Error(), "which does not exist; delete the entry at u.yaml:7")
}

func (s *ReconcileWorkspacesSuite) TestAPlainDirectoryInTheWayRefusesWithMGS1058() {
	s.Require().NoError(os.MkdirAll(s.at(s.a, "buzzed"), 0o755))
	_, _, err := ReconcileWorkspaces(s.a, s.both())
	s.Require().ErrorIs(err, types.WorkspacesNotALink)
	s.Contains(err.Error(), "move it away")
	s.DirExists(s.at(s.a, "buzzed"), "nothing magus did not make is deleted")
}

func (s *ReconcileWorkspacesSuite) TestAMovedEntryIsRetargeted() {
	_, _, err := ReconcileWorkspaces(s.a, s.both())
	s.Require().NoError(err)
	moved := s.workspace(s.T().TempDir(), "b2")
	_, lines, err := ReconcileWorkspaces(s.a, []ListedEntry{{Name: "magus", Root: s.a}, {Name: "buzzed", Root: moved}})
	s.Require().NoError(err)
	s.Equal(moved, s.target(s.a, "buzzed"))
	s.Equal(s.a, s.target(moved, "magus"))
	s.gone(s.b, "magus")
	s.NotEmpty(lines)
}
