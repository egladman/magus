//go:build !wasm

package std

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/unverified"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// unverifiedWorkspace is the workspace a magusfile target's context holds, over a root and a
// store directory.
type unverifiedWorkspace struct {
	types.WorkspaceRepository
	root, cacheDir string
}

func (w unverifiedWorkspace) Root() string               { return w.root }
func (w unverifiedWorkspace) CacheDir() string           { return w.cacheDir }
func (unverifiedWorkspace) VCSOptions() types.VCSOptions { return types.VCSOptions{} }

type unverifiedMemberSuite struct{ suite.Suite }

func TestUnverifiedMember(t *testing.T) { suite.Run(t, new(unverifiedMemberSuite)) }

func (s *unverifiedMemberSuite) TestAnEmptyRevisionRaises() {
	_, err := MagusWorkspaceUnverified(s.T().Context(), types.MagusWorkspace{Dir: "/w"}, "")
	s.Require().ErrorContains(err, "magus\\workspace(`/w`).unverified: revision is empty, pass the commit to read, such as req.push.revision")
}

func (s *unverifiedMemberSuite) TestOutsideAWorkspaceRaisesMGS1022() {
	_, err := MagusWorkspaceUnverified(s.T().Context(), types.MagusWorkspace{Dir: "/w"}, "abc")
	s.Require().ErrorIs(err, types.MagusfileOnlyMember)
}

func (s *unverifiedMemberSuite) TestThePinnedReaderAnswers() {
	var asked [2]string
	read := func(_ context.Context, dir, revision string) (types.UnverifiedReport, error) {
		asked = [2]string{dir, revision}
		return types.UnverifiedReport{Revision: revision, Changed: []string{"a.go"}}, nil
	}
	ctx := unverified.WithReader(s.T().Context(), read)

	got, err := MagusWorkspaceUnverified(ctx, types.MagusWorkspace{Dir: "/other"}, "abc")
	s.Require().NoError(err)
	s.Equal(types.UnverifiedReport{Revision: "abc", Changed: []string{"a.go"}}, got)
	s.Equal([2]string{"/other", "abc"}, asked)
}

func (s *unverifiedMemberSuite) TestADirOutsideTheWorkspaceRaises() {
	root, other := s.T().TempDir(), s.T().TempDir()
	ctx := types.WithWorkspace(s.T().Context(), unverifiedWorkspace{root: root, cacheDir: s.T().TempDir()})

	_, err := MagusWorkspaceUnverified(ctx, types.MagusWorkspace{Root: root, Dir: other}, "abc")
	s.Require().ErrorContains(err, "is outside this workspace")
}

func (s *unverifiedMemberSuite) TestAWorkspaceWithoutACacheDirectoryRaises() {
	ctx := types.WithWorkspace(s.T().Context(), checkoutWorkspace{})
	_, err := MagusWorkspaceUnverified(ctx, types.MagusWorkspace{Dir: "."}, "abc")
	s.Require().ErrorContains(err, "no cache directory")
}
