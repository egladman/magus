package changeset

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// namedDriver is the built-in driver called name, for text that needs only its
// spellings and starts no process.
func namedDriver(t *testing.T, name string) types.VCSDriver { //nolint:ireturn // the driver is picked by name
	t.Helper()
	enabled := true
	res, err := vcs.Resolve(context.Background(), "", "", types.VCSOptions{Name: name, Enabled: &enabled})
	require.NoError(t, err)
	require.NotNil(t, res.VCS)
	return res.VCS
}

type RegenerationOwedSuite struct {
	suite.Suite
	repo *testkit.Repo
	base string
}

func TestRegenerationOwedSuite(t *testing.T) { suite.Run(t, new(RegenerationOwedSuite)) }

// SetupTest publishes the base, so a commit on top reads as unpushed rather than as
// one whose push status nothing can tell.
func (s *RegenerationOwedSuite) SetupTest() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{"README": "r\n"})
	s.base = s.repo.Head()
	remote := s.T().TempDir()
	s.repo.Git("init", "--bare", remote)
	s.repo.Git("remote", "add", "origin", remote)
	s.repo.Git("push", "-u", "origin", "HEAD")
}

func (s *RegenerationOwedSuite) owed() OwedVerdict {
	classify := fixedClassify([]types.FileEntry{{Path: "api.proto", Role: "source", SourceOf: []string{"api"}}})
	got, err := RegenerationOwed(s.T().Context(), s.repo.Root(), resolveGitDriver(s.T(), s.repo.Root()), s.base, classify, func(string) bool { return true })
	s.Require().NoError(err)
	return got
}

func (s *RegenerationOwedSuite) TestATipThatChangedAnInputAmends() {
	changed := s.repo.Commit("change the input", map[string]string{"api.proto": "syntax\n"})
	got := s.owed()
	s.Equal([]string{"api"}, got.Owed.Projects)
	s.Contains(got.Notice, "[MGS4006] the range "+s.base+"..")
	s.Contains(got.Notice, "magus run generate:rw api")
	s.Equal("Then fold it into "+changed+" (unpushed, still the tip):\n  git commit --amend --no-edit", got.Fold)
}

func (s *RegenerationOwedSuite) TestAnInputBelowTheTipTakesAFixup() {
	changed := s.repo.Commit("change the input", map[string]string{"api.proto": "syntax\n"})
	s.repo.Commit("on top", map[string]string{"notes.txt": "n\n"})
	got := s.owed()
	s.Contains(got.Fold, "git commit --fixup="+changed)
}

func (s *RegenerationOwedSuite) TestAPushedInputTakesAFollowUp() {
	changed := s.repo.Commit("change the input", map[string]string{"api.proto": "syntax\n"})
	s.repo.Git("push", "origin", "HEAD")
	got := s.owed()
	s.Equal(changed+" is already pushed; do not amend or rebase published history. Commit the fix as a new, follow-up commit instead.", got.Fold)
}

func (s *RegenerationOwedSuite) TestARangeThatOwesNothingSaysNothing() {
	s.repo.Commit("unrelated", map[string]string{"notes.txt": "n\n"})
	got, err := RegenerationOwed(s.T().Context(), s.repo.Root(), resolveGitDriver(s.T(), s.repo.Root()), s.base, fixedClassify(nil), func(string) bool { return true })
	s.Require().NoError(err)
	s.Empty(got.Owed.Projects)
	s.Empty(got.Notice)
}

func (s *RegenerationOwedSuite) TestAnUnreadableBaseIsAnError() {
	_, err := RegenerationOwed(s.T().Context(), s.repo.Root(), resolveGitDriver(s.T(), s.repo.Root()), "no-such-base", fixedClassify(nil), func(string) bool { return true })
	s.Error(err)
}

// FoldTextSuite pins that no backend's repair line spells another backend's command.
type FoldTextSuite struct{ suite.Suite }

func TestFoldTextSuite(t *testing.T) { suite.Run(t, new(FoldTextSuite)) }

func (s *FoldTextSuite) TestHgAmendsTheTip() {
	s.Equal("Then fold it into n1 (unpushed, still the tip):\n  hg commit --amend", foldLine(namedDriver(s.T(), "hg"), "n1", driftUnpushedHead))
}

func (s *FoldTextSuite) TestHgSaysItCannotFoldBelowTheTip() {
	got := foldLine(namedDriver(s.T(), "hg"), "n1", driftUnpushedNotHead)
	s.Equal("Then fold it into n1 (unpushed, but no longer the tip). hg has no single command that folds below the tip: fold it by hand, or commit the fix as a follow-up commit.", got)
}

func (s *FoldTextSuite) TestSaplingAmendsBelowTheTip() {
	s.Equal("Then fold it into n1 (unpushed, but no longer the tip):\n  sl amend --to n1", foldLine(namedDriver(s.T(), "sl"), "n1", driftUnpushedNotHead))
}

func (s *FoldTextSuite) TestJJSquashes() {
	s.Equal("Then fold it into kx (unpushed, but no longer the tip):\n  jj squash --into kx", foldLine(namedDriver(s.T(), "jj"), "kx", driftUnpushedNotHead))
}

func (s *FoldTextSuite) TestNoNonGitNoticeNamesGit() {
	got := buildDriftNotice(namedDriver(s.T(), "jj"), "the range main@origin..kx", "kx", []string{"api"}, driftUnpushedHead)
	s.NotContains(got, "git ")
}
