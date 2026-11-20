package job

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/types"
)

// gradeRecorder answers every grade with verdict and keeps the rows it was asked about and
// the roots it judged them in.
type gradeRecorder struct {
	verdict CheckVerdict
	graded  []job.Job
	roots   []string
}

func (r *gradeRecorder) grade(_ context.Context, root string, row job.Job) CheckVerdict {
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

// ReplacementCheckSuite pins what a refused check is told to become.
type ReplacementCheckSuite struct {
	suite.Suite
}

func TestReplacementCheckSuite(t *testing.T) {
	suite.Run(t, new(ReplacementCheckSuite))
}

func (s *ReplacementCheckSuite) fix(paths ...string) string {
	return replacementCheckFix(job.Job{ID: "w", Paths: job.Paths{Write: paths}})
}

func (s *ReplacementCheckSuite) TestABuzzRowIsOfferedAScriptCheckNotGoVet() {
	got := s.fix("hack/dev/a.buzz")
	s.Equal(`declare the script check that tests what it writes, with "check": {"script": "hack/dev/a.buzz", "test": true}`, got)
}

func (s *ReplacementCheckSuite) TestARowWritingNeitherIsAskedForAnyCheckThatGradesIt() {
	s.NotContains(s.fix("docs/a.md"), "go-vet")
}

func (s *ReplacementCheckSuite) TestAGoRowIsOfferedTheCompilingCheck() {
	s.Contains(s.fix("internal/a/*.go", "hack/dev/a.buzz"), "go-vet")
}

func (s *ReplacementCheckSuite) TestAnUngradedCheckNamesTheNewerBinaryBeforeDoctor() {
	got := ungradedCheckProblem("w", "check", types.LeaseCheck{Target: "test", Project: "."}, "the guard denies").Hint
	s.Contains(got, "built at this checkout's HEAD or later")
	s.Contains(got, "doctor")
}

func TestStoreGradesAgainstItsOwnRoot(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{})
	assert.Empty(t, deniedCheckProblems(t.Context(), s, job.Job{ID: "w", Check: forkCheck()}))
	assert.Equal(t, []string{s.root}, rec.roots)
}

// NoGraderSuite pins that a store whose Location names no grader never grades nothing in
// silence.
type NoGraderSuite struct {
	suite.Suite
	store *Store
}

func TestNoGraderSuite(t *testing.T) { suite.Run(t, new(NoGraderSuite)) }

func (s *NoGraderSuite) SetupTest() {
	loc := tempLocation(s.T(), s.T().TempDir())
	loc.Grader = nil
	s.store = NewStore(loc)
}

func (s *NoGraderSuite) TestACheckIsOneUngradedProblem() {
	ps := deniedCheckProblems(s.T().Context(), s.store, job.Job{ID: "w", Check: forkCheck()})

	s.Require().Len(ps, 1)
	s.Equal(types.JobUngraded, ps[0].Code)
	s.Contains(ps[0].Message, "no check grader")
	s.Contains(ps[0].Hint, "Grader")
}

func (s *NoGraderSuite) TestAGateIsOfferedUngradedRatherThanPassed() {
	row := job.Job{ID: "w", Check: forkCheck()}

	verdict := gradeWith(nil, s.store.root, row)(types.LeaseCheck{Target: "test", Project: "."})

	s.Equal(noGraderReason, verdict.NotGraded)
	s.False(verdict.Denied)
}

func (s *NoGraderSuite) TestAJobWithNoCheckFindsNothing() {
	s.Empty(deniedCheckProblems(s.T().Context(), s.store, job.Job{ID: "w"}))
}

func TestDeniedCheckProblemsQuoteTheVerdictAndNameATargetThatCompiles(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{Denied: true, Rule: "worker-builds-magus", Reason: "a worker does not build magus;\nask the main session"})
	build := &types.LeaseCheck{Target: "go-build", Project: "."}

	_, err := ForkMerge(t.Context(), s, "w", func(u *job.Job) { u.Paths.Write, u.Check = []string{"pkg/b.go"}, fixtureCheck(*build) }, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.JobCheckUnrunnable)
	ps := deniedCheckProblems(t.Context(), s, job.Job{ID: "w", Paths: job.Paths{Write: []string{"pkg/b.go"}}, Check: fixtureCheck(*build)})
	require.Len(t, ps, 1)
	assert.Equal(t, "check", ps[0].Field)
	assert.NotContains(t, ps[0].Message, "\n", "a message is one line")
	assert.Contains(t, err.Error(), "w's check `magus run go-build .` would be denied to its worker by the guard (worker-builds-magus)")
	assert.Contains(t, err.Error(), "a worker does not build magus; ask the main session", "the verdict is quoted")
	assert.Contains(t, err.Error(), "`go::go-vet .`", "the remedy names a target that compiles without linking the binary")
	assert.Contains(t, err.Error(), "--check 'go::go-vet .'")
	require.Len(t, rec.graded, 2)
	assert.Equal(t, "w", rec.graded[0].ID, "the grader is handed the row, its id the lease to grade under")
	assert.Equal(t, *build, checkIn(t, rec.graded[0].Check))

	rows, err := s.List(t.Context())
	require.NoError(t, err)
	assert.Empty(t, rows, "a refused fork wrote nothing")

	_, err = Apply(t.Context(), s, []job.Declaration{{ID: "w", Paths: job.Paths{Write: []string{"pkg/b.go"}}, Check: fixtureCheck(*build)}}, config.Jobs{}, nil, nil, true)
	require.ErrorContains(t, err, "would be denied to its worker", "job apply grades too")
}

func TestDeniedCheckProblemsAcceptWhatTheGuardLets(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{})
	_, err := ForkMerge(t.Context(), s, "w", func(u *job.Job) { u.Paths.Write, u.Check = []string{"pkg/b.go"}, forkCheck() }, config.Jobs{}, nil)
	require.NoError(t, err)
	require.Len(t, rec.graded, 1)

	_, err = ForkMerge(t.Context(), s, "w", func(u *job.Job) { u.Agent = modelRequest("opus") }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, rec.graded, 1, "an update that leaves the check alone is not graded again")

	_, err = ForkMerge(t.Context(), s, "w", func(u *job.Job) { u.Check = fixtureCheck(types.LeaseCheck{Target: "lint", Project: "."}) }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, rec.graded, 2, "a changed check is")

	assert.Empty(t, deniedCheckProblems(t.Context(), tempStore(t, t.TempDir()), job.Job{ID: "w", Check: forkCheck()}), "a store with no grader finds nothing")
	_, err = ForkMerge(t.Context(), s, "scout", func(u *job.Job) { u.ReadOnly = true }, config.Jobs{}, nil)
	require.NoError(t, err)
	assert.Len(t, rec.graded, 2, "a job with no check has nothing to grade")
}

// A grader that reached no verdict is not an accept: the fork is refused with one
// JobUngraded problem, however many checks the row declares, since the reason is the
// workspace's and not any one check's.
func TestDeniedCheckProblemsReportACheckNobodyGradedOnce(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{NotGraded: "the guard denies this worker `magus run go::go-vet .` as well: the hook binary is older than the tree"})
	row := job.Job{ID: "w", Paths: job.Paths{Write: []string{"pkg/b.go"}}, Check: forkCheck(), Postconditions: []job.Postcondition{
		{ID: "lint", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "lint", Project: "."})},
	}}

	ps := deniedCheckProblems(t.Context(), s, row)

	require.Len(t, ps, 1)
	assert.Equal(t, types.JobUngraded, ps[0].Code)
	assert.Equal(t, "check", ps[0].Field)
	assert.Contains(t, ps[0].Message, "w's check `magus run")
	assert.Contains(t, ps[0].Message, "the hook binary is older than the tree", "the reason no verdict was reached is quoted")
	assert.NotEmpty(t, ps[0].Hint)
	assert.Len(t, rec.graded, 1, "grading stops at the first check nobody could grade")

	_, err := ForkMerge(t.Context(), s, "w", func(u *job.Job) { u.Paths.Write, u.Check = row.Paths.Write, row.Check }, config.Jobs{}, nil)
	require.ErrorIs(t, err, types.JobUngraded)
}

// humanOnlyGrader denies the target bench as the guard's human-only rule does, and lets
// every other check through.
func humanOnlyGrader(t *testing.T) (*Store, *gradeRecorder) {
	t.Helper()
	rec := &gradeRecorder{}
	loc := tempLocation(t, testRoot(t).Root())
	loc.Grader = func(ctx context.Context, root string, row job.Job) CheckVerdict {
		rec.grade(ctx, root, row)
		if checkIn(t, row.Check).Target != "bench" {
			return CheckVerdict{}
		}
		return CheckVerdict{Denied: true, Rule: "human-only", Reason: "`./magus run bench .` runs a target a person runs;\nlaunches paid sessions"}
	}
	return NewStore(loc), rec
}

func TestDeniedCheckProblemsWordAHumanOnlyCheckAsOneAPersonRuns(t *testing.T) {
	t.Parallel()

	s, _ := humanOnlyGrader(t)
	ps := deniedCheckProblems(t.Context(), s, job.Job{ID: "w", Check: fixtureCheck(types.LeaseCheck{Target: "bench", Project: "."})})
	require.Len(t, ps, 1)
	p := ps[0]
	assert.NotContains(t, p.Message, "\n", "a message is one line")
	assert.Contains(t, p.Message, "w's check `magus run bench .` is a target a person runs (human-only)")
	assert.Contains(t, p.Message, "launches paid sessions", "the guard's reason is quoted")
	assert.Equal(t, types.JobProblem{
		Code:    types.JobCheckUnrunnable,
		Field:   "check",
		Message: p.Message,
		Hint:    "declare a check its worker may run in its place, with `magus job fork w --check '<target> <project>'`",
	}, p, "compiling is no stand-in for a run a person owns")
}

func TestDeniedCheckProblemsGradeEveryPostconditionCheck(t *testing.T) {
	t.Parallel()

	s, rec := humanOnlyGrader(t)
	lint := types.LeaseCheck{Target: "lint", Project: "."}
	row := job.Job{ID: "w", Check: fixtureCheck(lint), Postconditions: []job.Postcondition{
		{ID: "again", Kind: job.PostconditionKindCheck, Check: fixtureCheck(lint)},
		{ID: "paid", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "bench", Project: "."})},
	}}
	ps := deniedCheckProblems(t.Context(), s, row)
	require.Len(t, ps, 1)
	assert.Equal(t, "postconditions[1].check", ps[0].Field)
	assert.Contains(t, ps[0].Message, "w's postcondition paid's check `magus run bench .` is a target a person runs (human-only)")
	require.Len(t, rec.graded, 2, "a check two postconditions share is graded once")
	assert.Equal(t, lint, checkIn(t, rec.graded[0].Check))
	assert.Equal(t, "bench", checkIn(t, rec.graded[1].Check).Target, "a postcondition's check is graded as the row with that check")
	assert.Equal(t, row.Postconditions, rec.graded[1].Postconditions)

	postconditionsOnly := job.Job{ID: "g", Postconditions: []job.Postcondition{{ID: "paid", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "bench", Project: "."})}}}
	require.ErrorContains(t, deniedCheckProblems(t.Context(), s, postconditionsOnly).Error(), "g's postcondition paid's check", "a row with no check of its own still has its postconditions graded")

	assert.Empty(t, deniedCheckProblems(t.Context(), s, job.Job{ID: "ok", Check: fixtureCheck(lint)}), "an ordinary check passes")

	both := job.Job{ID: "b", Postconditions: []job.Postcondition{
		{ID: "paid", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "bench", Project: "."})},
		{ID: "paid-too", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "bench", Project: "docs"})},
	}}
	assert.Len(t, deniedCheckProblems(t.Context(), s, both), 2, "every denied check is found, not the first")
}

// A grader that could not load the workspace's rules defers the grade: the row is stored,
// nothing is refused, and grading stops at the first check since the reason holds for all.
func TestDeniedCheckProblemsDeferWhenTheRulesDidNotLoad(t *testing.T) {
	t.Parallel()

	s, rec := graderStore(t, CheckVerdict{Deferred: "the workspace's rules did not load: magusfile.buzz:3: expected ';'"})
	row := job.Job{ID: "w", Paths: job.Paths{Write: []string{"pkg/b.go"}}, Check: forkCheck(), Postconditions: []job.Postcondition{
		{ID: "lint", Kind: job.PostconditionKindCheck, Check: fixtureCheck(types.LeaseCheck{Target: "lint", Project: "."})},
	}}

	assert.Empty(t, deniedCheckProblems(t.Context(), s, row))
	assert.Len(t, rec.graded, 1, "grading stops at the first deferred check")

	_, err := ForkMerge(t.Context(), s, "w", func(u *job.Job) { u.Paths.Write, u.Check = row.Paths.Write, row.Check }, config.Jobs{}, nil)
	require.NoError(t, err)
}

// KeptVerdictsSuite pins what a KeptVerdicts answers without judging: a verdict reached under
// the same key, never a deferral and never an unkeyed grade.
type KeptVerdictsSuite struct {
	suite.Suite
	kept *KeptVerdicts
}

func TestKeptVerdictsSuite(t *testing.T) {
	suite.Run(t, new(KeptVerdictsSuite))
}

func (s *KeptVerdictsSuite) SetupTest() {
	s.kept = NewKeptVerdicts()
}

func (s *KeptVerdictsSuite) TestAKeptVerdictIsReadBack() {
	denied := CheckVerdict{Denied: true, Rule: "r", Reason: "no"}
	s.Equal(denied, s.kept.Grade("k", func() CheckVerdict { return denied }))
	s.Equal(denied, s.kept.Grade("k", func() CheckVerdict { return CheckVerdict{} }))
	s.Equal(1, s.kept.Judged())
}

func (s *KeptVerdictsSuite) TestAnotherKeyJudgesAgain() {
	s.kept.Grade("k", func() CheckVerdict { return CheckVerdict{} })
	s.kept.Grade("other", func() CheckVerdict { return CheckVerdict{} })
	s.Equal(2, s.kept.Judged())
}

func (s *KeptVerdictsSuite) TestADeferralIsJudgedAgain() {
	s.kept.Grade("k", func() CheckVerdict { return CheckVerdict{Deferred: "ran out of time"} })
	s.Equal(CheckVerdict{}, s.kept.Grade("k", func() CheckVerdict { return CheckVerdict{} }))
	s.Equal(2, s.kept.Judged())
}

func (s *KeptVerdictsSuite) TestAnEmptyKeyKeepsNothing() {
	s.kept.Grade("", func() CheckVerdict { return CheckVerdict{} })
	s.kept.Grade("", func() CheckVerdict { return CheckVerdict{} })
	s.Equal(2, s.kept.Judged())
}
