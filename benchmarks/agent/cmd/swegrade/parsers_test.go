package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// SetupSuite produces the go test logs from the real toolchain rather than
// from pasted text, so a change in go test's output format fails here first.
// The fixture is .txt so the workspace's own Go linters never read its plain
// test functions; the go directive stays old enough for GOTOOLCHAIN=local.
func (s *swegradeSuite) SetupSuite() {
	src, err := os.ReadFile(filepath.Join("testdata", "gotest", "fixture_test.go.txt"))
	s.Require().NoError(err)
	dir := s.T().TempDir()
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module fixture\n\ngo 1.22\n"), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(dir, "fixture_test.go"), src, 0o644))
	s.goTestVerbose = s.runFixture(dir, "-v")
	s.goTestJSON = s.runFixture(dir, "-json")
}

// runFixture runs go test over the fixture module; the fixture has failing
// tests, so only exit status 1 is the expected outcome.
func (s *swegradeSuite) runFixture(dir, format string) string {
	cmd := exec.Command("go", "test", "-count=1", format, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=", "GOTOOLCHAIN=local", "GOPROXY=off")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	s.Require().True(errors.As(err, &exit), "go test %s: %v\n%s", format, err, out)
	s.Require().Equal(1, exit.ExitCode(), "go test %s:\n%s", format, out)
	return string(out)
}

// Each sample is built from the lines the upstream parser's own regexes and
// prefixes look for, so a port that drifts from them fails here before it
// misgrades a real log.

func (s *swegradeSuite) TestParsePytest() {
	log := "collected 4 items\n" +
		"PASSED tests/test_a.py::test_one\n" +
		"FAILED tests/test_a.py::test_two - AssertionError: 1 != 2\n" +
		"SKIPPED [1] tests/test_a.py:12: no backend\n" +
		"ERROR tests/test_b.py::test_three\n" +
		"XFAIL tests/test_b.py::test_four\n" +
		"PASSED\n" +
		"=== 1 passed ===\n"
	s.Equal(statusMap{
		"tests/test_a.py::test_one":   statusPassed,
		"tests/test_a.py::test_two":   statusFailed,
		"tests/test_b.py::test_three": statusError,
		"tests/test_b.py::test_four":  statusXfail,
	}, parsePytest(log))
}

func (s *swegradeSuite) TestParsePytestOptions() {
	log := "PASSED tests/test_x.py::test_url[/tmp/pytest-0/dir/file.txt]\n" +
		"PASSED tests/test_x.py::test_url[//server/share]\n" +
		"PASSED tests/test_x.py::test_url[/a/b/*]\n" +
		"FAILED tests/test_x.py::test_plain - boom\n" +
		"SKIPPED [2] tests/test_x.py:3: reason\n"
	s.Equal(statusMap{
		"tests/test_x.py::test_url[/file.txt]":      statusPassed,
		"tests/test_x.py::test_url[//server/share]": statusPassed,
		"tests/test_x.py::test_url[/a/b/*]":         statusPassed,
		"tests/test_x.py::test_plain":               statusFailed,
	}, parsePytestOptions(log))
}

func (s *swegradeSuite) TestParsePytestV2() {
	log := "\x1b[32mPASSED\x1b[0m astropy/io/tests/test_a.py::test_one\n" +
		"FAILED astropy/io/tests/test_a.py::test_two[a - b] - assert 1 == 2\n" +
		"astropy/io/tests/test_a.py::test_three PASSED\n" +
		"SKIPPED [3] astropy/conftest.py:9: optional\n" +
		"XFAIL astropy/io/tests/test_a.py::test_four reason\n"
	s.Equal(statusMap{
		"astropy/io/tests/test_a.py::test_one":         statusPassed,
		"astropy/io/tests/test_a.py::test_two[a":       statusFailed,
		"astropy/io/tests/test_a.py::test_three":       statusPassed,
		"astropy/io/tests/test_a.py::test_four reason": statusXfail,
	}, parsePytestV2(log))
}

func (s *swegradeSuite) TestParseDjango() {
	log := "test_one (app.tests.Case) ... ok\n" +
		"test_two (app.tests.Case) ... skipped 'no db'\n" +
		"test_three (app.tests.Case) ... FAIL\n" +
		"test_four (app.tests.Case) ... ERROR\n" +
		"FAIL: test_five (app.tests.Case)\n" +
		"ERROR: test_six (app.tests.Case)\n" +
		"test_seven (app.tests.Case) ... some output\n" +
		"ok\n" +
		"test_eight (app.tests.Case) ... System check identified no issues (0 silenced)\n" +
		"ok\n" +
		"test_nine (app.tests.Case) ... Testing against Django installed in '/x'\nwith up to 4 processes (0 silenced).\nok\n" +
		"test_ten (app.tests.Case) ... Internal Server Error: /admin/\nok\n" +
		"--version is equivalent to version ... ok\n"
	s.Equal(statusMap{
		"test_one (app.tests.Case)":          statusPassed,
		"test_two (app.tests.Case)":          statusSkipped,
		"test_three (app.tests.Case)":        statusFailed,
		"test_four (app.tests.Case)":         statusError,
		"test_five":                          statusFailed,
		"test_six":                           statusError,
		"test_seven (app.tests.Case)":        statusPassed,
		"test_eight (app.tests.Case)":        statusPassed,
		"test_nine (app.tests.Case)":         statusPassed,
		"test_ten (app.tests.Case)":          statusPassed,
		"--version is equivalent to version": statusPassed,
	}, parseDjango(log))
}

func (s *swegradeSuite) TestParseSympy() {
	log := "test_add ok\n" +
		"test_sub F\n" +
		"test_mul E\n" +
		"________ sympy/core/tests/test_arit.py:test_div ________\n" +
		"test_pow ok [1.2s]\n"
	s.Equal(statusMap{
		"test_add":                               statusPassed,
		"test_sub":                               statusFailed,
		"test_mul":                               statusError,
		"sympy/core/tests/test_arit.py:test_div": statusFailed,
	}, parseSympy(log))
}

func (s *swegradeSuite) TestParseMatplotlib() {
	log := "PASSED lib/tests/test_widgets.py::test_click[MouseButton.LEFT]\n" +
		"FAILED lib/tests/test_widgets.py::test_click[MouseButton.RIGHT] - assert\n" +
		"SKIPPED [1] lib/conftest.py:1: no display\n"
	s.Equal(statusMap{
		"lib/tests/test_widgets.py::test_click[1]": statusPassed,
		"lib/tests/test_widgets.py::test_click[3]": statusFailed,
	}, parseMatplotlib(log))
}

func (s *swegradeSuite) TestParseSeaborn() {
	log := "FAILED tests/test_a.py::test_one - assert\n" +
		"tests/test_a.py::test_two PASSED [ 50%]\n" +
		"PASSED tests/test_a.py::test_three\n" +
		"tests/test_a.py::test_four SKIPPED\n"
	s.Equal(statusMap{
		"tests/test_a.py::test_one":   statusFailed,
		"tests/test_a.py::test_two":   statusPassed,
		"tests/test_a.py::test_three": statusPassed,
	}, parseSeaborn(log))
}

// TestUnterminatedOutput's result line starts with its own output under -v, so
// upstream's anchored pattern misses it and grades it MISSING; -json does not.
func (s *swegradeSuite) TestParseGoTestVerbose() {
	s.Equal(statusMap{
		"TestPass":               statusPassed,
		"TestFail":               statusFailed,
		"TestSkip":               statusSkipped,
		"TestParent":             statusFailed,
		"TestParent/ok_case":     statusPassed,
		"TestParent/bad":         statusFailed,
		"TestParent/skipped":     statusSkipped,
		"TestNested":             statusPassed,
		"TestNested/outer":       statusPassed,
		"TestNested/outer/inner": statusPassed,
	}, parseGoTest(s.goTestVerbose))
}

func (s *swegradeSuite) TestParseGoTestJSON() {
	s.Equal(statusMap{
		"TestPass":               statusPassed,
		"TestFail":               statusFailed,
		"TestSkip":               statusSkipped,
		"TestParent":             statusFailed,
		"TestParent/ok_case":     statusPassed,
		"TestParent/bad":         statusFailed,
		"TestParent/skipped":     statusSkipped,
		"TestNested":             statusPassed,
		"TestNested/outer":       statusPassed,
		"TestNested/outer/inner": statusPassed,
		"TestUnterminatedOutput": statusPassed,
	}, parseGoTest(s.goTestJSON))
}

func (s *swegradeSuite) TestParseGoTestKeepsDurationParenthesesOffTheName() {
	s.Equal(statusMap{"TestA/case_(x)": statusPassed},
		parseGoTest("    --- PASS: TestA/case_(x) (0.01s)\n--- PASS: TestB\n"))
}

func (s *swegradeSuite) TestParserTableCoversDatasets() {
	for _, name := range []string{
		"parse_log_django", "parse_log_sympy", "parse_log_sphinx", "parse_log_matplotlib",
		"parse_log_scikit", "parse_log_astropy", "parse_log_xarray", "parse_log_pytest",
		"parse_log_pylint", "parse_log_requests", "parse_log_seaborn", "parse_log_flask",
		"parse_log_gotest",
	} {
		s.NotNil(parsers[name], "no parser for %s", name)
	}
}
