package benchtasks

import (
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type gotestSuite struct {
	suite.Suite
}

func TestGotestSuite(t *testing.T) {
	suite.Run(t, new(gotestSuite))
}

const suiteFile = `package p

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type KeySuite struct{ suite.Suite }

func TestKeySuite(t *testing.T) { suite.Run(t, new(KeySuite)) }

func (s *KeySuite) TestRace() { s.True(true) }

func (s *KeySuite) helper() {}

func TestPlain(t *testing.T) {}
`

func (s *gotestSuite) TestTestFuncsNamesSuiteMethodsUnderTheirRunner() {
	funcs, err := testFuncs(map[string][]byte{"p/key_test.go": []byte(suiteFile)})
	s.Require().NoError(err)
	s.Equal([]string{"TestKeySuite", "TestKeySuite/TestRace", "TestPlain"}, slices.Sorted(maps.Keys(funcs)))
}

func (s *gotestSuite) TestTestFuncsFollowsARunnerInAnotherFile() {
	funcs, err := testFuncs(map[string][]byte{
		"p/a_test.go": []byte("package p\nimport \"testing\"\nfunc TestRunner(t *testing.T) { suite.Run(t, &Other{}) }\n"),
		"p/b_test.go": []byte("package p\nfunc (o *Other) TestThing() {}\n"),
	})
	s.Require().NoError(err)
	s.Contains(funcs, "TestRunner/TestThing")
}

func (s *gotestSuite) TestChangedTestsFindsNewAndEditedTests() {
	parent := map[string]string{"TestA": "a", "TestB": "b"}
	fix := map[string]string{"TestA": "a", "TestB": "b2", "TestS/TestC": "c"}
	s.Equal([]string{"TestB", "TestS/TestC"}, changedTests(parent, fix))
}

func (s *gotestSuite) TestRunPatternSelectsTopLevelTestsOnce() {
	s.Equal("^(TestB|TestS)$", runPattern([]string{"TestS/TestC", "TestB", "TestS/TestD"}))
}

const testJSON = `# p [p.test]
p/x_test.go:3:2: undefined: Thing
{"Action":"run","Package":"p","Test":"TestS"}
{"Action":"run","Package":"p","Test":"TestS/TestC"}
{"Action":"fail","Package":"p","Test":"TestS/TestC"}
{"Action":"pass","Package":"p","Test":"TestB"}
{"Action":"fail","Package":"p","Test":"TestS"}
{"Action":"fail","Package":"p"}
{"Action":"pass","Package":"q"}
`

func (s *gotestSuite) TestParseTestJSONKeepsOutcomesAndFailedPackages() {
	run, err := parseTestJSON(strings.NewReader(testJSON))
	s.Require().NoError(err)
	s.Equal(testRun{outcomes: map[string]string{"TestS": "fail", "TestS/TestC": "fail", "TestB": "pass"}, failedPackages: []string{"p"}}, run)
}

func (s *gotestSuite) TestFailToPassWantsAFailOrAbsenceAtTheParent() {
	parent := testRun{outcomes: map[string]string{"TestA": "fail", "TestB": "pass"}}
	fix := testRun{outcomes: map[string]string{"TestA": "pass", "TestB": "pass", "TestC": "pass", "TestD": "fail"}}
	s.Equal([]string{"TestA", "TestC"}, failToPass([]string{"TestA", "TestB", "TestC", "TestD"}, parent, fix))
}
