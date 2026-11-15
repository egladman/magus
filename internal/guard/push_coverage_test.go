package guard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/suite"
)

// largeWorkspace answers the two reads runEvidence makes of a workspace.
type largeWorkspace struct {
	types.WorkspaceRepository
	projects []*types.Project
	byPath   map[string]*types.Project
}

func (w largeWorkspace) All() []*types.Project { return w.projects }

func (w largeWorkspace) Get(path string) *types.Project { return w.byPath[path] }

// largePush is sized like a branch that lapsed the guard's deadline. A
// thousand files meet 1344 runs over twenty projects. A third of the runs
// name a package path.
func largePush(b *testing.B) (largeWorkspace, []cache.OutputDescriptor, []string, string) {
	dir := b.TempDir()
	targets := []string{"test", "lint", "build", "generate"}
	ws := largeWorkspace{byPath: map[string]*types.Project{}}
	root := &types.Project{
		Path:             ".",
		Sources:          []types.Glob{{Pattern: "**/*.go"}, {Pattern: "**/*.buzz"}},
		MagusfileTargets: append([]string{string(types.TargetCI)}, targets...),
		TargetInputs: map[string][]types.InputRef{
			"lint":     {{Glob: "**/*.go"}, {Glob: ".golangci.yml"}},
			"generate": {{Glob: "types/**/*.go"}},
		},
		TargetOutputs: map[string][]types.OutputRef{"generate": {{Glob: "gen/*.go"}}},
	}
	ws.projects = append(ws.projects, root)
	for i := range 19 {
		ws.projects = append(ws.projects, &types.Project{
			Path:             fmt.Sprintf("libs/l%02d", i),
			Sources:          []types.Glob{{Pattern: "**/*.go"}, {Pattern: "**/*.md", Except: []string{"CHANGELOG.md"}}},
			MagusfileTargets: targets,
			TargetInputs:     map[string][]types.InputRef{"lint": {{Glob: "**/*.go"}, {Glob: "../../.golangci.yml"}}},
			TargetOutputs:    map[string][]types.OutputRef{"generate": {{Glob: "gen/**"}}},
		})
	}
	for _, p := range ws.projects {
		ws.byPath[p.Path] = p
		for pkg := range 4 {
			if err := os.MkdirAll(filepath.Join(dir, p.Path, fmt.Sprintf("pkg%02d", pkg)), 0o755); err != nil {
				b.Fatal(err)
			}
		}
	}
	runs := make([]cache.OutputDescriptor, 1344)
	for i := range runs {
		d := cache.OutputDescriptor{
			Project:  ws.projects[i%len(ws.projects)].Path,
			Target:   targets[i%len(targets)],
			Revision: fmt.Sprintf("r%02d", i%16),
			Failed:   i%10 == 0,
		}
		if i%5 == 0 {
			d.Target += ":rw"
		}
		switch i % 3 {
		case 0:
			d.ExtraArgs = []string{"-run", "Push"}
		case 1:
			d.ExtraArgs = []string{fmt.Sprintf("pkg%02d", i%8)}
		}
		runs[i] = d
	}
	files := make([]string, 1000)
	for k := range files {
		p := ws.projects[k%len(ws.projects)].Path
		switch k % 7 {
		case 0:
			files[k] = filepath.ToSlash(filepath.Join(p, "gen", fmt.Sprintf("f%04d.go", k)))
		case 1:
			files[k] = filepath.ToSlash(filepath.Join(p, fmt.Sprintf("pkg%02d", k%8), fmt.Sprintf("f%04d.md", k)))
		default:
			files[k] = filepath.ToSlash(filepath.Join(p, fmt.Sprintf("pkg%02d", k%8), fmt.Sprintf("f%04d.go", k)))
		}
	}
	return ws, runs, files, dir
}

// BenchmarkRunEvidenceWeighsALargePush weighs each changed file as
// narrowCoverageAt does. The repository holds the commit and no run's
// revision, so the rest measures matching files to runs.
func BenchmarkRunEvidenceWeighsALargePush(b *testing.B) {
	ws, runs, files, dir := largePush(b)
	driver := typestest.NewMockVCSDriver(b)
	driver.On("FindCommit", mock.Anything, dir, "c1").Return(types.Commit{ID: "c1"}, nil)
	driver.On("FindCommit", mock.Anything, dir, mock.Anything).Return(types.Commit{}, os.ErrNotExist)
	driver.On("ReadFileAt", mock.Anything, dir, "c1", mock.Anything).Return("", os.ErrNotExist)
	ctx := b.Context()
	for b.Loop() {
		ev := newRunEvidence(driver, dir, dir, ws, runs, "c1")
		if err := ev.readRevisions(ctx, files); err != nil {
			b.Fatal(err)
		}
		for _, file := range files {
			if !ev.covered(ctx, file) {
				ev.wouldCover(file)
			}
		}
	}
}

func (s *NarrowPushSuite) TestARunAtAnOlderRevisionCoversAFileUnchangedSince() {
	s.gateAtFirstCommit()
	older := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n"})
	s.passed(strings.Repeat("a", 64), "go-test", older)
	head := s.repo.Commit("again", map[string]string{"b.go": "package a\n\nvar B = 1\n"})
	s.passed(strings.Repeat("b", 64), "lint", head, "b.go")

	ev := s.push(head)
	s.Equal(gateNarrow, ev.cover)
	s.Contains(ev.admitted(), "each of the 2 files")
}

func (s *NarrowPushSuite) TestARunAtAnOlderRevisionLeavesAFileChangedSinceUncovered() {
	s.gateAtFirstCommit()
	older := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n", "b.go": "package a\n"})
	s.passed(strings.Repeat("a", 64), "go-test", older)
	head := s.repo.Commit("again", map[string]string{"b.go": "package a\n\nvar B = 1\n"})

	ev := s.push(head)
	s.Equal(gateAbsent, ev.cover)
	_, reason := gradePushWithoutGate(ev, "")
	s.Contains(reason, "No passing run covers 1 of the 2 files")
	s.Contains(reason, "  b.go: run `")
	s.NotContains(reason, "  a.go:")
}

// coveragePhase is the phase pushCoverage recorded on ctx's log.
func (s *NarrowPushSuite) coveragePhase(ctx context.Context) trail.Phase {
	for _, p := range judgmentPhases(ctx).finished() {
		if p.Name == phasePushCoverage {
			return p
		}
	}
	s.Fail("no push_coverage phase was recorded")
	return trail.Phase{}
}

// A revision read the budget cuts answers no commit, which once read as a
// push nothing could be asked about and passed.
func (s *NarrowPushSuite) TestAPushWhoseRevisionReadIsCutIsRefusedNamingTheRead() {
	s.gateAtFirstCommit()
	s.deps.Revision = func(ctx context.Context, _, _ string) string {
		<-ctx.Done()
		return ""
	}
	deadline, cancel := context.WithTimeout(s.T().Context(), judgeReserve+20*time.Millisecond)
	defer cancel()
	ctx := withPhaseLog(deadline, trail.Phase{})

	ev := pushCoverage(ctx, s.deps, s.at, "git push", DialectBash, s.repo.Root())
	decision, reason := gradePushWithoutGate(ev, "")

	s.Equal(decisionDeny, decision)
	s.Contains(reason, "magus did not judge pushing this revision: reading the pushed revision did not finish within ")
	s.Contains(reason, "refused, never let through")
	s.NoError(deadline.Err(), "the push left judgeReserve to the rules")
}

func (s *NarrowPushSuite) TestACutRevisionReadIsTheLapseTheTrailRecords() {
	s.deps.Revision = func(ctx context.Context, _, _ string) string {
		<-ctx.Done()
		return ""
	}
	ctx := withPhaseLog(s.T().Context(), trail.Phase{})

	ev := pushCoverageWithin(ctx, time.Millisecond, s.deps, s.at, "git push", DialectBash, s.repo.Root())

	s.Equal(pushLapse{cut: phasePushRevision, budget: time.Millisecond}, ev.lapse)
	coverage := s.coveragePhase(ctx)
	s.True(coverage.Lapsed)
	s.Equal(int64(1), coverage.BudgetMs)
}

func (s *NarrowPushSuite) TestAWorkerWhosePushLapsesIsSentToItsOrchestrator() {
	s.deps.Revision = func(ctx context.Context, _, _ string) string {
		<-ctx.Done()
		return ""
	}

	ev := pushCoverageWithin(s.T().Context(), time.Millisecond, s.deps, s.at, "git push", DialectBash, s.repo.Root())
	decision, reason := gradePushWithoutGate(ev, "fix-a-thing")

	s.Equal(decisionDeny, decision)
	s.Contains(reason, "magus did not judge pushing this revision")
	s.Contains(reason, "bound to job fix-a-thing")
}

// The workspace open is a phase of its own, bounded short of the push's
// budget, so it lapses before the push's own reads are cut.
func (s *NarrowPushSuite) TestAPushWhoseWorkspaceOpenLapsesIsRefusedNamingTheOpen() {
	s.gateAtFirstCommit()
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	s.passed(strings.Repeat("a", 64), "go-test", head)
	s.deps.Revision = func(context.Context, string, string) string { return head[:12] }
	release := make(chan struct{})
	s.T().Cleanup(func() { close(release) })
	s.deps.Inspect = func(context.Context, string) (types.WorkspaceRepository, error) {
		<-release
		return nil, context.Canceled
	}
	ctx := withPhaseLog(s.T().Context(), trail.Phase{})

	ev := pushCoverageWithin(ctx, judgeReserve+20*time.Millisecond, s.deps, s.at, "git push", DialectBash, s.repo.Root())
	decision, reason := gradePushWithoutGate(ev, "")

	s.Equal(phaseInspect, ev.lapse.cut)
	s.LessOrEqual(ev.lapse.budget, 20*time.Millisecond, "the open's own budget, not the push's")
	s.Equal(decisionDeny, decision)
	s.Contains(reason, fmt.Sprintf("magus did not judge pushing %s: opening the workspace to weigh the changed files ", head[:12]))
}

// A read that answered after ctx ended still answered: a gate it found
// covers the push.
func (s *NarrowPushSuite) TestAGateFoundByAReadThatOutlivedTheBudgetAdmits() {
	head := s.repo.Commit("change", map[string]string{"a.go": "package a\n\nvar A = 1\n"})
	dir, err := sessions.Dir(s.repo.Root())
	s.Require().NoError(err)
	s.Require().NoError(sessions.RecordGate(dir, sessions.GateResult{
		Target: types.TargetCI, Scope: sessions.GateScopeAffected, Ref: "main", Commit: head, Outcome: sessions.OutcomePass, Inv: "inv-green",
	}, sessions.InvocationStart{Workspace: s.repo.Root()}))
	s.deps.Revision = func(ctx context.Context, _, _ string) string {
		<-ctx.Done()
		return head[:12]
	}

	ev := pushCoverageWithin(s.T().Context(), time.Millisecond, s.deps, s.at, "git push", DialectBash, s.repo.Root())
	decision, _ := gradePushWithoutGate(ev, "")

	s.Equal(phasePushRevision, ev.lapse.cut)
	s.Equal(gatePassed, ev.cover)
	s.Empty(decision)
}

// PushBudgetSuite pins pushCoverageBudget against a frozen clock.
type PushBudgetSuite struct{ suite.Suite }

func TestPushBudgetSuite(t *testing.T) { suite.Run(t, new(PushBudgetSuite)) }

// budgetWithin is pushCoverageBudget of the share with left on the clock,
// read in a bubble whose clock moves only when every goroutine waits.
func (s *PushBudgetSuite) budgetWithin(left time.Duration, dryRun bool) time.Duration {
	var got time.Duration
	synctest.Test(s.T(), func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), left)
		defer cancel()
		if dryRun {
			ctx = context.WithValue(ctx, dryRunKey{}, true)
		}
		got = pushCoverageBudget(ctx, pushCoverageShare)
	})
	return got
}

func (s *PushBudgetSuite) TestAnUnboundedJudgmentGetsTheShare() {
	s.Equal(pushCoverageShare, pushCoverageBudget(s.T().Context(), pushCoverageShare))
}

func (s *PushBudgetSuite) TestAWholeHookBudgetGetsTheShare() {
	s.Equal(pushCoverageShare, s.budgetWithin(hookBudget, false))
}

func (s *PushBudgetSuite) TestALateStartLeavesTheReserve() {
	s.Equal(time.Second-judgeReserve, s.budgetWithin(time.Second, false))
}

func (s *PushBudgetSuite) TestNoTimePastTheReserveIsNoBudget() {
	s.Equal(time.Duration(0), s.budgetWithin(judgeReserve/2, false))
}

func (s *PushBudgetSuite) TestADryRunGetsAllButTheReserve() {
	s.Equal(dryRunBudget-judgeReserve, s.budgetWithin(dryRunBudget, true))
}
