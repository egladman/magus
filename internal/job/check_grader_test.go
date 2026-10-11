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
// the roots it was built for.
type gradeRecorder struct {
	verdict CheckVerdict
	graded  []types.Job
	roots   []string
}

func (r *gradeRecorder) forRoot(root string) CheckGrader {
	r.roots = append(r.roots, root)
	return func(_ context.Context, row types.Job) CheckVerdict {
		r.graded = append(r.graded, row)
		return r.verdict
	}
}

func graderStore(t *testing.T, verdict CheckVerdict) (*Store, *gradeRecorder) {
	t.Helper()
	rec := &gradeRecorder{verdict: verdict}
	loc := tmpLoc(t, testRoot(t).Root())
	loc.Grader = rec.forRoot
	return NewStore(loc), rec
}

func TestStoreBuildsItsGraderForItsRoot(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{})
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
