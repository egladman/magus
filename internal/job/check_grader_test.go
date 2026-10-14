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
	loc := tmpLoc(t, testRoot(t).Root())
	loc.Grader = rec.grade
	return NewStore(loc), rec
}

func TestStoreGradesAgainstItsOwnRoot(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{})
	require.NoError(t, RefuseDeniedCheck(t.Context(), s, types.Job{ID: "w", Check: forkCheck()}))
	assert.Equal(t, []string{s.root}, rec.roots)
	assert.Nil(t, tmpStore(t, t.TempDir()).grader, "a Location that names no grader grades nothing")
}

func TestRefuseDeniedCheckQuotesTheVerdictAndNamesATargetThatCompiles(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{Denied: true, Rule: "worker-builds-magus", Reason: "a worker does not build magus;\nask the main session"})
	build := &types.LeaseCheck{Target: "go-build", Project: "."}

	_, err := ForkMerge(t.Context(), s, "w", func(u *types.Job) { u.WritePaths, u.Check = []string{"pkg/b.go"}, build }, config.Jobs{}, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "\n", "an error is one line")
	assert.Contains(t, err.Error(), "w's check `magus run go-build .` would be denied to its worker by the guard (worker-builds-magus)")
	assert.Contains(t, err.Error(), "a worker does not build magus; ask the main session", "the verdict is quoted")
	assert.Contains(t, err.Error(), "`go::go-vet .`", "the remedy names a target that compiles without linking the binary")
	assert.Contains(t, err.Error(), "--check 'go::go-vet .'")
	require.Len(t, rec.graded, 1)
	assert.Equal(t, "w", rec.graded[0].ID, "the grader is handed the row, its id the lease to grade under")
	assert.Equal(t, build, rec.graded[0].Check)

	rows, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused fork wrote nothing")

	_, err = Apply(t.Context(), s, []types.Declaration{{ID: "w", WritePaths: []string{"pkg/b.go"}, Check: build}}, config.Jobs{}, nil, nil, true)
	require.ErrorContains(t, err, "would be denied to its worker", "job apply grades too")
}

func TestRefuseDeniedCheckAcceptsWhatTheGuardLets(t *testing.T) {
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

	require.NoError(t, RefuseDeniedCheck(t.Context(), tmpStore(t, t.TempDir()), types.Job{ID: "w", Check: forkCheck()}), "a store with no grader refuses nothing")
	_, err = ForkMerge(t.Context(), s, "scout", func(u *types.Job) { u.ReadOnly = true }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, rec.graded, 2, "a job with no check has nothing to grade")
}

// humanOnlyGrader denies the target bench as the guard's human-only rule does, and lets
// every other check through.
func humanOnlyGrader(t *testing.T) (*Store, *gradeRecorder) {
	t.Helper()
	rec := &gradeRecorder{}
	loc := tmpLoc(t, testRoot(t).Root())
	loc.Grader = func(ctx context.Context, root string, row types.Job) CheckVerdict {
		rec.grade(ctx, root, row)
		if row.Check.Target != "bench" {
			return CheckVerdict{}
		}
		return CheckVerdict{Denied: true, Rule: "human-only", Reason: "`./magus run bench .` runs a target a person runs;\nlaunches paid sessions"}
	}
	return NewStore(loc), rec
}

func TestRefuseDeniedCheckWordsAHumanOnlyCheckAsOneAPersonRuns(t *testing.T) {
	t.Parallel()

	s, _ := humanOnlyGrader(t)
	err := RefuseDeniedCheck(t.Context(), s, types.Job{ID: "w", Check: &types.LeaseCheck{Target: "bench", Project: "."}})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "\n", "an error is one line")
	assert.Contains(t, err.Error(), "w's check `magus run bench .` is a target a person runs (human-only)")
	assert.Contains(t, err.Error(), "launches paid sessions", "the guard's reason is quoted")
	assert.Contains(t, err.Error(), "declare a check its worker may run in its place, with `magus job fork w --check '<target> <project>'`")
	assert.NotContains(t, err.Error(), "go-vet", "compiling is no stand-in for a run a person owns")
}

func TestRefuseDeniedCheckGradesEveryGoalCheck(t *testing.T) {
	t.Parallel()

	s, rec := humanOnlyGrader(t)
	lint := types.LeaseCheck{Target: "lint", Project: "."}
	row := types.Job{ID: "w", Check: &lint, Goals: []types.Goal{
		{ID: "again", Kind: types.GoalKindCheck, Check: lint},
		{ID: "paid", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "bench", Project: "."}},
	}}
	err := RefuseDeniedCheck(t.Context(), s, row)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "w's goal paid's check `magus run bench .` is a target a person runs (human-only)")
	require.Len(t, rec.graded, 2, "a check two goals share is graded once")
	assert.Equal(t, &lint, rec.graded[0].Check)
	assert.Equal(t, "bench", rec.graded[1].Check.Target, "a goal's check is graded as the row with that check")
	assert.Equal(t, row.Goals, rec.graded[1].Goals)

	goalsOnly := types.Job{ID: "g", Goals: []types.Goal{{ID: "paid", Kind: types.GoalKindCheck, Check: types.LeaseCheck{Target: "bench", Project: "."}}}}
	require.ErrorContains(t, RefuseDeniedCheck(t.Context(), s, goalsOnly), "g's goal paid's check", "a row with no check of its own still has its goals graded")

	require.NoError(t, RefuseDeniedCheck(t.Context(), s, types.Job{ID: "ok", Check: &lint}), "an ordinary check passes")
}
