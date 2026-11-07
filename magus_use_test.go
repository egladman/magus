package magus

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

type UseLinksSuite struct {
	suite.Suite
	root, used string
}

func TestUseLinksSuite(t *testing.T) { suite.Run(t, new(UseLinksSuite)) }

func (s *UseLinksSuite) SetupTest() {
	parent := s.T().TempDir()
	s.root, s.used = filepath.Join(parent, "b"), filepath.Join(parent, "a")
	for _, dir := range []string{s.root, s.used} {
		s.Require().NoError(os.MkdirAll(dir, 0o755))
		s.Require().NoError(os.WriteFile(filepath.Join(dir, "magus.yaml"), nil, 0o644))
	}
}

func (s *UseLinksSuite) link(name string) string {
	return filepath.Join(s.root, filepath.FromSlash(types.UseLinkDir), types.UseRef(name, ""))
}

func (s *UseLinksSuite) TestAnEntryIsLinkedUnderItsReference() {
	uses, err := resolveUses(s.root, []config.UseEntry{{Name: "a", Path: "../a"}})
	s.Require().NoError(err)
	s.Equal([]types.UsedWorkspace{{Name: "a", Root: s.used, Share: []types.ShareKind{types.ShareLint}}}, uses)
	got, err := os.Readlink(s.link("a"))
	s.Require().NoError(err)
	s.Equal(s.used, got)
}

func (s *UseLinksSuite) TestASecondLoadChangesNothing() {
	entries := []config.UseEntry{{Name: "a", Path: "../a"}}
	_, err := resolveUses(s.root, entries)
	s.Require().NoError(err)
	before, err := os.Lstat(s.link("a"))
	s.Require().NoError(err)
	_, err = resolveUses(s.root, entries)
	s.Require().NoError(err)
	after, err := os.Lstat(s.link("a"))
	s.Require().NoError(err)
	s.True(os.SameFile(before, after), "the link is kept, not remade")
}

func (s *UseLinksSuite) TestAnEntryRemovedDropsItsLink() {
	_, err := resolveUses(s.root, []config.UseEntry{{Name: "a", Path: "../a"}})
	s.Require().NoError(err)
	_, err = resolveUses(s.root, nil)
	s.Require().NoError(err)
	_, err = os.Lstat(s.link("a"))
	s.True(os.IsNotExist(err))
}

func (s *UseLinksSuite) TestAnAbsentWorkspaceRefusesTheLoad() {
	_, err := resolveUses(s.root, []config.UseEntry{{Name: "a", Path: "../gone"}})
	s.Require().ErrorIs(err, types.UseWorkspaceUnavailable)
}
