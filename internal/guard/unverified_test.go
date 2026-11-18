package guard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// UnverifiedReaderSuite pins the reader a command rule's unverified member calls inside a
// judgment.
type UnverifiedReaderSuite struct {
	suite.Suite
	repo  *testkit.Repo
	base  string
	deps  Dependencies
	opens atomic.Int32
}

func TestUnverifiedReaderSuite(t *testing.T) { suite.Run(t, new(UnverifiedReaderSuite)) }

func (s *UnverifiedReaderSuite) SetupTest() {
	testkit.Isolate(s.T())
	s.opens.Store(0)
	s.repo = s.newCheckout()
	s.base = s.repo.Head()
	project := &types.Project{
		Path: ".", Sources: []types.Glob{{Pattern: "**/*.go"}}, MagusfileTargets: []string{"test"},
		TargetInputs: map[string][]types.InputRef{"test": {{Glob: "**/*.go"}}},
	}
	ws := typestest.NewMockWorkspaceRepository(s.T())
	ws.EXPECT().All().Return([]*types.Project{project}).Maybe()
	ws.EXPECT().Get(".").Return(project).Maybe()
	ws.EXPECT().Get(mock.Anything).Return(nil).Maybe()
	s.deps = Dependencies{
		Inspect: func(context.Context, string) (types.WorkspaceRepository, error) {
			s.opens.Add(1)
			return ws, nil
		},
		CacheDir: func(root string) (string, error) { return filepath.Join(root, ".magus"), nil },
		VCS:      types.VCSOptions{BaseRef: s.base},
	}
}

// newCheckout is a workspace root holding one Go file, its cache directory ignored.
func (s *UnverifiedReaderSuite) newCheckout() *testkit.Repo {
	return testkit.NewRepository(s.T(), map[string]string{".gitignore": ".magus/\n", "magus.yaml": "{}\n", "a.go": "package a\n"})
}

func (s *UnverifiedReaderSuite) passed(repo *testkit.Repo, key, revision string) {
	_, err := cache.NewOutputStore(filepath.Join(repo.Root(), ".magus")).Persist(s.T().Context(), key, []byte("ok\n"), cache.OutputDescriptor{
		Project: ".", Target: "test", Revision: revision, TimestampMs: time.Now().UnixMilli(),
	})
	s.Require().NoError(err)
}

func (s *UnverifiedReaderSuite) TestTheReadIsAnsweredOncePerJudgment() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	read := guardUnverified(s.deps)

	first, err := read(s.T().Context(), s.repo.Root(), head)
	s.Require().NoError(err)
	second, err := read(s.T().Context(), s.repo.Root(), head)
	s.Require().NoError(err)

	s.Equal(first, second)
	s.Equal([]string{"a.go"}, first.Changed)
	s.Equal(int32(1), s.opens.Load(), "the workspace opened once")
}

func (s *UnverifiedReaderSuite) TestTheReadLocatesTheCheckoutHoldingDir() {
	other := s.newCheckout()
	s.deps.VCS = types.VCSOptions{BaseRef: other.Head()}
	head := other.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.passed(other, strings.Repeat("a", 64), head)
	read := guardUnverified(s.deps)

	got, err := read(s.T().Context(), other.Root(), head)
	s.Require().NoError(err)
	s.Empty(got.Unknown)
	s.Empty(got.Unverified, "the run recorded in the other checkout's store reads its file")
}

func (s *UnverifiedReaderSuite) TestALapsedReadIsUnknownAndAPhaseRecordedLapsed() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	release := make(chan struct{})
	s.T().Cleanup(func() { close(release) })
	s.deps.Inspect = func(context.Context, string) (types.WorkspaceRepository, error) {
		<-release
		return nil, os.ErrClosed
	}
	ctx, cancel := context.WithTimeout(s.T().Context(), judgeReserve+100*time.Millisecond)
	defer cancel()
	ctx = withPhaseLog(ctx, journal.Phase{})

	got, err := guardUnverified(s.deps)(ctx, s.repo.Root(), head)
	s.Require().NoError(err)
	// Whichever of the read's share and its own workspace open runs out first words the lapse.
	s.Regexp(`^(weighing the changed files against the recorded runs|opening the workspace to read which targets declare the changed files) (did not finish within|had no time left)`, got.Unknown)
	s.Empty(got.Changed)
	s.True(judgmentPhases(ctx).lapsed(phaseUnverified))
}

func (s *UnverifiedReaderSuite) TestAnEarlierRulesLapsedOpenIsNamedAsThatAndNotAsThisRead() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	ctx := withPhaseLog(s.T().Context(), journal.Phase{})
	judgmentPhases(ctx).record(timedPhase(phaseInspect, 2*time.Second, 2*time.Second, true))

	got, err := guardUnverified(s.deps)(ctx, s.repo.Root(), head)
	s.Require().NoError(err)
	s.Equal("an earlier rule of this judgment opened the workspace, and that open did not finish within 2s, "+
		"so no target's inputs could be read for this push", got.Unknown)
	s.Equal(int32(0), s.opens.Load(), "the judgment opens the workspace once")
	s.False(judgmentPhases(ctx).lapsed(phaseUnverified), "the lapse is the earlier open's")
}

func (s *UnverifiedReaderSuite) TestNoInspectorRaises() {
	s.deps.Inspect = nil
	_, err := guardUnverified(s.deps)(s.T().Context(), s.repo.Root(), s.base)
	s.Require().ErrorContains(err, "this caller cannot open the workspace")
}

func (s *UnverifiedReaderSuite) TestADirNoCheckoutHoldsIsUnknown() {
	got, err := guardUnverified(s.deps)(s.T().Context(), s.T().TempDir(), s.base)
	s.Require().NoError(err)
	s.Contains(got.Unknown, "is unread: fatal: not a git repository")
}

func (s *UnverifiedReaderSuite) TestAnotherRepositoryIsReadAsNoWorkspace() {
	other := testkit.NewRepository(s.T(), map[string]string{"README.md": "other\n"})
	s.deps.VCS = types.VCSOptions{BaseRef: other.Head()}
	head := other.Commit("change", map[string]string{"README.md": "changed\n"})

	got, err := guardUnverified(s.deps)(s.T().Context(), other.Root(), head)
	s.Require().NoError(err)
	s.Empty(got.Unknown)
	s.Equal([]types.UnverifiedFile{{Path: "README.md"}}, got.Unverified, "no target declares it")
	s.True(got.NoWorkspace, "no magus workspace holds the other repository")
}

// UnverifiedBudgetSuite pins unverifiedBudget against a frozen clock.
type UnverifiedBudgetSuite struct{ suite.Suite }

func TestUnverifiedBudgetSuite(t *testing.T) { suite.Run(t, new(UnverifiedBudgetSuite)) }

// budgetWithin is unverifiedBudget of the share with left on the clock, read in a bubble
// whose clock moves only when every goroutine waits.
func (s *UnverifiedBudgetSuite) budgetWithin(left time.Duration, dryRun bool) time.Duration {
	var got time.Duration
	synctest.Test(s.T(), func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), left)
		defer cancel()
		if dryRun {
			ctx = context.WithValue(ctx, dryRunKey{}, true)
		}
		got = unverifiedBudget(ctx, unverifiedShare)
	})
	return got
}

func (s *UnverifiedBudgetSuite) TestAnUnboundedJudgmentGetsTheShare() {
	s.Equal(unverifiedShare, unverifiedBudget(s.T().Context(), unverifiedShare))
}

func (s *UnverifiedBudgetSuite) TestAWholeHookBudgetGetsTheShare() {
	s.Equal(unverifiedShare, s.budgetWithin(hookBudget, false))
}

func (s *UnverifiedBudgetSuite) TestALateStartLeavesTheReserve() {
	s.Equal(time.Second-judgeReserve, s.budgetWithin(time.Second, false))
}

func (s *UnverifiedBudgetSuite) TestNoTimePastTheReserveIsNoBudget() {
	s.Equal(time.Duration(0), s.budgetWithin(judgeReserve/2, false))
}

func (s *UnverifiedBudgetSuite) TestADryRunGetsAllButTheReserve() {
	s.Equal(dryRunBudget-judgeReserve, s.budgetWithin(dryRunBudget, true))
}
