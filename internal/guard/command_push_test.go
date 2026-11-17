package guard

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// pushLine is a real git repository at a known head, against which a test reads what
// req.Push says about one line.
type pushLine struct {
	suite.Suite
	repo *testkit.Repo
	head string
}

// CommandPushSuite pins what req.Push says about each shape of git push's own arguments.
type CommandPushSuite struct{ pushLine }

// PushLineShapeSuite pins req.Push over the line around a push: where it runs, what runs
// before it, and words only running the line reveals.
type PushLineShapeSuite struct{ pushLine }

// PullRequestPushSuite pins req.Push for gh pr create, alone and after a push.
type PullRequestPushSuite struct{ pushLine }

// OtherBackendPushSuite pins req.Push for jj, hg and sl, against a driver answering as asked
// so none needs installing.
type OtherBackendPushSuite struct{ pushLine }

func TestCommandPushSuite(t *testing.T) { suite.Run(t, new(CommandPushSuite)) }

func TestPushLineShapeSuite(t *testing.T) { suite.Run(t, new(PushLineShapeSuite)) }

func TestPullRequestPushSuite(t *testing.T) { suite.Run(t, new(PullRequestPushSuite)) }

func TestOtherBackendPushSuite(t *testing.T) { suite.Run(t, new(OtherBackendPushSuite)) }

func (s *pushLine) SetupTest() {
	testkit.Isolate(s.T())
	s.repo = testkit.NewRepository(s.T(), map[string]string{"a.txt": "a\n", "sub/b.txt": "b\n"})
	s.head = s.repo.Head()
}

func (s *pushLine) read(deps Dependencies, line string) *types.CommandPush {
	at := location{dir: s.repo.Root(), workspace: s.repo.Root()}
	return commandPush(s.T().Context(), deps, commandRuleInput{command: line, dialect: DialectBash}, at)
}

// driverAnswering is a VCS driver of the named tool whose FindCommit answers id for any
// revision, and records the ones asked.
func (s *pushLine) driverAnswering(name, id string, asked *[]string) *typestest.MockVCSDriver {
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

func (s *PushLineShapeSuite) TestGitDashCPushesFromTheCheckoutItNames() {
	other := testkit.NewRepository(s.T(), map[string]string{"o.txt": "o\n"})
	got := s.read(Dependencies{}, fmt.Sprintf("git -C %s push", other.Root()))
	s.Require().NotNil(got)
	s.Equal(other.Root(), got.Dir)
	s.Equal(other.Head(), got.Revision)
}

func (s *PushLineShapeSuite) TestACdBeforeThePushMovesTheDir() {
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

// many asserts line publishes more than one revision, as flag makes it.
func (s *pushLine) many(line, flag string) {
	got := s.read(Dependencies{}, line)
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, fmt.Sprintf("`git push %s` publishes more than one revision", flag))
}

func (s *CommandPushSuite) TestPushAllIsUnknown() { s.many("git push --all", "--all") }

func (s *CommandPushSuite) TestPushBranchesIsUnknown() { s.many("git push --branches", "--branches") }

func (s *CommandPushSuite) TestPushMirrorIsUnknown() { s.many("git push --mirror", "--mirror") }

func (s *CommandPushSuite) TestPushTagsIsUnknown() { s.many("git push --tags", "--tags") }

func (s *CommandPushSuite) TestADeleteFlagPublishesNothing() {
	s.Nil(s.read(Dependencies{}, "git push origin --delete old"))
}

func (s *CommandPushSuite) TestADeletingRefspecPublishesNothing() {
	s.Nil(s.read(Dependencies{}, "git push origin :old"))
}

func (s *CommandPushSuite) TestABareColonRefspecIsUnknown() {
	got := s.read(Dependencies{}, "git push origin :")
	s.Require().NotNil(got, "a bare : pushes every matching branch")
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "pushes every branch that matches one on the remote")
}

func (s *CommandPushSuite) TestAForcedBareColonRefspecIsUnknown() {
	got := s.read(Dependencies{}, "git push origin +:")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "pushes every branch that matches one on the remote")
}

func (s *CommandPushSuite) TestANewUpstreamBranchNamesItsRevision() {
	s.repo.Git("branch", "topic")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, "git push -u origin topic")
	s.Require().NotNil(got)
	s.Equal(types.CommandPush{Tool: "git", Dir: s.repo.Root(), Source: "topic", Revision: s.head}, *got)
}

func (s *CommandPushSuite) TestAForceWithLeasePushNamesItsRefspec() {
	s.repo.Git("branch", "topic")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, "git push --force-with-lease origin topic")
	s.Require().NotNil(got)
	s.Equal("topic", got.Source)
	s.Equal(s.head, got.Revision)
}

func (s *CommandPushSuite) TestAForcedRefspecNamesItsSource() {
	s.repo.Git("branch", "feature")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, "git push origin +feature:main")
	s.Require().NotNil(got)
	s.Equal("feature", got.Source)
	s.Equal(s.head, got.Revision)
}

func (s *PushLineShapeSuite) TestAPushInsideASubstitutionIsUnknown() {
	got := s.read(Dependencies{}, `out=$(git push origin HEAD:x); echo "$out"`)
	s.Require().NotNil(got, "the push still runs")
	s.Empty(got.Revision)
	s.Equal("`git push` runs inside a command substitution, whose words magus does not place before the line runs", got.Unknown)
}

func (s *CommandPushSuite) TestAPushOptionsValueIsNotADryRun() {
	got := s.read(Dependencies{}, "git push -o --dry-run origin HEAD:topic")
	s.Require().NotNil(got, "--dry-run is -o's value, sent to the server")
	s.Equal(s.head, got.Revision)
}

func (s *CommandPushSuite) TestADryRunPublishesNothing() {
	s.Nil(s.read(Dependencies{}, "git push --dry-run origin HEAD:topic"))
}

func (s *CommandPushSuite) TestGitPushDashNPublishesNothing() {
	s.Nil(s.read(Dependencies{}, "git push -n origin HEAD:topic"))
}

func (s *CommandPushSuite) TestPushHelpPublishesNothing() {
	s.Nil(s.read(Dependencies{}, "git push --help"))
}

func (s *PullRequestPushSuite) TestPullRequestHelpPublishesNothing() {
	s.Nil(s.read(Dependencies{}, "gh pr create --help"))
}

func (s *PullRequestPushSuite) TestAPushThenAPullRequestForItsBranchIsOnePublication() {
	s.repo.Git("branch", "topic")
	got := s.read(Dependencies{}, "git push -u origin topic && gh pr create --head topic --title x --body y")
	s.Require().NotNil(got)
	s.Equal(types.CommandPush{Tool: "git", Dir: s.repo.Root(), Source: "topic", Revision: s.head, OpensPullRequest: true}, *got)
}

func (s *PullRequestPushSuite) TestAPushThenAPullRequestForAnotherCommitIsUnknown() {
	s.repo.Git("branch", "older")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, "git push -u origin older && gh pr create --title x")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "publishes 2 times (`git push`, `gh pr create`) and they do not name one commit")
}

func (s *PullRequestPushSuite) TestAHeredocBodyIsNotAWordThatDecidesWhatIsPublished() {
	got := s.read(Dependencies{}, "gh pr create --title x --body \"$(cat <<'EOF'\nWhy it is.\nEOF\n)\"")
	s.Require().NotNil(got)
	s.Empty(got.Unknown)
	s.Equal(s.head, got.Revision)
}

func (s *PullRequestPushSuite) TestABodyOfDashHIsNotHelp() {
	got := s.read(Dependencies{}, "gh pr create --title x --body '-h'")
	s.Require().NotNil(got)
	s.Equal(s.head, got.Revision)
}

func (s *PullRequestPushSuite) TestABodyOfDryRunIsNotADryRun() {
	got := s.read(Dependencies{}, "gh pr create --body --dry-run --title x")
	s.Require().NotNil(got)
	s.Equal(s.head, got.Revision)
}

func (s *PullRequestPushSuite) TestABodyStartingWithADashIsTheBody() {
	got := s.read(Dependencies{}, "gh pr create --title x --body '- Handles the push'")
	s.Require().NotNil(got)
	s.Empty(got.Unknown)
	s.Empty(got.Source, "the body's H is not a --head")
	s.Equal(s.head, got.Revision)
}

func (s *PullRequestPushSuite) TestAShortHeadInAClusterNamesItsRevision() {
	s.repo.Git("branch", "topic")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, "gh pr create -dH topic --title x")
	s.Require().NotNil(got)
	s.Equal("topic", got.Source)
	s.Equal(s.head, got.Revision)
}

func (s *PushLineShapeSuite) TestAScriptPushWithAnUnsetWordIsUnknown() {
	got := s.read(Dependencies{}, `sh -c 'git push $ARGS'`)
	s.Require().NotNil(got)
	s.Empty(got.Revision, "an empty rendered word is not the current revision")
	s.Contains(got.Unknown, "has a word that is not literal")
}

func (s *PushLineShapeSuite) TestTheFirstPublishingPushIsReadPastADeletion() {
	got := s.read(Dependencies{}, "git push origin :old; git push")
	s.Require().NotNil(got)
	s.Equal(s.head, got.Revision)
}

func (s *PushLineShapeSuite) TestTwoPublishingPushesAreUnknown() {
	got := s.read(Dependencies{}, "git push origin a:x; git push origin b:y")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "publishes 2 times")
}

func (s *PushLineShapeSuite) TestACommitBeforeThePushIsUnknownNamingTheCommit() {
	got := s.read(Dependencies{}, "git commit -m x && git push")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "this line runs `git commit` before it pushes")
}

func (s *PushLineShapeSuite) TestAnUnplacedPushIsUnknownNotAbsent() {
	got := s.read(Dependencies{}, `cd "$X" && git push`)
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "known only by running the line")
}

func (s *PushLineShapeSuite) TestAnUnplacedPushAfterAPullRequestEditIsUnknown() {
	got := s.read(Dependencies{}, `gh pr edit 1 -b x; cd "$X" && git push`)
	s.Require().NotNil(got)
	s.Contains(got.Unknown, "known only by running the line")
}

func (s *PushLineShapeSuite) TestAnUnplacedPushAfterADeletionIsUnknown() {
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

func (s *PushLineShapeSuite) TestAPushInAScriptNamesItsRefspec() {
	s.repo.Git("branch", "feature")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, `sh -c 'git push origin feature:main'`)
	s.Require().NotNil(got)
	s.Equal("feature", got.Source)
	s.Equal(s.head, got.Revision)
}

func (s *PushLineShapeSuite) TestAPushInAScriptNamingTwoRefspecsIsUnknown() {
	got := s.read(Dependencies{}, `sh -c 'git push origin a b'`)
	s.Require().NotNil(got)
	s.Contains(got.Unknown, "more than one refspec")
}

func (s *CommandPushSuite) TestAnUnresolvableSourceIsUnknownWithTheError() {
	got := s.read(Dependencies{}, "git push origin nosuch:main")
	s.Require().NotNil(got)
	s.Equal("nosuch", got.Source)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, fmt.Sprintf("`nosuch` names no commit in `%s`", s.repo.Root()))
}

func (s *CommandPushSuite) TestNoVersionControlIsUnknown() {
	off := false
	got := s.read(Dependencies{VCS: types.VCSOptions{Enabled: &off}}, "git push")
	s.Require().NotNil(got)
	s.Contains(got.Unknown, fmt.Sprintf("no version control answers in `%s`", s.repo.Root()))
}

func (s *PullRequestPushSuite) TestGhPrCreateOpensAPullRequestAtTheCurrentRevision() {
	got := s.read(Dependencies{}, "gh pr create --title x")
	s.Require().NotNil(got)
	s.Equal(types.CommandPush{Tool: "gh", Dir: s.repo.Root(), Revision: s.head, OpensPullRequest: true}, *got)
}

func (s *PullRequestPushSuite) TestGhPrCreateHeadNamesItsRevision() {
	s.repo.Git("branch", "topic")
	s.repo.Commit("later", map[string]string{"a.txt": "later\n"})
	got := s.read(Dependencies{}, "gh pr create --head topic")
	s.Require().NotNil(got)
	s.Equal("topic", got.Source)
	s.Equal(s.head, got.Revision)
}

func (s *PullRequestPushSuite) TestGhPrCreateHeadOfAnotherRepositoryIsUnknown() {
	got := s.read(Dependencies{}, "gh pr create --head owner:topic")
	s.Require().NotNil(got)
	s.True(got.OpensPullRequest)
	s.Contains(got.Unknown, "branch of another repository")
}

func (s *OtherBackendPushSuite) TestJjGitPushWithABookmarkNamesItsCommit() {
	var asked []string
	deps := Dependencies{VCS: types.VCSOptions{Driver: s.driverAnswering("jj", "jjcommit", &asked)}}
	got := s.read(deps, "jj git push -b feature")
	s.Require().NotNil(got)
	s.Equal(types.CommandPush{Tool: "jj", Dir: s.repo.Root(), Source: "feature", Revision: "jjcommit"}, *got)
	s.Equal([]string{"feature"}, asked)
}

func (s *OtherBackendPushSuite) TestBareJjGitPushIsUnknown() {
	got := s.read(Dependencies{}, "jj git push")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "with no -r, -c or -b publishes bookmarks")
}

func (s *OtherBackendPushSuite) TestJjGitPushAllIsUnknown() {
	got := s.read(Dependencies{}, "jj git push --all")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "`jj git push --all` publishes bookmarks")
}

func (s *OtherBackendPushSuite) TestJjGitPushOfTwoBookmarksIsUnknown() {
	got := s.read(Dependencies{}, "jj git push -b a -b b")
	s.Require().NotNil(got)
	s.Empty(got.Revision)
	s.Contains(got.Unknown, "names more than one of -r, -c and -b")
}

func (s *OtherBackendPushSuite) TestAJjDeletionPublishesNothing() {
	s.Nil(s.read(Dependencies{}, "jj git push --deleted"))
}

func (s *OtherBackendPushSuite) TestHgPushNamesItsRevision() {
	var asked []string
	deps := Dependencies{VCS: types.VCSOptions{Driver: s.driverAnswering("hg", "hgcommit", &asked)}}
	got := s.read(deps, "hg push -r abc123")
	s.Require().NotNil(got)
	s.Equal(types.CommandPush{Tool: "hg", Dir: s.repo.Root(), Source: "abc123", Revision: "hgcommit"}, *got)
}

func (s *OtherBackendPushSuite) TestBareHgPushIsUnknown() {
	var asked []string
	deps := Dependencies{VCS: types.VCSOptions{Driver: s.driverAnswering("hg", "hgcommit", &asked)}}
	got := s.read(deps, "hg push")
	s.Require().NotNil(got)
	s.Contains(got.Unknown, "publishes every outgoing head")
}

func (s *OtherBackendPushSuite) TestSlPushToPublishesTheCurrentRevision() {
	var asked []string
	deps := Dependencies{VCS: types.VCSOptions{Driver: s.driverAnswering("sl", "slcommit", &asked)}}
	got := s.read(deps, "sl push --to main")
	s.Require().NotNil(got)
	s.Empty(got.Source, "--to names the remote bookmark, not the source")
	s.Equal("slcommit", got.Revision)
	s.Equal([]string{""}, asked)
}

func (s *OtherBackendPushSuite) TestSlPushRevNamesItsSource() {
	var asked []string
	deps := Dependencies{VCS: types.VCSOptions{Driver: s.driverAnswering("sl", "slcommit", &asked)}}
	got := s.read(deps, "sl push -r abc --to main")
	s.Require().NotNil(got)
	s.Equal("abc", got.Source)
}

func (s *OtherBackendPushSuite) TestARangeSelectorIsUnknown() {
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
	s.Contains(got.Unknown, fmt.Sprintf("the current revision of `%s` is unread: no commits yet", s.repo.Root()))
}
