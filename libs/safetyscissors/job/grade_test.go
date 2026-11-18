package job

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/codec"
)

// gradingSuite is the store every grading suite declares job j1 in: its tree reads
// s.tree, and its check command is s.argv run in s.work.
type gradingSuite struct {
	suite.Suite
	dir, work, tree string
	argv            []string
	store           *Store
}

func (s *gradingSuite) SetupTest() {
	s.dir, s.work, s.tree = s.T().TempDir(), s.T().TempDir(), "tree-1"
	s.argv = []string{"sh", "-c", "echo ok"}
	s.store = s.open()
}

func (s *gradingSuite) open(opts ...StoreOption) *Store {
	st, err := Open(s.dir, append(writable(
		WithTree(func(context.Context) (string, error) { return s.tree, nil }),
		WithCheckCommand(func(Job, Goal) ([]string, string, error) { return s.argv, s.work, nil }),
	), opts...)...)
	s.Require().NoError(err)
	return st
}

func testCheck() Check { return checkJSON("test", ".") }

func checkGoalAccepting(id string, accept ...Basis) Goal {
	return Goal{ID: id, Kind: GoalKindCheck, Expect: ExpectPassed, Check: testCheck(), Accept: accept}
}

func (s *gradingSuite) declare(goals ...Goal) {
	_, err := s.store.Update(s.T().Context(), "j1", func(j *Job) { j.Goals = goals })
	s.Require().NoError(err)
}

func (s *gradingSuite) report(check Check) RunRef {
	ref, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Goal: "tests", Check: check, By: "worker"})
	s.Require().NoError(err)
	return ref
}

func (s *gradingSuite) grade(ref RunRef) GoalGrade {
	g, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{"tests": ref})
	s.Require().NoError(err)
	s.Require().Len(g.Goals, 1)
	s.Equal(g.Verified, g.Goals[0].Verified)
	return g.Goals[0]
}

type GradeSuite struct{ gradingSuite }

func TestGradeSuite(t *testing.T) { suite.Run(t, new(GradeSuite)) }

func (s *GradeSuite) TestAReportDoesNotVerifyAGoalAcceptingOnlyWitnessed() {
	s.declare(checkGoalAccepting("tests", BasisWitnessed))

	g := s.grade(s.report(testCheck()))

	s.False(g.Verified)
	s.True(g.Passed)
	s.Equal(BasisReported, g.Basis)
	s.Contains(g.Violations, "the run is reported and this goal accepts only [witnessed]")
}

func (s *GradeSuite) TestAReportVerifiesAGoalThatAcceptsIt() {
	s.declare(checkGoalAccepting("tests", BasisWitnessed, BasisReported))

	g := s.grade(s.report(testCheck()))

	s.True(g.Verified, g.Violations)
	s.Equal(BasisReported, g.Basis)
}

func (s *GradeSuite) TestAGoalCitingNoRunIsNotVerified() {
	s.declare(checkGoalAccepting("tests", BasisReported))

	g := s.grade("")

	s.False(g.Verified)
	s.Equal([]string{"cites no run"}, g.Violations)
}

func (s *GradeSuite) TestAnInventedRefIsNotARun() {
	s.declare(checkGoalAccepting("tests", BasisReported))

	g := s.grade("run-0123456789abcdef")

	s.False(g.Verified)
	s.Equal([]string{`no run is recorded for ref "run-0123456789abcdef"`}, g.Violations)
}

func (s *GradeSuite) TestAnotherGoalsRunDoesNotGradeThisOne() {
	lint := checkGoalAccepting("lint", BasisReported)
	s.declare(checkGoalAccepting("tests", BasisReported), lint)
	ref := s.report(testCheck())

	g, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{"tests": ref, "lint": ref})

	s.Require().NoError(err)
	s.Require().Len(g.Goals, 2)
	s.False(g.Verified)
	s.True(g.Goals[0].Verified, g.Goals[0].Violations)
	s.Contains(g.Goals[1].Violations[0], `goal "tests"'s run, not this goal's`)
}

func (s *GradeSuite) TestARunOfAnOlderTreeIsStale() {
	s.declare(checkGoalAccepting("tests", BasisReported))
	ref := s.report(testCheck())
	s.tree = "tree-2"

	g := s.grade(ref)

	s.False(g.Verified)
	s.True(g.Passed)
	s.Contains(g.Violations, "the run read tree tree-1 and the tree is now tree-2")
}

func (s *GradeSuite) TestARunOfAnotherCheckIsAViolation() {
	s.declare(checkGoalAccepting("tests", BasisReported))

	g := s.grade(s.report(checkJSON("lint", ".")))

	s.False(g.Verified)
	s.Contains(g.Violations, "the run is of `lint .` and this goal's check is `test .`")
}

func (s *GradeSuite) TestARefForAGoalTheJobDoesNotDeclareIsRefused() {
	s.declare(checkGoalAccepting("tests", BasisReported))

	_, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{"nope": "run-1"})

	refused, ok := errors.AsType[*GoalError](err)
	s.Require().True(ok, err)
	s.Equal(GoalError{Job: "j1", Goal: "nope"}, *refused)
}

func (s *GradeSuite) TestAJobWithNoCheckGoalNeverVerifies() {
	s.declare(Goal{ID: "docs", Kind: GoalKindPaths, Expect: ExpectChanged, Paths: []string{"docs"}})

	g, err := s.store.Grade(s.T().Context(), "j1", nil)

	s.Require().NoError(err)
	s.False(g.Verified, "a grade over no check goal is no pass")
	s.Empty(g.Goals)
}

func (s *GradeSuite) TestAnUnknownJobIsRefused() {
	_, err := s.store.Grade(s.T().Context(), "nope", nil)

	s.ErrorIs(err, ErrUnknownJob)
}

func (s *GradeSuite) TestGradingWithNoTreeIsRefused() {
	st, err := Open(s.dir, writable()...)
	s.Require().NoError(err)

	_, err = st.Grade(s.T().Context(), "j1", nil)

	refused, ok := errors.AsType[*GradingUnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal(GradingUnconfiguredError{Op: "Grade", Option: "WithTree"}, *refused)
}

func (s *GradeSuite) TestRunsAndGradesAreLinesOfGradesJSONL() {
	s.declare(checkGoalAccepting("tests", BasisReported))
	ref := s.report(testCheck())
	s.grade(ref)

	read, err := codec.ReadLines(filepath.Join(s.dir, gradesFileName))
	s.Require().NoError(err)
	s.Require().Len(read.Lines, 2)
	var kinds []string
	for _, line := range read.Lines {
		var probe struct {
			Kind string `json:"kind"`
		}
		s.Require().NoError(codec.Unmarshal(line.Text, &probe))
		kinds = append(kinds, probe.Kind)
	}
	s.Equal([]string{"Run", "Grade"}, kinds)
	s.store = s.open()
	s.True(s.grade(ref).Verified, "a reopened store reads the run back")
}

func (s *GradeSuite) TestALineOfAnotherKindIsRefused() {
	s.declare(checkGoalAccepting("tests", BasisReported))
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, gradesFileName),
		[]byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Verdict"}`+"\n"), 0o600))

	_, err := s.store.Grade(s.T().Context(), "j1", nil)

	s.ErrorIs(err, codec.ErrLineUndecodable)
}

// An older build's camelCase run line carries no api_version, so it is refused rather
// than read as a run of nothing.
func (s *GradeSuite) TestAnOlderBuildsRunLineIsRefused() {
	s.declare(checkGoalAccepting("tests", BasisReported))
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, gradesFileName),
		[]byte(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"Run","ref":"run-1","job":"j1","goal":"tests","basis":"Reported"}`+"\n"), 0o600))

	_, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{"tests": "run-1"})

	s.ErrorIs(err, codec.ErrLineUndecodable)
}

func (s *GradeSuite) TestARunsAndAGradesLinesArePinned() {
	rec := recordCheckRun(run{job: "j1", goal: "tests", basis: BasisWitnessed, check: checkJSON("test", "."),
		argv: []string{"magus", "run"}, tree: "t1", exit: 1, started: time.UnixMilli(5), ended: time.UnixMilli(6), moved: true})
	rec.Ref = "run-1"
	grade := recordGrade(Grade{Job: "j1", Goals: []GoalGrade{{Goal: "tests", Ref: "run-1", Basis: BasisAttested, Passed: true}}}, time.UnixMilli(7))

	runLine, err := codec.Marshal(gradesEntry{run: &rec})
	s.Require().NoError(err)
	gradeLine, err := codec.Marshal(gradesEntry{grade: grade})
	s.Require().NoError(err)

	s.Equal(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Run","ref":"run-1","job":"j1","goal":"tests","basis":"witnessed",`+
		`"check":{"target":"test","project":"."},"argv":["magus","run"],"tree":"t1","exit":1,"started":5,"ended":6,"moved":true}`, string(runLine))
	s.Equal(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Grade","job":"j1","graded":7,"verified":false,`+
		`"goals":[{"goal":"tests","ref":"run-1","basis":"attested","passed":true,"verified":false}]}`, string(gradeLine))
}
