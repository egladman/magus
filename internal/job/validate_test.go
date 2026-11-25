package job

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

// integratedDirectorySuite pins MGS3018 against a branch replacing a directory.
// Its tests only read the repository, so they share it and fail if one of them
// changed it.
type integratedDirectorySuite struct {
	suite.Suite
	repo   *testkit.Repo
	shared *testkit.SharedRepository
	store  *Store
}

func TestIntegratedDirectory(t *testing.T) { suite.Run(t, new(integratedDirectorySuite)) }

func (s *integratedDirectorySuite) SetupSuite() {
	s.repo = testkit.NewRepository(s.T(), map[string]string{
		"skills/a/SKILL.md": "a\n",
		"skills/keep.md":    "keep\n",
		"shared/a/SKILL.md": "a\n",
	})
	s.repo.Git("checkout", "-q", "-b", "replaces-dir")
	s.repo.Git("rm", "-q", "-r", "skills/a")
	s.Require().NoError(os.Symlink("../shared/a", filepath.Join(s.repo.Root(), "skills", "a")))
	s.repo.Git("add", "skills/a")
	s.repo.Git("commit", "-q", "-m", "replace the directory with a symlink")
	s.repo.Git("checkout", "-q", "-")
	s.shared = testkit.ShareRepository(s.T(), s.repo.Root())
}

func (s *integratedDirectorySuite) SetupTest() {
	s.store = NewStore(tempLocation(s.T(), s.repo.Root()))
}

func (s *integratedDirectorySuite) TearDownTest() { s.shared.AssertUnchanged(s.T()) }

func (s *integratedDirectorySuite) row(write ...string) job.Job {
	return job.Job{ID: "integrate", Paths: job.Paths{Write: write}}
}

func (s *integratedDirectorySuite) TestDirectoryABranchReplacesWithASymlinkPasses() {
	ps := mergedDirectoryProblems(s.T().Context(), s.store, s.row("skills/a"), []string{"replaces-dir"})

	s.Empty(ps)
}

func (s *integratedDirectorySuite) TestDirectoryTheBranchKeepsIsRefused() {
	ps := mergedDirectoryProblems(s.T().Context(), s.store, s.row("skills/a", "shared/a"), []string{"replaces-dir"})

	s.Equal([]types.DiagnosticCode{types.WritePathIsDirectory}, codes(ps))
	s.Equal("paths.write[1]", ps[0].Field, "the index of the entry as the record wrote it")
}

func (s *integratedDirectorySuite) TestWithoutIntegratesTheDirectoryIsRefused() {
	ps := mergedDirectoryProblems(s.T().Context(), s.store, s.row("skills/a"), nil)

	s.Require().Len(ps, 1)
	s.Equal(types.WritePathIsDirectory, ps[0].Code)
}

func (s *integratedDirectorySuite) TestBranchTheVCSCannotReadKeepsTheRefusal() {
	ps := mergedDirectoryProblems(s.T().Context(), s.store, s.row("skills/a"), []string{"no-such-branch"})

	s.Require().Len(ps, 1)
	s.Equal(types.WritePathIsDirectory, ps[0].Code)
}

// validateWorkspace is regeneratedWorkspace with the targets its root project exports and a
// ci chain that lints and tests, so every workspace check has something to read.
func validateWorkspace(t *testing.T) (*Store, []*types.Project) {
	t.Helper()
	root, projects := regeneratedWorkspace(t)
	top := projects[0]
	top.MagusfileTargets = []string{"ci", "lint", "test", "man-generate", "config-generate", "types-generate"}
	top.TargetInputs["lint"] = []types.InputRef{{Glob: "**/*.go"}}
	top.TargetChains = map[string][]types.ChainStep{"ci": {{Target: "lint"}, {Target: "test"}}}
	projects[1].MagusfileTargets = []string{"content-generate", "site-generate"}
	return NewStore(tempLocation(t, root)).InWorkspace(Workspace{Projects: projects}), projects
}

func codes(ps types.JobProblems) []types.DiagnosticCode {
	out := make([]types.DiagnosticCode, len(ps))
	for i, p := range ps {
		out[i] = p.Code
	}
	return out
}

func problemAt(t *testing.T, ps types.JobProblems, code types.DiagnosticCode) types.JobProblem {
	t.Helper()
	i := slices.IndexFunc(ps, func(p types.JobProblem) bool { return p.Code == code })
	require.GreaterOrEqual(t, i, 0, "no %s in %v", code, codes(ps))
	return ps[i]
}

func TestValidateMergeCollectsEveryProblem(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	v := validateMerge(t.Context(), s, nil, "w", func(u *job.Job) {
		u.Paths.Write = []string{"cli", "cli/registry.go"}
		u.Check = fixtureCheck(types.LeaseCheck{Target: "nosuch", Project: "."})
		u.Postconditions = []job.Postcondition{{ID: "elsewhere", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "nowhere"})}}
	}, config.Jobs{}, nil)

	got := codes(v.Problems)
	for _, want := range []types.DiagnosticCode{types.WritePathIsDirectory, types.JobCheckUnrunnable, types.OutputOutsideWritePaths, types.GateNotRun} {
		assert.Contains(t, got, want, "every problem at once")
	}
	assert.Equal(t, "paths.write[0]", problemAt(t, v.Problems, types.WritePathIsDirectory).Field)

	var target, project types.JobProblem
	for _, p := range v.Problems {
		switch p.Field {
		case "check.target":
			target = p
		case "postconditions[0].check.project":
			project = p
		}
	}
	assert.Contains(t, target.Message, `names target "nosuch", which project "." does not run`)
	assert.Contains(t, target.Hint, "config-generate, lint", "an unknown target names the project's targets")
	assert.Contains(t, project.Message, `names project "nowhere"`)
	assert.Equal(t, "name one of its projects: ., docs", project.Hint)

	regen := problemAt(t, v.Problems, types.OutputOutsideWritePaths)
	assert.NotEmpty(t, regen.Subject, "a permissible problem names what a deviation names")
	assert.Contains(t, regen.Hint, `"reason":"the root regenerates it"`)
	gate := problemAt(t, v.Problems, types.GateNotRun)
	assert.Equal(t, "lint .", gate.Subject)
	assert.Contains(t, gate.Hint, `"check":{"target":"lint","project":"."}`, "the fix is the postcondition that runs it")
	assert.Empty(t, v.Unchecked)
	assert.Equal(t, "w", v.Job.ID, "the result carries the row as it would be stored")
}

func TestValidateMergePermitsOnlyWhatTheRecordNames(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	declare := func(deviations ...job.Deviation) func(*job.Job) {
		return func(u *job.Job) {
			u.Paths.Write, u.Check, u.Deviations = []string{"cli/registry.go"}, forkCheck(), deviations
		}
	}
	v := validateMerge(t.Context(), s, nil, "w", declare(), config.Jobs{}, nil)
	require.NotEmpty(t, v.Problems)
	var deviations []job.Deviation
	for _, p := range v.Problems {
		require.True(t, types.Permissible(p.Code), "only permissible problems are left: %v", p)
		deviations = append(deviations, job.Deviation{Code: job.Code(p.Code), Subject: p.Subject, Reason: "the root regenerates and runs it"})
	}

	permitted := validateMerge(t.Context(), s, nil, "w", declare(deviations...), config.Jobs{}, nil)
	assert.Empty(t, permitted.Problems)
	assert.Equal(t, v.Problems, permitted.Permitted)

	stored, err := ForkMerge(t.Context(), s, "w", declare(deviations...), config.Jobs{}, nil)
	require.NoError(t, err)
	assert.NotZero(t, stored.Validated, "a validated row is stamped")
	assert.Equal(t, deviations, stored.Deviations)

	wrong := validateMerge(t.Context(), s, nil, "w2", func(u *job.Job) {
		u.Paths.Write, u.Check = []string{"cli"}, forkCheck()
		u.Deviations = []job.Deviation{{Code: job.Code(types.WritePathIsDirectory), Subject: "cli", Reason: "it is mine"}, {Code: job.Code(types.GateNotRun), Subject: "lint ."}}
	}, config.Jobs{}, nil)
	fields := map[string]types.DiagnosticCode{}
	for _, p := range wrong.Problems {
		fields[p.Field] = p.Code
	}
	assert.Equal(t, types.JobRecordInvalid, fields["deviations[0].code"], "a deviation on a code that is not permissible is itself a problem")
	assert.Equal(t, types.JobRecordInvalid, fields["deviations[1].reason"], "and so is one with no reason")
	assert.Equal(t, types.WritePathIsDirectory, fields["paths.write[0]"], "and it permits nothing")
}

func TestValidateMergeNamesWhatItCouldNotCheck(t *testing.T) {
	t.Parallel()

	root, _ := regeneratedWorkspace(t)
	merge := func(u *job.Job) { u.Paths.Write, u.Check = []string{"cli/registry.go"}, forkCheck() }

	v := validateMerge(t.Context(), NewStore(tempLocation(t, root)), nil, "w", merge, config.Jobs{}, nil)
	assert.Empty(t, v.Problems)
	assert.Len(t, v.Unchecked, 4, "the target, outputs, gates and breadth checks each say they did not run")
	for _, why := range v.Unchecked {
		assert.Contains(t, why, "without a workspace")
	}

	unavailable := NewStore(tempLocation(t, root)).InWorkspace(Workspace{Unavailable: "magusfile.buzz: line 3: unexpected }"})
	v = validateMerge(t.Context(), unavailable, nil, "w", merge, config.Jobs{}, nil)
	require.Len(t, v.Unchecked, 4)
	assert.Contains(t, v.Unchecked[0], "the workspace did not load: magusfile.buzz: line 3")

	v = validateMerge(t.Context(), unavailable, nil, "scout", func(u *job.Job) { u.ReadOnly = true }, config.Jobs{}, nil)
	assert.Empty(t, v.Unchecked, "a read-only row with no check skips nothing it would have needed")
}

func TestValidateDryForkWritesNothing(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	v, err := validateDryFork(t.Context(), s, "w", func(u *job.Job) {
		u.Paths.Write, u.Check, u.Checkpoint = []string{"cli"}, forkCheck(), "abc+zz"
	})
	require.NoError(t, err)
	got := codes(v.Problems)
	assert.Contains(t, got, types.JobCheckpointUnplaceable, "the checkpoint is judged in the same pass")
	assert.Contains(t, got, types.WritePathIsDirectory)
	assert.Equal(t, "checkpoint", problemAt(t, v.Problems, types.JobCheckpointUnplaceable).Field)

	rows, err := s.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, rows, "a dry run writes nothing")

	_, err = ForkMerge(t.Context(), s, "w", func(u *job.Job) {
		u.Paths.Write, u.Check, u.Checkpoint = []string{"cli"}, forkCheck(), "abc+zz"
	}, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.JobCheckpointUnplaceable)
	require.ErrorIs(t, err, types.WritePathIsDirectory, "a fork reports both, not the first")
}

// forkPlanSuite pins that a fork validates once: its dry run lists what it
// refuses, and it writes the plan it judged.
type forkPlanSuite struct {
	suite.Suite
	store *Store
}

func TestForkPlan(t *testing.T) { suite.Run(t, new(forkPlanSuite)) }

func (s *forkPlanSuite) SetupTest() { s.store, _ = validateWorkspace(s.T()) }

func (s *forkPlanSuite) plan(merge func(*job.Job), dry bool) ForkPlan {
	plan, err := PlanFork(s.T().Context(), s.store, "w", merge, config.Jobs{}, nil, dry)
	s.Require().NoError(err)
	return plan
}

func (s *forkPlanSuite) TestDryRunListsWhatTheForkRefuses() {
	merge := func(u *job.Job) { u.Paths.Write, u.Check, u.Checkpoint = []string{"cli"}, forkCheck(), "abc+zz" }

	dry := s.plan(merge, true)
	real := s.plan(merge, false)
	_, err := WriteFork(s.T().Context(), s.store, real, nil, config.Jobs{})

	s.Require().NotEmpty(dry.Validation.Problems)
	s.Equal(dry.Validation.Problems, real.Validation.Problems)
	for _, p := range dry.Validation.Problems {
		s.ErrorIs(err, p.Code)
	}
	rows, lerr := s.store.List(s.T().Context())
	s.Require().NoError(lerr)
	s.Empty(rows)
}

func (s *forkPlanSuite) TestForkWritesThePlanItValidated() {
	merge := func(u *job.Job) { u.Paths.Write, u.Check = []string{"cli/registry.go"}, forkCheck() }
	permit := func(u *job.Job) {
		merge(u)
		u.Deviations = []job.Deviation{{Code: job.Code(types.OutputOutsideWritePaths), Subject: "**", Reason: job.ReasonIntegratorRegenerates}}
	}
	plan := s.plan(permit, false)
	s.Require().Empty(plan.Validation.Problems)

	stored, err := WriteFork(s.T().Context(), s.store, plan, func(u *job.Job) { u.Checkpoint = "amended" }, config.Jobs{})

	s.Require().NoError(err)
	want := plan.Validation.Job
	want.Checkpoint = "amended"
	s.Equal(RetryDeclaration(want), RetryDeclaration(stored), "the plan's settled merge, lint postcondition included, then the amend")
}

func (s *forkPlanSuite) TestForkRefusesWhenALiveRowAppeared() {
	plan := s.plan(func(u *job.Job) {
		u.Paths.Write, u.Check = []string{"cli/registry.go"}, forkCheck()
		u.Deviations = []job.Deviation{{Code: job.Code(types.OutputOutsideWritePaths), Subject: "**", Reason: job.ReasonIntegratorRegenerates}}
	}, false)
	s.Require().Empty(plan.Validation.Problems)
	seed(s.T(), s.store, job.Job{ID: "other", State: job.StateRunning, Paths: job.Paths{Write: []string{"cli/registry.go"}}, Check: forkCheck()})

	_, err := WriteFork(s.T().Context(), s.store, plan, nil, config.Jobs{})

	s.Require().Error(err)
	s.Contains(err.Error(), "other changed after w was validated")
}

func (s *forkPlanSuite) TestForkWritesWhenALiveRowOnlyNarrowed() {
	seed(s.T(), s.store, job.Job{ID: "other", State: job.StateRunning, Paths: job.Paths{Write: []string{"docs/**"}}, Check: forkCheck()})
	plan := s.plan(func(u *job.Job) {
		u.Paths.Write, u.Check = []string{"cli/registry.go"}, forkCheck()
		u.Deviations = []job.Deviation{{Code: job.Code(types.OutputOutsideWritePaths), Subject: "**", Reason: job.ReasonIntegratorRegenerates}}
	}, false)
	s.Require().Empty(plan.Validation.Problems)
	seed(s.T(), s.store, job.Job{ID: "other", State: job.StateRunning, Paths: job.Paths{Write: []string{"docs/rules/guard.md"}}, Check: forkCheck()})

	_, err := WriteFork(s.T().Context(), s.store, plan, nil, config.Jobs{})

	s.NoError(err, "a narrowed hold only lifts a constraint")
}

func TestValidateMergeJudgesAnUpdateByWhatItAdds(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	seed(t, s, job.Job{ID: "w", State: job.StateRunning, Paths: job.Paths{Write: []string{"cli/**", "docs/**"}}, Check: forkCheck()})
	rows, err := s.List(t.Context())
	require.NoError(t, err)

	narrowed := validateMerge(t.Context(), s, rows, "w", func(u *job.Job) { u.Paths.Write = []string{"cli/registry.go"} }, config.Jobs{}, nil)
	assert.Empty(t, narrowed.Problems, "narrowing a glob to a file it covers adds nothing")

	widened := validateMerge(t.Context(), s, rows, "w", func(u *job.Job) { u.Paths.Write = []string{"cli/**", "docs/**", "cmd"} }, config.Jobs{}, nil)
	assert.Equal(t, "paths.write[2]", problemAt(t, widened.Problems, types.WritePathIsDirectory).Field,
		"a problem found on what the update adds names the field in the whole row")
	assert.NotContains(t, codes(widened.Problems), types.JobRecordInvalid, "a shape the row already had is not the update's")
}

func TestValidateMergeRefusesAnUpdateThatDropsWhatTheRowDeclared(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	seed(t, s, job.Job{
		ID: "w", State: job.StateRunning, Paths: job.Paths{Write: []string{"cli/registry.go"}}, Check: forkCheck(),
		Agent:          &job.Agent{Instructions: "do the thing"},
		Postconditions: []job.Postcondition{{ID: "g", Kind: job.PostconditionKindCheck, Check: forkCheck()}},
	})
	rows, err := s.List(t.Context())
	require.NoError(t, err)

	wiped := func(u *job.Job) {
		u.Agent, u.Paths.Write, u.Check, u.Postconditions = nil, nil, job.Check{}, nil
	}
	v := validateMerge(t.Context(), s, rows, "w", wiped, config.Jobs{}, nil)
	var subjects []string
	for _, p := range v.Problems {
		if p.Code == types.JobRecordInvalid && strings.HasPrefix(p.Subject, types.DropsSubject) {
			subjects = append(subjects, p.Subject)
		}
	}
	assert.ElementsMatch(t, []string{"drops agent.instructions", "drops paths.write", "drops check", "drops postconditions"}, subjects)

	said := func(u *job.Job) {
		wiped(u)
		u.Deviations = []job.Deviation{
			{Code: job.Code(types.JobRecordInvalid), Subject: "drops agent.instructions", Reason: "the job is reset"},
			{Code: job.Code(types.JobRecordInvalid), Subject: "drops paths.write", Reason: "the job is reset"},
			{Code: job.Code(types.JobRecordInvalid), Subject: "drops check", Reason: "the job is reset"},
			{Code: job.Code(types.JobRecordInvalid), Subject: "drops postconditions", Reason: "the job is reset"},
		}
	}
	v = validateMerge(t.Context(), s, rows, "w", said, config.Jobs{}, nil)
	for _, p := range v.Problems {
		assert.False(t, strings.HasPrefix(p.Subject, types.DropsSubject), "a deviation naming the erasure permits it: %s", p.Subject)
	}
	assert.Len(t, v.Permitted, 4)

	kept := validateMerge(t.Context(), s, rows, "w", func(u *job.Job) { u.Paths.Write = []string{"cli/registry.go"} }, config.Jobs{}, nil)
	for _, p := range kept.Problems {
		assert.False(t, strings.HasPrefix(p.Subject, types.DropsSubject), "an update that keeps the fields drops none")
	}
}

// ReforkGradeSuite pins that a refork is graded as the row it writes, with no checkout
// attached: the checkout its last holder took is gone or not the next holder's.
type ReforkGradeSuite struct {
	suite.Suite
	store  *Store
	worker *Store
	head   string
	graded []job.Job
}

func TestReforkGradeSuite(t *testing.T) { suite.Run(t, new(ReforkGradeSuite)) }

func (s *ReforkGradeSuite) SetupTest() {
	main, worktree, head := twoCheckouts(s.T())
	main.Grader, worktree.Grader = s.grade, s.grade
	s.store, s.worker, s.head, s.graded = NewStore(main), NewStore(worktree), head, nil
}

// grade stands in for the guard, which places the check in the checkout the row names and
// denies the check and its compiling baseline alike where that checkout has no ./magus.
func (s *ReforkGradeSuite) grade(_ context.Context, _ string, row job.Job) CheckVerdict {
	s.graded = append(s.graded, row)
	if row.CheckoutRoot == "" {
		return CheckVerdict{}
	}
	if _, err := os.Stat(filepath.Join(row.CheckoutRoot, "magus")); err != nil {
		return CheckVerdict{NotGraded: fmt.Sprintf("%s has no ./magus yet", row.CheckoutRoot)}
	}
	return CheckVerdict{}
}

func (s *ReforkGradeSuite) declare(u *job.Job) {
	u.State, u.Checkpoint = job.StateDeclared, s.head
	u.Paths.Write, u.Check = []string{"a.txt"}, fixtureCheck(*goTestCheck())
}

func (s *ReforkGradeSuite) TestAReforkIsGradedWithNoCheckoutAttached() {
	ctx := s.T().Context()
	_, err := ForkMerge(ctx, s.store, "w", s.declare, config.Jobs{}, nil)
	s.Require().NoError(err)
	taken, err := s.worker.Exec(ctx, "w")
	s.Require().NoError(err)
	s.Require().NotEmpty(taken.CheckoutRoot)
	_, err = s.store.Update(ctx, "w", func(u *job.Job) { u.State = job.StateFail })
	s.Require().NoError(err)

	again, err := ForkMerge(ctx, s.store, "w", s.declare, config.Jobs{}, nil)

	s.Require().NoError(err, "a refork is not graded in the checkout its last holder took")
	s.Require().Len(s.graded, 2)
	graded := s.graded[1]
	s.Equal(job.Job{}, job.Job{ReportedBase: graded.ReportedBase, BaseVerdict: graded.BaseVerdict, Registered: graded.Registered, CheckoutRoot: graded.CheckoutRoot}, "the grade reads the row the refork writes")
	s.Equal(job.Job{}, job.Job{ReportedBase: again.ReportedBase, BaseVerdict: again.BaseVerdict, Registered: again.Registered, CheckoutRoot: again.CheckoutRoot})
	s.Equal(job.StateDeclared, again.State)
}

func TestApplyCollectsEveryRecordsProblems(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	_, err := Apply(t.Context(), s, []job.Declaration{
		{ID: "a", Paths: job.Paths{Write: []string{"cli"}}, Check: forkCheck()},
		{ID: "b", Paths: job.Paths{Write: []string{"cli/registry.go"}}, State: job.StateRunning, Check: forkCheck()},
	}, config.Jobs{}, nil, nil, false)
	require.ErrorIs(t, err, types.WritePathIsDirectory)
	require.ErrorIs(t, err, types.JobRecordInvalid)
	assert.Contains(t, err.Error(), `a: "cli", inside project "." is a directory`, "each message names its record")
	assert.Contains(t, err.Error(), `b: the record for b carries state "running"`)

	rows, lerr := s.List(t.Context())
	require.NoError(t, lerr)
	assert.Empty(t, rows, "nothing is written while any record has a problem")
}

func TestValidateMergePermitsEveryRegeneratedOutputByGlob(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	declare := func(deviations ...job.Deviation) func(*job.Job) {
		return func(u *job.Job) {
			u.Paths.Write, u.Check, u.Deviations = []string{"cli/registry.go"}, forkCheck(), deviations
		}
	}
	bare := validateMerge(t.Context(), s, nil, "w", declare(), config.Jobs{}, nil)
	require.Greater(t, len(bare.Problems), 2, "several outputs and a gate stand unpermitted")

	outputs := validateMerge(t.Context(), s, nil, "w", declare(job.Deviation{Code: job.Code(types.OutputOutsideWritePaths), Subject: "**", Reason: "the integrator regenerates it"}), config.Jobs{}, nil)
	assert.Equal(t, []types.DiagnosticCode{types.GateNotRun}, slices.Compact(codes(outputs.Problems)), "a glob permits its own code alone")

	both := validateMerge(t.Context(), s, nil, "w", declare(
		job.Deviation{Code: job.Code(types.OutputOutsideWritePaths), Subject: "**", Reason: "the integrator regenerates it"},
		job.Deviation{Code: job.Code(types.GateNotRun), Subject: "**", Reason: "the integrator runs it"},
	), config.Jobs{}, nil)
	assert.Equal(t, []types.DiagnosticCode{types.GateNotRun}, codes(both.Problems), "lint is the one gate a glob cannot permit")
	assert.Len(t, both.Permitted, len(bare.Problems)-1)

	narrow := validateMerge(t.Context(), s, nil, "w", declare(job.Deviation{Code: job.Code(types.OutputOutsideWritePaths), Subject: "docs/**", Reason: "the integrator regenerates it"}), config.Jobs{}, nil)
	assert.Contains(t, codes(narrow.Problems), types.OutputOutsideWritePaths, "a glob permits only the subjects it matches")
	assert.Less(t, len(narrow.Problems), len(bare.Problems))
}

func TestIntegratorDeviationsAcceptEveryOutputButADeniedOne(t *testing.T) {
	t.Parallel()

	root, projects := regeneratedWorkspace(t)
	row := job.Job{Paths: job.Paths{Write: []string{"cli/registry.go"}, Deny: []string{"cli/testdata/api.lock"}}}

	got := IntegratorDeviations(root, row, projects, ignoresCoverage)

	subjects := make([]string, len(got))
	for i, w := range got {
		assert.Equal(t, job.Code(types.OutputOutsideWritePaths), w.Code)
		assert.Equal(t, job.ReasonIntegratorRegenerates, w.Reason)
		subjects[i] = w.Subject
	}
	assert.Contains(t, subjects, "docs/manpage/*.md")
	assert.Contains(t, subjects, "cmd/gen/*.go")
	assert.NotContains(t, subjects, "cli/testdata/api.lock", "a denied output stays refused")

	row.Deviations = got
	assert.Empty(t, IntegratorDeviations(root, row, projects, ignoresCoverage), "an output already permitted needs no second deviation")
}

func TestExtendFootprintAddsSiblingTests(t *testing.T) {
	t.Parallel()

	root, projects := regeneratedWorkspace(t)
	row := job.Job{Paths: job.Paths{Write: []string{"cli/registry.go"}, Deny: []string{"cli/testdata/api.lock"}}}
	referrers := func(file, _ string) map[string]int {
		if file != "cli/registry.go" {
			return nil
		}
		return map[string]int{"cli/registry_test.go": 3, "cmd/main_test.go": 1, "cmd/main.go": 2}
	}

	paths, notes := ExtendFootprint(root, row, projects, referrers)

	assert.Equal(t, []string{"cli/registry_test.go"}, paths, "a test beside a written file is reached; outputs and other directories are not")
	assert.Len(t, notes, len(paths), "each added path says why")

	none, _ := ExtendFootprint(root, job.Job{ReadOnly: true, Paths: job.Paths{Write: row.Paths.Write}}, projects, referrers)
	assert.Empty(t, none)
}

func TestValidateMergePermitsLintOnlyByItsExactSubject(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	declare := func(deviations ...job.Deviation) func(*job.Job) {
		return func(u *job.Job) {
			u.Paths.Write, u.Check, u.Deviations = []string{"cli/registry.go"}, forkCheck(), deviations
		}
	}
	glob := job.Deviation{Code: job.Code(types.GateNotRun), Subject: "**", Reason: "the integrator runs it"}
	got := validateMerge(t.Context(), s, nil, "w", declare(glob), config.Jobs{}, nil)
	lint := problemAt(t, got.Problems, types.GateNotRun)
	assert.Equal(t, "lint .", lint.Subject, "a glob leaves the lint gate standing")

	exact := job.Deviation{Code: job.Code(types.GateNotRun), Subject: "lint .", Reason: "the root lints this one"}
	got = validateMerge(t.Context(), s, nil, "w", declare(glob, exact), config.Jobs{}, nil)
	assert.NotContains(t, codes(got.Problems), types.GateNotRun)
	assert.Contains(t, codes(got.Permitted), types.GateNotRun)
}

func TestResolveMergeAddsALintPostconditionAndSaysSo(t *testing.T) {
	t.Parallel()

	s, _ := validateWorkspace(t)
	declare := func(paths ...string) func(*job.Job) {
		return func(u *job.Job) { u.Paths.Write, u.Check = paths, forkCheck() }
	}
	resolve := func(merge func(*job.Job)) (job.Job, string) {
		var notes bytes.Buffer
		s.notices = &notes
		merged, err := resolveMerge(t.Context(), s, nil, "w", merge, nil)
		require.NoError(t, err)
		var row job.Job
		merged(&row)
		return row, notes.String()
	}

	for _, f := range []string{"docs/notes.md", "cli/registry.go"} {
		full := filepath.Join(s.root, filepath.FromSlash(f))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, nil, 0o644))
	}
	row, note := resolve(declare("docs/notes.md", "cli/registry.go"))
	i := slices.IndexFunc(row.Postconditions, func(g job.Postcondition) bool { return g.ID == "lint" })
	require.GreaterOrEqual(t, i, 0, "postconditions: %v", row.Postconditions)
	assert.Equal(t, types.LeaseCheck{Target: "lint", Project: ".", NoDefaultCharms: true,
		Args: []string{"cli/registry.go", "docs/notes.md"}}, checkIn(t, row.Postconditions[i].Check), "lint is scoped to the written files, sorted")
	assert.Contains(t, note, `added the postcondition "lint": the lint target of . reads 2 written file(s) (cli/registry.go docs/notes.md) and no check or postcondition runs it`)

	row, note = resolve(func(u *job.Job) {
		declare("cli/registry.go")(u)
		u.Postconditions = []job.Postcondition{job.Postcondition{ID: "lint", Check: fixtureCheck(types.LeaseCheck{Target: "lint", Project: "."})}.Resolve()}
	})
	assert.Len(t, row.Postconditions, 1, "a postcondition that runs lint is kept alone")
	assert.Empty(t, note)

	row, note = resolve(declare("docs/notes.md"))
	assert.Empty(t, row.Postconditions, "no file lint declares it reads, no lint postcondition")
	assert.Empty(t, note)
}

func TestResolveMergeAddsALintPostconditionForAnyLanguageLintReads(t *testing.T) {
	t.Parallel()

	root, projects := regeneratedWorkspace(t)
	top := projects[0]
	top.MagusfileTargets = []string{"ci", "lint", "test"}
	top.TargetInputs["lint"] = []types.InputRef{{Glob: "**/*.ts"}}
	top.TargetChains = map[string][]types.ChainStep{"ci": {{Target: "lint"}, {Target: "test"}}}
	s := NewStore(tempLocation(t, root)).InWorkspace(Workspace{Projects: projects})

	merged, err := resolveMerge(t.Context(), s, nil, "w", func(u *job.Job) { u.Paths.Write, u.Check = []string{"web/app.ts"}, forkCheck() }, nil)
	require.NoError(t, err)
	var row job.Job
	merged(&row)

	i := slices.IndexFunc(row.Postconditions, func(g job.Postcondition) bool { return g.ID == "lint" })
	require.GreaterOrEqual(t, i, 0, "postconditions: %v", row.Postconditions)
	assert.Equal(t, []string{"web/app.ts"}, checkIn(t, row.Postconditions[i].Check).Args)
}

func TestFilesInScopesANestedProjectsLintToItsOwnFiles(t *testing.T) {
	t.Parallel()

	files := []string{"cli/a.go", "libs/x/b.go", "libs/xy/c.go"}
	assert.Equal(t, files, filesIn(".", files), "the root lints every written file")
	assert.Equal(t, []string{"b.go"}, filesIn("libs/x", files), "relative to the project, and never a sibling sharing its prefix")
	assert.Empty(t, filesIn("libs/z", files))
}

// lintPostconditionArgsSuite pins the lint postcondition's args to the written files the tree
// holds at fork, whatever language the project lints.
type lintPostconditionArgsSuite struct {
	suite.Suite
	root string
}

func TestLintPostconditionArgs(t *testing.T) { suite.Run(t, new(lintPostconditionArgsSuite)) }

func (s *lintPostconditionArgsSuite) SetupTest() { s.root = s.T().TempDir() }

func (s *lintPostconditionArgsSuite) write(files ...string) {
	for _, f := range files {
		full := filepath.Join(s.root, filepath.FromSlash(f))
		s.Require().NoError(os.MkdirAll(filepath.Dir(full), 0o755))
		s.Require().NoError(os.WriteFile(full, nil, 0o644))
	}
}

// args is the lint postcondition's args for project, whose lint reads sources, and the notes.
func (s *lintPostconditionArgsSuite) args(project, sources string, write ...string) ([]string, []string) {
	lintable := func(path string) *types.Project {
		return &types.Project{
			Path:             path,
			Sources:          types.MustParseGlobs(sources),
			MagusfileTargets: []string{"ci", "lint", "go-test"},
			TargetChains:     map[string][]types.ChainStep{"ci": {{Target: "lint"}}},
		}
	}
	projects := []*types.Project{lintable("."), lintable("libs/kit")}
	row := job.Job{ID: "j", Paths: job.Paths{Write: write}, Check: fixtureCheck(types.LeaseCheck{Target: "go-test", Project: "."})}
	got, notes := lintPostconditions(s.T().Context(), s.root, row, Workspace{Projects: projects}, nil)
	for _, p := range got {
		if p.Check.Project == project {
			return p.Check.Args, notes
		}
	}
	s.FailNow("no lint postcondition for " + project)
	return nil, nil
}

func (s *lintPostconditionArgsSuite) TestAGoWritePathTheTreeDoesNotHoldIsLeftOut() {
	s.write("libs/kit/job/digest.go")

	got, notes := s.args("libs/kit", "**/*.go", "libs/kit/job/digest.go", "libs/kit/job/digest_open_unix.go")

	s.Equal([]string{"job/digest.go"}, got, "the file the job may never create reaches no tool")
	s.Contains(notes, "left 1 written file(s) the tree does not hold yet out of the lint postcondition for libs/kit:"+
		" exit grades the run over every file the job changed, so name each one it creates")
}

func (s *lintPostconditionArgsSuite) TestAWritePathOfAnotherLanguageIsLeftOutTheSameWay() {
	s.write("web/app.ts")

	got, _ := s.args(".", "**/*.ts", "web/app.ts", "web/later.ts")

	s.Equal([]string{"web/app.ts"}, got)
}

func (s *lintPostconditionArgsSuite) TestAProjectWithNoWrittenFilePresentKeepsThemAllAndSaysSo() {
	got, notes := s.args(".", "**/*.go", "internal/job/new.go")

	s.Equal([]string{"internal/job/new.go"}, got, "empty args would lint the whole project")
	s.Contains(notes, "none of the 1 file(s) the lint postcondition for . reads exists yet, so it names every one:"+
		" drop from the run any the job does not create")
}

func TestAmbiguousSymbolProblemsRefuseAnUnresolvedPostconditionOnAnExistingSymbol(t *testing.T) {
	t.Parallel()

	read := func(_ context.Context, name, _ string) (SymbolFact, bool) {
		if name == "job.Declare" {
			return SymbolFact{DefinedIn: []string{"internal/job/plan.go"}}, true
		}
		return SymbolFact{}, true
	}
	postconditions := []job.Postcondition{
		{ID: "gone", Kind: job.PostconditionKindSymbol, Expect: job.ExpectAbsent, Symbols: []string{"job.Declare", "job.Typo"}},
		{ID: "new", Kind: job.PostconditionKindSymbol, Expect: job.ExpectPresent, Symbols: []string{"job.Typo"}},
		{ID: "bare", Kind: job.PostconditionKindSymbol, Expect: job.ExpectAbsent, Symbols: []string{"Typo"}},
	}

	ps := ambiguousSymbolProblems(t.Context(), postconditions, read)

	require.Len(t, ps, 1, "a present postcondition names what the job will write, and a bare name may be a graph without an index")
	assert.Equal(t, "postconditions[0].symbols[1]", ps[0].Field)
	assert.Contains(t, ps[0].Message, `"job.Typo"`)
}

func TestDirectoryWritePathStandsDownForAnAllOutputDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for _, rel := range []string{
		"docs/gen/a.md", "docs/gen/sub/b.md",
		"docs/mixed/c.md", "docs/mixed/hand.txt",
		"docs/src/page.buzz",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(rel), 0o644))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(root, "docs", "empty"), 0o755))
	projects := []*types.Project{{
		Path: "docs",
		TargetOutputs: map[string][]types.OutputRef{
			"site-generate": {{Glob: "gen/**"}, {Glob: "mixed/*.md"}, {Glob: "empty/*.md"}},
		},
	}}
	store := NewStore(tempLocation(t, root)).InWorkspace(Workspace{Projects: projects})
	problems := func(paths ...string) types.JobProblems {
		return directoryWritePathProblems(store, job.Job{Paths: job.Paths{Write: paths}})
	}

	assert.Empty(t, problems("docs/gen"), "every file under it is a declared output")
	assert.Empty(t, problems("docs/gen/**"), "a wildcard subtree reads as the directory")

	ps := problems("docs/mixed")
	require.Len(t, ps, 1)
	assert.Equal(t, types.WritePathIsDirectory, ps[0].Code)
	assert.Contains(t, ps[0].Message, "docs/mixed/hand.txt is not a declared output", "the message names the file that keeps the refusal")

	assert.Len(t, problems("docs/src"), 1, "a source file keeps the refusal")
	assert.Len(t, problems("docs/empty"), 1, "a directory with no file shows nothing generated")
	assert.Len(t, problems("**"), 1, "a bare ** stays refused")
	assert.Len(t, problems("."), 1)

	bare := NewStore(tempLocation(t, root))
	assert.Len(t, directoryWritePathProblems(bare, job.Job{Paths: job.Paths{Write: []string{"docs/gen"}}}), 1, "with no workspace nothing is known to be generated")

	row := job.Job{Paths: job.Paths{Write: []string{"docs/gen"}}}
	assert.True(t, outputInside(row, types.Glob{Pattern: "docs/gen/*.md"}), "the directory claim holds an output glob under it (MGS3044)")
	assert.False(t, outputInside(row, types.Glob{Pattern: "docs/mixed/*.md"}))
}

type FootprintMayExtendSuite struct {
	suite.Suite
	root string
}

func TestFootprintMayExtendSuite(t *testing.T) {
	suite.Run(t, new(FootprintMayExtendSuite))
}

func (s *FootprintMayExtendSuite) SetupTest() {
	s.root = s.T().TempDir()
	for name, body := range map[string]string{"run/run.go": "package run\n", "run/run_test.go": "package run\n", "docs/guide.md": "# guide\n"} {
		path := filepath.Join(s.root, filepath.FromSlash(name))
		s.Require().NoError(os.MkdirAll(filepath.Dir(path), 0o755))
		s.Require().NoError(os.WriteFile(path, []byte(body), 0o644))
	}
}

func (s *FootprintMayExtendSuite) TestSourceBesideATestMayExtend() {
	s.True(FootprintMayExtend(s.root, job.Job{Paths: job.Paths{Write: []string{"run/run.go"}}}))
}

func (s *FootprintMayExtendSuite) TestADirectoryWritePathMayExtend() {
	s.True(FootprintMayExtend(s.root, job.Job{Paths: job.Paths{Write: []string{"run"}}}))
}

func (s *FootprintMayExtendSuite) TestADirectoryWithNoTestCannotExtend() {
	s.False(FootprintMayExtend(s.root, job.Job{Paths: job.Paths{Write: []string{"docs/guide.md"}}}))
}

func (s *FootprintMayExtendSuite) TestANewFileInANewDirectoryCannotExtend() {
	s.False(FootprintMayExtend(s.root, job.Job{Paths: job.Paths{Write: []string{"fresh/new.go"}}}))
}

func (s *FootprintMayExtendSuite) TestWritingOnlyTestsCannotExtend() {
	s.False(FootprintMayExtend(s.root, job.Job{Paths: job.Paths{Write: []string{"run/run_test.go"}}}))
}

func (s *FootprintMayExtendSuite) TestAReadOnlyRowCannotExtend() {
	s.False(FootprintMayExtend(s.root, job.Job{ReadOnly: true, Paths: job.Paths{Write: []string{"run/run.go"}}}))
}

// LintPostconditionTrackedFilesSuite pins that a derived lint postcondition skips the files the VCS ignores and
// keeps every other one, tracked or not.
type LintPostconditionTrackedFilesSuite struct {
	suite.Suite
}

func TestLintPostconditionTrackedFilesSuite(t *testing.T) {
	suite.Run(t, new(LintPostconditionTrackedFilesSuite))
}

func (s *LintPostconditionTrackedFilesSuite) resolve(repo *testkit.Repo, ws Workspace) ([]string, string) {
	st := NewStore(tempLocation(s.T(), repo.Root())).InWorkspace(ws)
	var notes bytes.Buffer
	st.notices = &notes
	merged, err := resolveMerge(s.T().Context(), st, nil, "w", func(u *job.Job) {
		u.Paths.Write, u.Check = []string{"skills/**"}, forkCheck()
	}, nil)
	s.Require().NoError(err)
	var row job.Job
	merged(&row)
	i := slices.IndexFunc(row.Postconditions, func(g job.Postcondition) bool { return g.ID == "lint" })
	s.Require().GreaterOrEqual(i, 0, "postconditions: %v", row.Postconditions)
	return checkIn(s.T(), row.Postconditions[i].Check).Args, notes.String()
}

func (s *LintPostconditionTrackedFilesSuite) workspace(root string) Workspace {
	top := &types.Project{
		Path:             ".",
		MagusfileTargets: []string{"ci", "lint", "test"},
		TargetInputs:     map[string][]types.InputRef{"lint": {{Glob: "**/*.md"}}},
		TargetChains:     map[string][]types.ChainStep{"ci": {{Target: "lint"}, {Target: "test"}}},
	}
	return Workspace{Projects: []*types.Project{top}, Ignored: VCSIgnored(s.T().Context(), root)}
}

func (s *LintPostconditionTrackedFilesSuite) TestAnIgnoredFileIsLeftOutAndEveryOtherKept() {
	repo := testkit.NewRepository(s.T(), map[string]string{
		".gitignore":       "skills/installed/\n",
		"skills/kept/a.md": "# a\n",
	})
	write := func(rel string) {
		p := filepath.Join(repo.Root(), filepath.FromSlash(rel))
		s.Require().NoError(os.MkdirAll(filepath.Dir(p), 0o755))
		s.Require().NoError(os.WriteFile(p, []byte("# x\n"), 0o644))
	}
	write("skills/installed/b.md")
	write("skills/installed/forced.md")
	repo.Git("add", "-f", "skills/installed/forced.md")
	repo.Git("commit", "-q", "-m", "force-add an ignored path")
	write("skills/new/c.md")

	args, note := s.resolve(repo, s.workspace(repo.Root()))

	s.Equal([]string{"skills/installed/forced.md", "skills/kept/a.md", "skills/new/c.md"}, args,
		"the tracked, the force-added and the not yet tracked file stay; the ignored one goes")
	s.Contains(note, "left 1 ignored file(s) out of the lint postcondition")
}

func (s *LintPostconditionTrackedFilesSuite) TestAnUnaskableVCSKeepsEveryFileAndSaysSo() {
	repo := testkit.NewRepository(s.T(), map[string]string{"skills/kept/a.md": "# a\n"})
	ws := s.workspace(repo.Root())
	ws.Ignored = nil

	args, note := s.resolve(repo, ws)

	s.Equal([]string{"skills/kept/a.md"}, args)
	s.Contains(note, "keeps every one")
}

func (s *LintPostconditionTrackedFilesSuite) TestAFailedIgnoreQueryKeepsEveryFileAndNamesTheError() {
	repo := testkit.NewRepository(s.T(), map[string]string{"skills/kept/a.md": "# a\n"})
	ws := s.workspace(repo.Root())
	ws.Ignored = func([]string) ([]string, error) { return nil, errors.New("git check-ignore: exit status 128") }

	args, note := s.resolve(repo, ws)

	s.Equal([]string{"skills/kept/a.md"}, args)
	s.Contains(note, "keeps every written file")
	s.Contains(note, "git check-ignore: exit status 128")
}

func unrunGate() UnrunGate {
	return UnrunGate{
		Target: "unit", Project: ".", Source: "**/*.go",
		Postcondition: types.Postcondition{Postcondition: job.Postcondition{ID: "unit", Kind: job.PostconditionKindCheck}, Check: types.LeaseCheck{Target: "unit", Project: "."}},
	}
}

func TestUnrunGateFixAddsThePostconditionWithoutAnOwner(t *testing.T) {
	t.Parallel()

	ps := unrunGateProblems([]UnrunGate{unrunGate()}, gateOwner{})

	postcondition := unrunGate().Postcondition
	assert.Equal(t, types.JobProblems{{
		Code: types.GateNotRun, Field: "postconditions", Subject: "unit .",
		Message:       "ci runs `unit .` over the write paths (it reads **/*.go), and neither the check nor a postcondition runs it",
		Hint:          `add the postcondition {"id":"unit","kind":"check","check":{"target":"unit","project":"."}} to postconditions, or permit it in deviations: {"code":"MGS3045","subject":"unit .","reason":"the root runs it"}`,
		Postcondition: &postcondition,
	}}, ps, "the postcondition is the one remedy")
}

func TestUnrunGateFixNamesTheDeclaredOwner(t *testing.T) {
	t.Parallel()

	for _, runs := range []string{"github-actions runs it", integratorRuns} {
		ps := unrunGateProblems([]UnrunGate{unrunGate()}, gateOwner{runs: runs})

		assert.Equal(t, types.JobProblems{{
			Code: types.GateNotRun, Field: "postconditions", Subject: "unit .",
			Message:   "ci runs `unit .` over the write paths (it reads **/*.go), and neither the check nor a postcondition runs it",
			Hint:      fmt.Sprintf(`permit it in deviations, since %[1]s: {"code":"MGS3045","subject":"unit .","reason":"%[1]s"}`, runs),
			Deviation: &job.Deviation{Code: "MGS3045", Subject: "unit .", Reason: runs},
		}}, ps, "the owner's deviation is the one remedy")
	}
}

func TestUnrunLintGateFixStaysThePostconditionWhoeverOwnsTheRest(t *testing.T) {
	t.Parallel()

	lint := unrunGate()
	lint.Target = lintTarget

	ps := unrunGateProblems([]UnrunGate{lint}, gateOwner{runs: "github-actions runs it"})

	postcondition := lint.Postcondition
	assert.Equal(t, types.JobProblems{{
		Code: types.GateNotRun, Field: "postconditions", Subject: "lint .",
		Message:       "ci runs `lint .` over the write paths (it reads **/*.go), and neither the check nor a postcondition runs it",
		Hint:          fmt.Sprintf(`add the postcondition %s to postconditions, or permit it in deviations: {"code":"MGS3045","subject":"lint .","reason":"the root runs it"}`, `{"id":"unit","kind":"check","check":{"target":"unit","project":"."}}`),
		Postcondition: &postcondition,
	}}, ps)
}

func TestIntegratorGatesAskForNoFix(t *testing.T) {
	t.Parallel()

	gate := unrunGate()
	gate.IntegratorGates = true

	assert.Empty(t, unrunGateProblems([]UnrunGate{gate}, gateOwner{}))
}

func TestRegeneratedOutputFixOffersTheWritePathOrADeviation(t *testing.T) {
	t.Parallel()

	ps := regeneratedProblems([]RegeneratedOutput{{Target: ".:man-generate", Output: "manpage/*.1", Source: "cli/registry.go"}})

	assert.Equal(t, types.JobProblems{{
		Code: types.OutputOutsideWritePaths, Field: "paths.write", Subject: "manpage/*.1",
		Message:   ".:man-generate regenerates manpage/*.1 from cli/registry.go, inside the write paths, and manpage/*.1 sits outside them",
		Hint:      `add "manpage/*.1" to paths.write, or permit it in deviations: {"code":"MGS3044","subject":"manpage/*.1","reason":"the root regenerates it"}`,
		Deviation: &job.Deviation{Code: "MGS3044", Subject: "manpage/*.1", Reason: "the root regenerates it"},
	}}, ps)
}

// declaredGateSuite pins which owner the workspace declares for a worker's gates.
type declaredGateSuite struct {
	suite.Suite
	store *Store
}

func TestDeclaredGate(t *testing.T) { suite.Run(t, new(declaredGateSuite)) }

func (s *declaredGateSuite) SetupTest() {
	store, _ := validateWorkspace(s.T())
	s.store = store
}

func (s *declaredGateSuite) writeConfig(body string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.store.root, "magus.yaml"), []byte(body), 0o644))
}

func (s *declaredGateSuite) TestRemoteGateOwnsIt() {
	s.writeConfig("ci:\n  remote_gate: github-actions\n")

	s.Equal(gateOwner{runs: "github-actions runs it"}, declaredGateOwner(s.store.root, nil, job.Job{ID: "w"}))
}

func (s *declaredGateSuite) TestNoOwnerWithoutAConfigOrParent() {
	s.Equal(gateOwner{}, declaredGateOwner(s.store.root, nil, job.Job{ID: "w"}))
}

func (s *declaredGateSuite) TestIntegratingParentOwnsIt() {
	parent := job.Job{ID: "integrate", State: job.StateRunning, Integrates: []string{"a-branch"}}

	s.Equal(gateOwner{runs: integratorRuns}, declaredGateOwner(s.store.root, []job.Job{parent}, job.Job{ID: "w", Parent: "integrate"}))
}

func (s *declaredGateSuite) TestAnIntegrationJobOwnsItsOwnGates() {
	s.writeConfig("ci:\n  remote_gate: github-actions\n")

	s.Equal(gateOwner{}, declaredGateOwner(s.store.root, nil, job.Job{ID: "integrate", Integrates: []string{"a-branch"}}))
}

func (s *declaredGateSuite) TestUnreadableConfigDeclaresNoOwner() {
	s.writeConfig("ci: [\n")

	s.Equal(gateOwner{}, declaredGateOwner(s.store.root, nil, job.Job{ID: "w"}))
}

type acceptValidationSuite struct{ suite.Suite }

func TestAcceptValidation(t *testing.T) { suite.Run(t, new(acceptValidationSuite)) }

func (s *acceptValidationSuite) problems(accept ...job.Basis) types.JobProblems {
	st, _ := validateWorkspace(s.T())
	return validateMerge(s.T().Context(), st, nil, "w", func(u *job.Job) {
		u.Paths.Write = []string{"cli/registry.go"}
		u.Postconditions = []job.Postcondition{{ID: "unit", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "."}), Accept: accept}}
	}, config.Jobs{}, nil).Problems
}

func (s *acceptValidationSuite) at(ps types.JobProblems, field string) (types.JobProblem, bool) {
	i := slices.IndexFunc(ps, func(p types.JobProblem) bool { return p.Field == field })
	if i < 0 {
		return types.JobProblem{}, false
	}
	return ps[i], true
}

func (s *acceptValidationSuite) TestAnUnknownBasisIsAPostconditionProblem() {
	p, ok := s.at(s.problems(job.BasisWitnessed, "trusted"), "postconditions[0].accept[1]")

	s.Require().True(ok)
	s.Equal(types.JobPostconditionInvalid, p.Code)
	s.Contains(p.Message, `accepts "trusted", which is not a basis`)
}

func (s *acceptValidationSuite) TestANamedBasisIsNoProblem() {
	ps := s.problems(job.BasisWitnessed, job.BasisReported)

	_, ok := s.at(ps, "postconditions[0].accept")
	s.False(ok, codes(ps))
}

func (s *acceptValidationSuite) TestAForkResolvesAnUnnamedAcceptToWitnessed() {
	st, _ := validateWorkspace(s.T())

	plan, err := PlanFork(s.T().Context(), st, "w", func(u *job.Job) {
		u.Paths.Write = []string{"cli/registry.go"}
		u.Postconditions = []job.Postcondition{{ID: "unit", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "test", Project: "."})}}
	}, config.Jobs{}, nil, true)

	s.Require().NoError(err)
	unit := slices.IndexFunc(plan.Validation.Job.Postconditions, func(g job.Postcondition) bool { return g.ID == "unit" })
	s.Require().GreaterOrEqual(unit, 0)
	s.Equal([]job.Basis{job.BasisWitnessed}, plan.Validation.Job.Postconditions[unit].Accept)
	_, unresolved := s.at(plan.Validation.Problems, "postconditions[0].accept")
	s.False(unresolved)
}

type derivedLintSuite struct {
	suite.Suite
	store *Store
}

func TestDerivedLint(t *testing.T) { suite.Run(t, new(derivedLintSuite)) }

func (s *derivedLintSuite) SetupTest() {
	s.store, _ = validateWorkspace(s.T())
	s.store.notices = &bytes.Buffer{}
	for _, f := range []string{"a.go", "b.go"} {
		s.Require().NoError(os.WriteFile(filepath.Join(s.store.root, f), nil, 0o644))
	}
}

func (s *derivedLintSuite) apply(prev job.Job, merge func(*job.Job)) job.Job {
	resolved, err := resolveMerge(s.T().Context(), s.store, []job.Job{prev}, prev.ID, merge, nil)
	s.Require().NoError(err)
	next := prev.Clone()
	resolved(&next)
	return next
}

func (s *derivedLintSuite) lintArgs(row job.Job) []string {
	i := slices.IndexFunc(row.Postconditions, func(g job.Postcondition) bool { return g.ID == "lint" })
	s.Require().GreaterOrEqual(i, 0, "postconditions: %v", row.Postconditions)
	s.True(row.Postconditions[i].Derived)
	return checkIn(s.T(), row.Postconditions[i].Check).Args
}

func (s *derivedLintSuite) TestWidenedAndReleasedRowsKeepTheLintOnTheirWritePaths() {
	handWritten := job.Postcondition{ID: "lint-x", Check: fixtureCheck(types.LeaseCheck{Target: "format", Project: "."})}.Resolve()
	row := s.apply(job.Job{ID: "w"}, func(u *job.Job) {
		u.Paths.Write, u.Check, u.Postconditions = []string{"a.go"}, forkCheck(), []job.Postcondition{handWritten}
	})
	s.Equal([]string{"a.go"}, s.lintArgs(row))

	row = s.apply(row, func(u *job.Job) { u.Paths.Write = []string{"a.go", "b.go"} })
	s.Equal([]string{"a.go", "b.go"}, s.lintArgs(row), "a widened row lints what it now writes")

	row = s.apply(row, func(u *job.Job) { u.Paths.Write = []string{"b.go"} })
	s.Equal([]string{"b.go"}, s.lintArgs(row), "a released path leaves the lint")

	ids := make([]string, len(row.Postconditions))
	for i, g := range row.Postconditions {
		ids[i] = g.ID
	}
	s.Contains(ids, "lint-x", "a hand-written postcondition is never touched")
	s.Len(row.Postconditions, 2)
}
