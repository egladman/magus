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

func (s *gradingSuite) open(opts ...Option) *Store {
	st, err := Open(s.T().Context(), Config{Dir: s.dir}, append(writable(
		WithTree(func(context.Context) (string, error) { return s.tree, nil }),
		WithCheckCommand(func(Job, Postcondition) ([]string, string, error) { return s.argv, s.work, nil }),
	), opts...)...)
	s.Require().NoError(err)
	return st
}

func testCheck() Check { return checkJSON("test", ".") }

func checkPostconditionAccepting(id string, accept ...Basis) Postcondition {
	return Postcondition{ID: id, Kind: PostconditionKindCheck, Expect: ExpectPassed, Check: testCheck(), Accept: accept}
}

func (s *gradingSuite) declare(postconditions ...Postcondition) {
	_, err := s.store.Update(s.T().Context(), "j1", func(j *Job) { j.Postconditions = postconditions })
	s.Require().NoError(err)
}

func (s *gradingSuite) report(check Check) RunRef {
	ref, err := s.store.ReportRun(s.T().Context(), Report{Job: "j1", Postcondition: "tests", Check: check, By: "worker"})
	s.Require().NoError(err)
	return ref
}

func (s *gradingSuite) grade(ref RunRef) PostconditionGrade {
	g, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{"tests": ref})
	s.Require().NoError(err)
	s.Require().Len(g.Postconditions, 1)
	s.Equal(g.Verified, g.Postconditions[0].Verified)
	return g.Postconditions[0]
}

type GradeSuite struct{ gradingSuite }

func TestGradeSuite(t *testing.T) { suite.Run(t, new(GradeSuite)) }

func (s *GradeSuite) TestAReportDoesNotVerifyAPostconditionAcceptingOnlyWitnessed() {
	s.declare(checkPostconditionAccepting("tests", BasisWitnessed))

	g := s.grade(s.report(testCheck()))

	s.False(g.Verified)
	s.True(g.Passed)
	s.Equal(BasisReported, g.Basis)
	s.Contains(g.Violations, "the run is reported and this postcondition accepts only [witnessed]")
}

func (s *GradeSuite) TestAReportVerifiesAPostconditionThatAcceptsIt() {
	s.declare(checkPostconditionAccepting("tests", BasisWitnessed, BasisReported))

	g := s.grade(s.report(testCheck()))

	s.True(g.Verified, g.Violations)
	s.Equal(BasisReported, g.Basis)
}

func (s *GradeSuite) TestAPostconditionCitingNoRunIsNotVerified() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))

	g := s.grade("")

	s.False(g.Verified)
	s.Equal([]string{"cites no run"}, g.Violations)
}

func (s *GradeSuite) TestAnInventedRefIsNotARun() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))

	g := s.grade("run-0123456789abcdef")

	s.False(g.Verified)
	s.Equal([]string{`no run is recorded for ref "run-0123456789abcdef"`}, g.Violations)
}

func (s *GradeSuite) TestAnotherPostconditionsRunDoesNotGradeThisOne() {
	lint := checkPostconditionAccepting("lint", BasisReported)
	s.declare(checkPostconditionAccepting("tests", BasisReported), lint)
	ref := s.report(testCheck())

	g, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{"tests": ref, "lint": ref})

	s.Require().NoError(err)
	s.Require().Len(g.Postconditions, 2)
	s.False(g.Verified)
	s.True(g.Postconditions[0].Verified, g.Postconditions[0].Violations)
	s.Contains(g.Postconditions[1].Violations[0], `postcondition "tests"'s run, not this postcondition's`)
}

func (s *GradeSuite) TestARunOfAnOlderTreeIsStale() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))
	ref := s.report(testCheck())
	s.tree = "tree-2"

	g := s.grade(ref)

	s.False(g.Verified)
	s.True(g.Passed)
	s.Contains(g.Violations, "the run read tree tree-1 and the tree is now tree-2")
}

func (s *GradeSuite) TestARunOfAnotherCheckIsAViolation() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))

	g := s.grade(s.report(checkJSON("lint", ".")))

	s.False(g.Verified)
	s.Contains(g.Violations, "the run is of `lint .` and this postcondition's check is `test .`")
}

func (s *GradeSuite) TestARefForAPostconditionTheJobDoesNotDeclareIsRefused() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))

	_, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{"nope": "run-1"})

	refused, ok := errors.AsType[*PostconditionError](err)
	s.Require().True(ok, err)
	s.Equal(PostconditionError{Job: "j1", Postcondition: "nope"}, *refused)
}

func (s *GradeSuite) TestAJobWithNoCheckPostconditionNeverVerifies() {
	s.declare(Postcondition{ID: "docs", Kind: PostconditionKindPaths, Expect: ExpectChanged, Paths: []string{"docs"}})

	g, err := s.store.Grade(s.T().Context(), "j1", nil)

	s.Require().NoError(err)
	s.False(g.Verified, "a grade over no check postcondition is no pass")
	s.Empty(g.Postconditions)
}

func (s *GradeSuite) TestAnUnknownJobIsRefused() {
	_, err := s.store.Grade(s.T().Context(), "nope", nil)

	s.ErrorIs(err, ErrUnknownJob)
}

func (s *GradeSuite) TestGradingWithNoTreeIsRefused() {
	st, err := Open(s.T().Context(), Config{Dir: s.dir}, writable()...)
	s.Require().NoError(err)

	_, err = st.Grade(s.T().Context(), "j1", nil)

	refused, ok := errors.AsType[*GradingUnconfiguredError](err)
	s.Require().True(ok, err)
	s.Equal(GradingUnconfiguredError{Op: "Grade", Option: "WithTree"}, *refused)
}

func (s *GradeSuite) TestRunsAndGradesAreLinesOfGradesJSONL() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))
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
	s.declare(checkPostconditionAccepting("tests", BasisReported))
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, gradesFileName),
		[]byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Verdict"}`+"\n"), 0o600))

	_, err := s.store.Grade(s.T().Context(), "j1", nil)

	s.ErrorIs(err, codec.ErrLineUndecodable)
}

// An older build's camelCase run line carries no api_version, so it is refused rather
// than read as a run of nothing.
func (s *GradeSuite) TestAnOlderBuildsRunLineIsRefused() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, gradesFileName),
		[]byte(`{"apiVersion":"safetyscissors.job/v1alpha1","kind":"Run","ref":"run-1","job":"j1","goal":"tests","basis":"Reported"}`+"\n"), 0o600))

	_, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{"tests": "run-1"})

	s.ErrorIs(err, codec.ErrLineUndecodable)
}

// grades.jsonl has no migration, so a line written before goal became postcondition is
// refused rather than read as a run of no postcondition.
func (s *GradeSuite) TestARunLineSpellingGoalIsRefused() {
	s.declare(checkPostconditionAccepting("tests", BasisReported))
	s.Require().NoError(os.WriteFile(filepath.Join(s.dir, gradesFileName),
		[]byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Run","ref":"run-1","job":"j1","goal":"tests","basis":"reported",`+
			`"check":{"target":"test","project":"."},"tree":"t","exit":0,"started":1,"ended":2}`+"\n"), 0o600))

	_, err := s.store.Grade(s.T().Context(), "j1", map[string]RunRef{"tests": "run-1"})

	s.ErrorIs(err, codec.ErrLineUndecodable)
	s.ErrorContains(err, "spells goal")
}

func (s *GradeSuite) TestAGradeLineSpellingGoalsIsRefused() {
	var e gradesEntry
	err := codec.Unmarshal([]byte(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Grade","job":"j1","graded":1,"verified":false,"goals":[]}`), &e)

	s.ErrorContains(err, "spells goal")
}

func (s *GradeSuite) TestAMemberANewerWriterStoredInAGradeOrARunIsRefused() {
	for _, line := range []string{
		`{"api_version":"safetyscissors.job/v1alpha1","kind":"Grade","job":"j1","graded":1,"verified":false,"postconditions":[],"later":1}`,
		`{"api_version":"safetyscissors.job/v1alpha1","kind":"Grade","job":"j1","graded":1,"verified":false,` +
			`"postconditions":[{"postcondition":"tests","passed":true,"verified":false,"later":1}]}`,
		`{"api_version":"safetyscissors.job/v1alpha1","kind":"Run","ref":"run-1","job":"j1","postcondition":"tests","basis":"reported",` +
			`"check":{"target":"test","project":"."},"tree":"t","exit":0,"started":1,"ended":2,"later":1}`,
	} {
		var e gradesEntry
		err := codec.Unmarshal([]byte(line), &e)

		refused, ok := errors.AsType[*UnknownMemberError](err)
		s.Require().True(ok, "%s: %v", line, err)
		s.Equal([]string{"later"}, refused.Members, line)
	}
}

func (s *GradeSuite) TestARunsAndAGradesLinesArePinned() {
	rec := recordCheckRun(run{job: "j1", postcondition: "tests", basis: BasisWitnessed, check: checkJSON("test", "."),
		argv: []string{"magus", "run"}, tree: "t1", exit: 1, started: time.UnixMilli(5), ended: time.UnixMilli(6), moved: true})
	rec.Ref = "run-1"
	grade := recordGrade(Grade{Job: "j1", Postconditions: []PostconditionGrade{{Postcondition: "tests", Ref: "run-1", Basis: BasisAttested, Passed: true}}}, time.UnixMilli(7))

	runLine, err := codec.Marshal(gradesEntry{run: &rec})
	s.Require().NoError(err)
	gradeLine, err := codec.Marshal(gradesEntry{grade: grade})
	s.Require().NoError(err)

	s.Equal(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Run","ref":"run-1","job":"j1","postcondition":"tests","basis":"witnessed",`+
		`"check":{"target":"test","project":"."},"argv":["magus","run"],"tree":"t1","exit":1,"started":5,"ended":6,"moved":true}`, string(runLine))
	s.Equal(`{"api_version":"safetyscissors.job/v1alpha1","kind":"Grade","job":"j1","graded":7,"verified":false,`+
		`"postconditions":[{"postcondition":"tests","ref":"run-1","basis":"attested","passed":true,"verified":false}]}`, string(gradeLine))
}
