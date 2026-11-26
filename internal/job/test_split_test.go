package job

import (
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/suite"
)

// splitFiles is a package with code and two existing tests under the golang diff driver,
// and a second package with no tests.
var splitFiles = map[string]string{
	".gitattributes": "*.go diff=golang\n",
	"pkg/a.go":       "package pkg\n\nfunc A() int {\n\treturn 1\n}\n",
	"pkg/a_test.go":  "package pkg\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != 1 {\n\t\tt.Fatal(A())\n\t}\n}\n\nfunc TestB(t *testing.T) {\n}\n",
	"web/x.go":       "package web\n",
}

type existingTestSuite struct {
	suite.Suite
	root  string
	store *Store
}

func TestExistingTests(t *testing.T) { suite.Run(t, new(existingTestSuite)) }

func (s *existingTestSuite) SetupTest() {
	s.root = testkit.NewRepository(s.T(), splitFiles).Root()
	s.store = NewStore(tempLocation(s.T(), s.root))
}

func (s *existingTestSuite) problems(write, deny []string) types.JobProblems {
	return existingTestProblems(s.T().Context(), s.store, job.Job{ID: "impl", Paths: job.Paths{Write: write, Deny: deny}})
}

func (s *existingTestSuite) TestAGlobOverCodeAndItsTestsSplitsThem() {
	got := s.problems([]string{"pkg/*.go"}, nil)

	s.Equal(types.JobProblems{{
		Code:    types.WritePathExistingTests,
		Field:   "paths.write",
		Subject: "pkg",
		Message: "impl writes the code of pkg (pkg/a.go) and tests that exist at its base (pkg/a_test.go#TestA, pkg/a_test.go#TestB):" +
			" its worker could weaken the test that grades its own change",
		Hint: `split the existing tests into their own row: ` +
			`{"id":"impl","paths":{"write":["pkg/*.go"],"deny":["pkg/a_test.go#TestA","pkg/a_test.go#TestB"]}}, then ` +
			`{"id":"impl-tests","depends_on":["impl"],"paths":{"write":["pkg/a_test.go#TestA","pkg/a_test.go#TestB"]},` +
			`"agent":{"worker":{"type":"new","reason":"` + testWorkerReason + `"}}}; ` +
			`or keep them in one row with the deviation {"code":"MGS3060","subject":"pkg","reason":"<why one worker edits this code and its tests>"}`,
	}}, got)
}

func (s *existingTestSuite) TestTheSplitRowsRaiseNothing() {
	s.Empty(s.problems([]string{"pkg/*.go"}, []string{"pkg/a_test.go#TestA", "pkg/a_test.go#TestB"}), "the implementation row")
	s.Empty(s.problems([]string{"pkg/a_test.go#TestA", "pkg/a_test.go#TestB"}, nil), "the test row")
}

func (s *existingTestSuite) TestADenyNarrowsWhatIsSplit() {
	got := s.problems([]string{"pkg/a.go", "pkg/a_test.go"}, []string{"pkg/a_test.go#TestB"})

	s.Require().Len(got, 1)
	s.Contains(got[0].Message, "(pkg/a_test.go#TestA)")
}

func (s *existingTestSuite) TestNewTestsStayWithTheCode() {
	s.Empty(s.problems([]string{"pkg/a.go", "pkg/new_test.go"}, nil), "a new test file")
	s.Empty(s.problems([]string{"pkg/a.go", "pkg/a_test.go#TestNew"}, nil), "a new declaration in an existing test file")
}

func (s *existingTestSuite) TestAClaimOnOneExistingTestIsSplit() {
	got := s.problems([]string{"pkg/a.go", "pkg/a_test.go#TestA"}, nil)

	s.Require().Len(got, 1)
	s.Contains(got[0].Message, "(pkg/a_test.go#TestA)")
}

func (s *existingTestSuite) TestTestsBesideNoWrittenCodeAreNotOne() {
	s.Empty(s.problems([]string{"web/x.go", "pkg/a_test.go"}, nil))
}

func (s *existingTestSuite) TestAReadOnlyOrIntegrationRowIsNotJudged() {
	ctx := s.T().Context()
	s.Empty(existingTestProblems(ctx, s.store, job.Job{ID: "scout", ReadOnly: true, Paths: job.Paths{Write: []string{"pkg/*.go"}}}))
	s.Empty(existingTestProblems(ctx, s.store, job.Job{ID: "merge", Integrates: []string{"a"}, Paths: job.Paths{Write: []string{"pkg/*.go"}}}))
}

func (s *existingTestSuite) TestAnUnplacedTestFileIsHeldWholeAndSaysSo() {
	files := map[string]string{}
	for k, v := range splitFiles {
		if k != ".gitattributes" {
			files[k] = v
		}
	}
	files[".gitattributes"] = "*.go -diff\n"
	s.root = testkit.NewRepository(s.T(), files).Root()
	s.store = NewStore(tempLocation(s.T(), s.root))

	got := s.problems([]string{"pkg/*.go"}, nil)

	s.Require().Len(got, 1)
	s.Contains(got[0].Message, "(pkg/a_test.go)")
	s.Contains(got[0].Message, "no diff driver placed the declarations of pkg/a_test.go, so the split holds it whole")
	s.Contains(got[0].Hint, `"deny":["pkg/a_test.go"]`)
}

func (s *existingTestSuite) TestAForkIsRefusedAndWritesNoRow() {
	_, err := ForkMerge(s.T().Context(), s.store, "impl", func(u *job.Job) {
		u.Check, u.State, u.Paths.Write = forkCheck(), job.StateDeclared, []string{"pkg/a.go", "pkg/a_test.go"}
	}, config.Jobs{}, nil)

	s.Require().ErrorIs(err, types.WritePathExistingTests)
	rows, err := s.store.List(s.T().Context())
	s.Require().NoError(err)
	s.Empty(rows)
}
