package job

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

// gradeRecorder answers every grade with verdict and keeps the rows it was asked about and
// the roots it judged them in.
type gradeRecorder struct {
	verdict CheckVerdict
	graded  []types.Job
	roots   []string
}

func (r *gradeRecorder) grade(_ context.Context, root string, row types.Job) CheckVerdict {
	r.roots = append(r.roots, root)
	r.graded = append(r.graded, row)
	return r.verdict
}

func graderStore(t *testing.T, verdict CheckVerdict) (*Store, *gradeRecorder) {
	t.Helper()
	rec := &gradeRecorder{verdict: verdict}
	loc := tempLocation(t, testRoot(t).Root())
	loc.Grader = rec.grade
	return NewStore(loc), rec
}

func TestStoreGradesAgainstItsOwnRoot(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{})
	assert.Empty(t, deniedCheckProblems(t.Context(), s, types.Job{ID: "w", Check: forkCheck()}))
	assert.Equal(t, []string{s.root}, rec.roots)
	assert.Nil(t, tempStore(t, t.TempDir()).grader, "a Location that names no grader grades nothing")
}

func TestDeniedCheckProblemsQuoteTheVerdictAndNameATargetThatCompiles(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{Denied: true, Rule: "worker-builds-magus", Reason: "a worker does not build magus;\nask the main session"})
	build := &types.LeaseCheck{Target: "go-build", Project: "."}

	_, err := ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.WritePaths, u.Check = []string{"pkg/b.go"}, build }, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.JobCheckUnrunnable)
	ps := deniedCheckProblems(t.Context(), s, types.Job{ID: "w", WritePaths: []string{"pkg/b.go"}, Check: build})
	require.Len(t, ps, 1)
	assert.Equal(t, "check", ps[0].Field)
	assert.NotContains(t, ps[0].Message, "\n", "a message is one line")
	assert.Contains(t, err.Error(), "w's check `magus run go-build .` would be denied to its worker by the guard (worker-builds-magus)")
	assert.Contains(t, err.Error(), "a worker does not build magus; ask the main session", "the verdict is quoted")
	assert.Contains(t, err.Error(), "`go::go-vet .`", "the remedy names a target that compiles without linking the binary")
	assert.Contains(t, err.Error(), "--check 'go::go-vet .'")
	require.Len(t, rec.graded, 2)
	assert.Equal(t, "w", rec.graded[0].ID, "the grader is handed the row, its id the lease to grade under")
	assert.Equal(t, build, rec.graded[0].Check)

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused fork wrote nothing")

	_, err = Apply(t.Context(), s, []types.Declaration{{ID: "w", WritePaths: []string{"pkg/b.go"}, Check: build}}, config.Jobs{}, nil, nil, true)
	require.ErrorContains(t, err, "would be denied to its worker", "job apply grades too")
}

func TestDeniedCheckProblemsAcceptWhatTheGuardLets(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{})
	_, err := ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.WritePaths, u.Check = []string{"pkg/b.go"}, forkCheck() }, config.Jobs{}, nil)
	require.NoError(t, err)
	require.Len(t, rec.graded, 1)

	_, err = ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.Model = "opus" }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, rec.graded, 1, "an update that leaves the check alone is not graded again")

	_, err = ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.Check = &types.LeaseCheck{Target: "lint", Project: "."} }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, rec.graded, 2, "a changed check is")

	assert.Empty(t, deniedCheckProblems(t.Context(), tempStore(t, t.TempDir()), types.Job{ID: "w", Check: forkCheck()}), "a store with no grader finds nothing")
	_, err = ForkMerge(t.Context(), s, "scout", func(u *types.Job) { u.ReadOnly = true }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, rec.graded, 2, "a job with no check has nothing to grade")
}

// A grader that reached no verdict is not an accept: the fork is refused with one
// JobUngraded problem, however many checks the row declares, since the reason is the
// workspace's and not any one check's.
func TestDeniedCheckProblemsReportACheckNobodyGradedOnce(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{NotGraded: "the guard denies this worker `magus run go::go-vet .` as well: the hook binary is older than the tree"})
	row := types.Job{ID: "w", WritePaths: []string{"pkg/b.go"}, Check: forkCheck(), Goals: []types.Goal{
		{ID: "lint", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "lint", Project: "."}},
	}}

	ps := deniedCheckProblems(t.Context(), s, row)

	require.Len(t, ps, 1)
	assert.Equal(t, types.JobUngraded, ps[0].Code)
	assert.Equal(t, "check", ps[0].Field)
	assert.Contains(t, ps[0].Message, "w's check `magus run")
	assert.Contains(t, ps[0].Message, "the hook binary is older than the tree", "the reason no verdict was reached is quoted")
	assert.NotEmpty(t, ps[0].Fix)
	assert.Len(t, rec.graded, 1, "grading stops at the first check nobody could grade")

	_, err := ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.WritePaths, u.Check = row.WritePaths, row.Check }, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.JobUngraded)
}

// humanOnlyGrader denies the target bench as the guard's human-only rule does, and lets
// every other check through.
func humanOnlyGrader(t *testing.T) (*Store, *gradeRecorder) {
	t.Helper()
	rec := &gradeRecorder{}
	loc := tempLocation(t, testRoot(t).Root())
	loc.Grader = func(ctx context.Context, root string, row types.Job) CheckVerdict {
		rec.grade(ctx, root, row)
		if row.Check.Target != "bench" {
			return CheckVerdict{}
		}
		return CheckVerdict{Denied: true, Rule: "human-only", Reason: "`./magus run bench .` runs a target a person runs;\nlaunches paid sessions"}
	}
	return NewStore(loc), rec
}

func TestDeniedCheckProblemsWordAHumanOnlyCheckAsOneAPersonRuns(t *testing.T) {
	t.Parallel()

	s, _ := humanOnlyGrader(t)
	ps := deniedCheckProblems(t.Context(), s, types.Job{ID: "w", Check: &types.LeaseCheck{Target: "bench", Project: "."}})
	require.Len(t, ps, 1)
	p := ps[0]
	assert.NotContains(t, p.Message, "\n", "a message is one line")
	assert.Contains(t, p.Message, "w's check `magus run bench .` is a target a person runs (human-only)")
	assert.Contains(t, p.Message, "launches paid sessions", "the guard's reason is quoted")
	assert.Equal(t, types.JobProblem{
		Code:    types.JobCheckUnrunnable,
		Field:   "check",
		Message: p.Message,
		Fix:     "declare a check its worker may run in its place, with `magus job fork w --check '<target> <project>'`",
	}, p, "compiling is no stand-in for a run a person owns")
}

func TestDeniedCheckProblemsGradeEveryGoalCheck(t *testing.T) {
	t.Parallel()

	s, rec := humanOnlyGrader(t)
	lint := types.LeaseCheck{Target: "lint", Project: "."}
	row := types.Job{ID: "w", Check: &lint, Goals: []types.Goal{
		{ID: "again", Kind: types.GoalKindCheck, Check: lint},
		{ID: "paid", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "bench", Project: "."}},
	}}
	ps := deniedCheckProblems(t.Context(), s, row)
	require.Len(t, ps, 1)
	assert.Equal(t, "goals[1].check", ps[0].Field)
	assert.Contains(t, ps[0].Message, "w's goal paid's check `magus run bench .` is a target a person runs (human-only)")
	require.Len(t, rec.graded, 2, "a check two goals share is graded once")
	assert.Equal(t, &lint, rec.graded[0].Check)
	assert.Equal(t, "bench", rec.graded[1].Check.Target, "a goal's check is graded as the row with that check")
	assert.Equal(t, row.Goals, rec.graded[1].Goals)

	goalsOnly := types.Job{ID: "g", Goals: []types.Goal{{ID: "paid", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "bench", Project: "."}}}}
	require.ErrorContains(t, deniedCheckProblems(t.Context(), s, goalsOnly).Err(), "g's goal paid's check", "a row with no check of its own still has its goals graded")

	assert.Empty(t, deniedCheckProblems(t.Context(), s, types.Job{ID: "ok", Check: &lint}), "an ordinary check passes")

	both := types.Job{ID: "b", Goals: []types.Goal{
		{ID: "paid", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "bench", Project: "."}},
		{ID: "paid-too", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "bench", Project: "docs"}},
	}}
	assert.Len(t, deniedCheckProblems(t.Context(), s, both), 2, "every denied check is found, not the first")
}

// A grader that could not load the workspace's rules defers the grade: the row is stored,
// nothing is refused, and grading stops at the first check since the reason holds for all.
func TestDeniedCheckProblemsDeferWhenTheRulesDidNotLoad(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{Deferred: "the workspace's rules did not load: magusfile.buzz:3: expected ';'"})
	row := types.Job{ID: "w", WritePaths: []string{"pkg/b.go"}, Check: forkCheck(), Goals: []types.Goal{
		{ID: "lint", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "lint", Project: "."}},
	}}

	assert.Empty(t, deniedCheckProblems(t.Context(), s, row))
	assert.Len(t, rec.graded, 1, "grading stops at the first deferred check")

	_, err := ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.WritePaths, u.Check = row.WritePaths, row.Check }, config.Jobs{}, nil)
	require.NoError(t, err)
}
