package guard

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// CommandPushSuite pins what req.Push says about each shape of line, against real git
// repositories and, for the tools that need none installed, a driver answering as asked.
type CommandPushSuite struct {
	suite.Suite
	repo *testkit.Repo
	head string
}

func TestCommandPushSuite(t *testing.T) { suite.Run(t, new(CommandPushSuite)) }

func (s *CommandPushSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.repo = testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n", "sub/b.txt": "b\n"})
	s.head = s.repo.Head()
}

func (s *CommandPushSuite) read(deps Dependencies, line string) *types.CommandPush {
	at := location{dir: s.repo.Root(), workspace: s.repo.Root()}
	return commandPush(s.T().Context(), deps, commandRuleInput{command: line, dialect: DialectBash}, at)
}

// driverAnswering is a VCS driver of the named tool whose FindCommit answers id for any
// revision, and records the ones asked.
func (s *CommandPushSuite) driverAnswering(name, id string, asked *[]string) types.VCSDriver {
	driver := typestest.NewMockVCSDriver(s.T())
	driver.EXPECT().Name().Return(name).Maybe()
	driver.EXPECT().Base().Return("main").Maybe()
	driver.EXPECT().FindCommit(mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, _, rev string) (types.Commit, error) {
			*asked = append(*asked, rev)
			return types.Commit{ID: id}, nil
		}).Maybe()
	return driver
}

func (s *CommandPushSuite) TestALineThatPushesNothingHasNoPush() {
	s.Nil(s.read(Dependencies{}, "ls"))
}

func (s *CommandPushSuite) TestAGitPushNamesTheCurrentRevision() {
	got := s.read(Dependencies{}, "git push")
	s.Require().NotNil(got)
	s.Equal(types.CommandPush{Tool: "git", Dir: s.repo.Root(), Revision: s.head}, *got)
}

func (s *CommandPushSuite) TestAHeadRefspecNamesTheCurrentRevision() {
	got := s.read(Dependencies{}, "git push origin HEAD:topic")
	s.Require().NotNil(got)
	s.Equal(types.CommandPush{Tool: "git", Dir: s.repo.Root(), Revision: s.head}, *got)
}

func (s *CommandPushSuite) TestARefspecSourceIsTheRevision() {
	s.repo.Git("branch", "feature")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, "git push origin feature:main")
	s.Require().NotNil(got)
	s.Equal("feature", got.Source)
	s.Equal(s.head, got.Revision)
	s.Empty(got.Unknown)
}

func (s *CommandPushSuite) TestGitDashCPushesFromTheCheckoutItNames() {
	other := testkit.NewRepository(s.T(), map[string]string{"o.txt": "o\n"})
	got := s.read(Dependencies{}, "git -C "+other.Root()+" push")
	s.Require().NotNil(got)
	s.Equal(other.Root(), got.Dir)
	s.Equal(other.Head(), got.Revision)
}

func (s *CommandPushSuite) TestACdBeforeThePushMovesTheDir() {
	got := s.read(Dependencies{}, "cd sub && git push")
	s.Require().NotNil(got)
	s.Equal(filepath.Join(s.repo.Root(), "sub"), got.Dir)
	s.Equal(s.head, got.Revision)
}

func (s *CommandPushSuite) TestTwoRefspecsAreUnknown() {
	got := s.read(Dependencies{}, "git push origin a b")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "more than one refspec")
}

func (s *CommandPushSuite) TestPushAllIsUnknown() {
	for _, flag := range []string{"--all", "--branches", "--mirror", "--tags"} {
		got := s.read(Dependencies{}, "git push "+flag)
		s.Require().NotNil(got, flag)
		s.Empty(got.Revision, flag)
		s.Contains(got.Unknown, "`git push "+flag+"` publishes more than one revision")
	}
}

func (s *CommandPushSuite) TestADeletionPublishesNothing() {
	s.Nil(s.read(Dependencies{}, "git push origin --delete old"))
	s.Nil(s.read(Dependencies{}, "git push origin :old"))
}

func (s *CommandPushSuite) TestTheFirstPublishingPushIsReadPastADeletion() {
	got := s.read(Dependencies{}, "git push origin :old; git push")
	s.Require().NotNil(got)
	s.Equal(s.head, got.Revision)
}

func (s *CommandPushSuite) TestTwoPublishingPushesAreUnknown() {
	got := s.read(Dependencies{}, "git push origin a:x; git push origin b:y")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "publishes 2 times")
}

func (s *CommandPushSuite) TestACommitBeforeThePushIsUnknownNamingTheCommit() {
	got := s.read(Dependencies{}, "git commit -m x && git push")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "this line runs `git commit` before it pushes")
}

func (s *CommandPushSuite) TestAnUnplacedPushIsUnknownNotAbsent() {
	got := s.read(Dependencies{}, `cd "$X" && git push`)
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "known only by running the line")
}

func (s *CommandPushSuite) TestAnUnplacedPushAfterAPullRequestEditIsUnknown() {
	got := s.read(Dependencies{}, `gh pr edit 1 -b x; cd "$X" && git push`)
	s.Require().NotNil(got)
	s.Contains(got.Unknown, "known only by running the line")
}

func (s *CommandPushSuite) TestAnUnplacedPushAfterADeletionIsUnknown() {
	got := s.read(Dependencies{}, `git push origin :old; cd "$X" && git push`)
	s.Require().NotNil(got)
	s.Contains(got.Unknown, "known only by running the line")
}

func (s *CommandPushSuite) TestARefspecThatIsNotLiteralIsUnknown() {
	got := s.read(Dependencies{}, `git push origin "$BRANCH"`)
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "not literal")
}

func (s *CommandPushSuite) TestAPushInAScriptNamingARefspecIsUnknownOrRead() {
	s.repo.Git("branch", "feature")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, `sh -c 'git push origin feature:main'`)
	s.Require().NotNil(got)
	s.Equal("feature", got.Source)
	s.Equal(s.head, got.Revision)
	two := s.read(Dependencies{}, `sh -c 'git push origin a b'`)
	s.Require().NotNil(two)
	s.Contains(two.Unknown, "more than one refspec")
}

func (s *CommandPushSuite) TestAnUnresolvableSourceIsUnknownWithTheError() {
	got := s.read(Dependencies{}, "git push origin nosuch:main")
	s.Require().NotNil(got)
	s.Equal("nosuch", got.Source)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "`nosuch` names no commit in `"+s.repo.Root()+"`")
}

func (s *CommandPushSuite) TestNoVersionControlIsUnknown() {
	off := false
	got := s.read(Dependencies{VCS: types.VCSOptions{Enabled: &off}}, "git push")
	s.Require().NotNil(got)
	s.Contains(got.Unknown, "no version control answers in `"+s.repo.Root()+"`")
}

func (s *CommandPushSuite) TestGhPrCreateOpensAPullRequestAtTheCurrentRevision() {
	got := s.read(Dependencies{}, "gh pr create --title x")
	s.Require().NotNil(got)
	s.Equal(types.CommandPush{Tool: "gh", Dir: s.repo.Root(), Revision: s.head, OpensPullRequest: true}, *got)
}

func (s *CommandPushSuite) TestGhPrCreateHeadNamesItsRevision() {
	s.repo.Git("branch", "topic")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, "gh pr create --head topic")
	s.Require().NotNil(got)
	s.Equal("topic", got.Source)
	s.Equal(s.head, got.Revision)
}

func (s *CommandPushSuite) TestGhPrCreateHeadOfAnotherRepositoryIsUnknown() {
	got := s.read(Dependencies{}, "gh pr create --head owner:topic")
	s.Require().NotNil(got)
	s.True(got.OpensPullRequest)
	s.Contains(got.Unknown, "branch of another repository")
}

func (s *CommandPushSuite) TestJjGitPushWithABookmarkNamesItsCommit() {
	var asked []string
	deps := Dependencies{VCS: types.VCSOptions{Driver: s.driverAnswering("jj", "jjcommit", &asked)}}
	got := s.read(deps, "jj git push -b feature")
	s.Require().NotNil(got)
	s.Equal(types.CommandPush{Tool: "jj", Dir: s.repo.Root(), Source: "feature", Revision: "jjcommit"}, *got)
	s.Equal([]string{"feature"}, asked)
}

func (s *CommandPushSuite) TestBareJjGitPushIsUnknown() {
	for _, line := range []string{"jj git push", "jj git push --all", "jj git push -b a -b b"} {
		got := s.read(Dependencies{}, line)
		s.Require().NotNil(got, line)
		s.Empty(got.Revision, line)
		s.NotEmpty(got.Unknown, line)
	}
}

func (s *CommandPushSuite) TestAJjDeletionPublishesNothing() {
	s.Nil(s.read(Dependencies{}, "jj git push --deleted"))
}

func (s *CommandPushSuite) TestHgPushNamesItsRevisionOrIsUnknown() {
	var asked []string
	deps := Dependencies{VCS: types.VCSOptions{Driver: s.driverAnswering("hg", "hgcommit", &asked)}}
	got := s.read(deps, "hg push -r abc123")
	s.Require().NotNil(got)
	s.Equal("abc123", got.Source)
	s.Equal("hgcommit", got.Revision)

	bare := s.read(deps, "hg push")
	s.Require().NotNil(bare)
	s.Contains(bare.Unknown, "publishes every outgoing head")
}

func (s *CommandPushSuite) TestSlPushToPublishesTheCurrentRevision() {
	var asked []string
	deps := Dependencies{VCS: types.VCSOptions{Driver: s.driverAnswering("sl", "slcommit", &asked)}}
	got := s.read(deps, "sl push --to main")
	s.Require().NotNil(got)
	s.Empty(got.Source, "--to names the remote bookmark, not the source")
	s.Equal("slcommit", got.Revision)
	s.Equal([]string{""}, asked)

	named := s.read(deps, "sl push -r abc --to main")
	s.Require().NotNil(named)
	s.Equal("abc", named.Source)
}

func (s *CommandPushSuite) TestARangeSelectorIsUnknown() {
	got := s.read(Dependencies{}, "jj git push -r 'a::b'")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "not one commit")
}

func (s *CommandPushSuite) TestAnUnreadableCurrentRevisionIsUnknown() {
	driver := typestest.NewMockVCSDriver(s.T())
	driver.EXPECT().Name().Return("git").Maybe()
	driver.EXPECT().Base().Return("main").Maybe()
	driver.EXPECT().FindCommit(mock.Anything, mock.Anything, "").Return(types.Commit{}, errors.New("no commits yet"))
	got := s.read(Dependencies{VCS: types.VCSOptions{Driver: driver}}, "git push")
	s.Require().NotNil(got)
	s.Contains(got.Unknown, "the current revision of `"+s.repo.Root()+"` is unread: no commits yet")
}

func (s *CommandPushSuite) TestNoCommandRuleReadsNoRevision() {
	driver := typestest.NewMockVCSDriver(s.T())
	driver.EXPECT().Name().Return("git").Maybe()
	driver.EXPECT().Base().Return("main").Maybe()
	at := location{dir: s.repo.Root(), workspace: s.repo.Root()}
	in := commandRuleInput{command: "git push", dialect: DialectBash}

	verdict, _ := gradeWorkspaceCommand(s.T().Context(), Dependencies{VCS: types.VCSOptions{Driver: driver}},
		types.GuardJudgment{Decision: decisionPass}, in, hookAttribution{}, at)
	s.Equal(decisionPass, verdict.Decision)
	driver.AssertNotCalled(s.T(), "FindCommit", mock.Anything, mock.Anything, mock.Anything)
}

func (s *CommandPushSuite) TestTheCommandRuleIsHandedThePush() {
	probe := &commandRuleProbe{}
	at := location{dir: s.repo.Root(), workspace: s.repo.Root()}
	in := commandRuleInput{command: "git push", dialect: DialectBash}

	gradeWorkspaceCommand(s.T().Context(), Dependencies{CommandRule: probe.rule()},
		types.GuardJudgment{Decision: decisionPass}, in, hookAttribution{}, at)
	s.Require().Len(probe.asked, 1)
	s.Require().NotNil(probe.asked[0].Push)
	s.Equal(s.head, probe.asked[0].Push.Revision)
}
