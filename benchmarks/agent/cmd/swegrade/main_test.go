package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/json"
)

type swegradeSuite struct {
	suite.Suite
	// What the go toolchain printed for testdata/gotest under -v and -json;
	// SetupSuite runs it.
	goTestVerbose string
	goTestJSON    string
}

func TestSwegrade(t *testing.T) {
	suite.Run(t, new(swegradeSuite))
}

func wrapped(body string) string {
	return fmt.Sprintf(`setup
%s
%s%s
cleanup
`, markerStart, body, markerEnd)
}

func (s *swegradeSuite) row(raw string) instance {
	var row instance
	s.Require().NoError(json.Unmarshal([]byte(raw), &row))
	return row
}

func (s *swegradeSuite) pytestRow() instance {
	return s.row(`{"instance_id":"x__x-1","log_parser":"parse_log_pytest",` +
		`"FAIL_TO_PASS":"[\"tests/t.py::new\"]",` +
		`"PASS_TO_PASS":["tests/t.py::old", "tests/t.py::param[a"]}`)
}

func (s *swegradeSuite) requireUnresolved(body string) {
	v := grade(s.pytestRow(), wrapped(body))
	s.False(v.Resolved, "%+v", v)
}

func (s *swegradeSuite) requireRejected(log string) {
	v := grade(s.pytestRow(), log)
	s.False(v.Resolved, "%+v", v)
	s.NotEmpty(v.Error, "%+v", v)
	s.Equal(statusMissing, v.FailToPass["tests/t.py::new"], "a rejected log reports every test MISSING")
}

func (s *swegradeSuite) TestResolved() {
	v := grade(s.pytestRow(), wrapped("PASSED tests/t.py::new\nPASSED tests/t.py::old\nXFAIL tests/t.py::param[a-b]\n"))
	s.True(v.Resolved, "%+v", v)
	s.Empty(v.Error)
	s.Equal(statusXfail, v.PassToPass["tests/t.py::param[a"], "a truncated id resolves by prefix")
}

func (s *swegradeSuite) TestUnresolvedWhenFailToPassFails() {
	s.requireUnresolved("FAILED tests/t.py::new - boom\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n")
}

func (s *swegradeSuite) TestUnresolvedWhenFailToPassSkips() {
	s.requireUnresolved("SKIPPED tests/t.py::new\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n")
}

func (s *swegradeSuite) TestUnresolvedWhenFailToPassMissing() {
	s.requireUnresolved("PASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n")
}

func (s *swegradeSuite) TestUnresolvedWhenPassToPassRegresses() {
	s.requireUnresolved("PASSED tests/t.py::new\nERROR tests/t.py::old\nPASSED tests/t.py::param[a-b]\n")
}

func (s *swegradeSuite) TestUnresolvedWhenPassToPassMissing() {
	s.requireUnresolved("PASSED tests/t.py::new\nPASSED tests/t.py::param[a-b]\n")
}

func (s *swegradeSuite) TestUnresolvedWhenPrefixCandidatesDisagree() {
	s.requireUnresolved("PASSED tests/t.py::new\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\nFAILED tests/t.py::param[a-c]\n")
}

// Upstream treats SKIPPED and FAILED candidates as agreeing (neither passes)
// and reports the lowest key, FAILED here, so the pass-to-pass test regresses.
func (s *swegradeSuite) TestUnresolvedWhenPrefixSkipMeetsFail() {
	s.requireUnresolved("PASSED tests/t.py::new\nPASSED tests/t.py::old\nFAILED tests/t.py::param[a-b]\nSKIPPED tests/t.py::param[a-c]\n")
}

func (s *swegradeSuite) TestUnresolvedWhenExitCodeHasNoFailure() {
	s.requireUnresolved("PASSED tests/t.py::new\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n" +
		markerEnd + "\n" + markerExitCode + ": 2\n")
}

func (s *swegradeSuite) TestSkippedPassToPassMaintains() {
	v := grade(s.pytestRow(), wrapped("PASSED tests/t.py::new\nSKIPPED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n"))
	s.True(v.Resolved, "a skipped PASS_TO_PASS test is not a regression: %+v", v)
}

const allPytestPassed = "PASSED tests/t.py::new\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n"

func (s *swegradeSuite) TestRejectsLogWithoutMarkers() {
	s.requireRejected(allPytestPassed)
}

func (s *swegradeSuite) TestRejectsFailedPatchApply() {
	s.requireRejected(">>>>> Patch Apply Failed\n" + wrapped(allPytestPassed))
}

func (s *swegradeSuite) TestRejectsTimedOutTests() {
	s.requireRejected(wrapped(allPytestPassed) + ">>>>> Tests Timed Out\n")
}

func (s *swegradeSuite) TestFallsBackToWholeLog() {
	v := grade(s.pytestRow(), allPytestPassed+markerStart+"\n"+markerEnd+"\n")
	s.True(v.Resolved, "results outside the markers still count: %+v", v)
}

func (s *swegradeSuite) goTestRow(failToPass, passToPass string) instance {
	return s.row(fmt.Sprintf(`{"instance_id":"x__x-2","log_parser":"parse_log_gotest","FAIL_TO_PASS":%s,"PASS_TO_PASS":%s}`,
		failToPass, passToPass))
}

func (s *swegradeSuite) TestGoTestVerboseResolved() {
	row := s.goTestRow(`["TestPass","TestParent/ok_case"]`, `["TestSkip","TestNested/outer/inner"]`)
	v := grade(row, wrapped(s.goTestVerbose))
	s.True(v.Resolved, "%+v", v)
}

func (s *swegradeSuite) TestGoTestJSONResolved() {
	row := s.goTestRow(`["TestPass","TestParent/ok_case"]`, `["TestSkip","TestNested/outer/inner"]`)
	v := grade(row, wrapped(s.goTestJSON))
	s.True(v.Resolved, "%+v", v)
}

func (s *swegradeSuite) TestGoTestFailedSubtestUnresolved() {
	v := grade(s.goTestRow(`["TestParent/bad"]`, `["TestPass"]`), wrapped(s.goTestVerbose))
	s.False(v.Resolved, "%+v", v)
	s.Equal(statusFailed, v.FailToPass["TestParent/bad"])
}

func (s *swegradeSuite) TestGoTestParentOfFailedSubtestRegresses() {
	v := grade(s.goTestRow(`["TestPass"]`, `["TestParent"]`), wrapped(s.goTestJSON))
	s.False(v.Resolved, "%+v", v)
	s.Equal(statusFailed, v.PassToPass["TestParent"])
}
