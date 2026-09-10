package main

import (
	"reflect"
	"testing"
)

// Each sample is built from the lines the upstream parser's own regexes and
// prefixes look for, so a port that drifts from them fails here before it
// misgrades a real log.

func TestSwegradeParsePytest(t *testing.T) {
	log := "collected 4 items\n" +
		"PASSED tests/test_a.py::test_one\n" +
		"FAILED tests/test_a.py::test_two - AssertionError: 1 != 2\n" +
		"SKIPPED [1] tests/test_a.py:12: no backend\n" +
		"ERROR tests/test_b.py::test_three\n" +
		"XFAIL tests/test_b.py::test_four\n" +
		"PASSED\n" +
		"=== 1 passed ===\n"
	want := statusMap{
		"tests/test_a.py::test_one":   statusPassed,
		"tests/test_a.py::test_two":   statusFailed,
		"tests/test_b.py::test_three": statusError,
		"tests/test_b.py::test_four":  statusXfail,
	}
	if got := parsePytest(log); !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePytest = %v, want %v", got, want)
	}
}

func TestSwegradeParsePytestOptions(t *testing.T) {
	log := "PASSED tests/test_x.py::test_url[/tmp/pytest-0/dir/file.txt]\n" +
		"PASSED tests/test_x.py::test_url[//server/share]\n" +
		"PASSED tests/test_x.py::test_url[/a/b/*]\n" +
		"FAILED tests/test_x.py::test_plain - boom\n" +
		"SKIPPED [2] tests/test_x.py:3: reason\n"
	want := statusMap{
		"tests/test_x.py::test_url[/file.txt]":      statusPassed,
		"tests/test_x.py::test_url[//server/share]": statusPassed,
		"tests/test_x.py::test_url[/a/b/*]":         statusPassed,
		"tests/test_x.py::test_plain":               statusFailed,
	}
	if got := parsePytestOptions(log); !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePytestOptions = %v, want %v", got, want)
	}
}

func TestSwegradeParsePytestV2(t *testing.T) {
	log := "\x1b[32mPASSED\x1b[0m astropy/io/tests/test_a.py::test_one\n" +
		"FAILED astropy/io/tests/test_a.py::test_two[a - b] - assert 1 == 2\n" +
		"astropy/io/tests/test_a.py::test_three PASSED\n" +
		"SKIPPED [3] astropy/conftest.py:9: optional\n" +
		"XFAIL astropy/io/tests/test_a.py::test_four reason\n"
	want := statusMap{
		"astropy/io/tests/test_a.py::test_one":         statusPassed,
		"astropy/io/tests/test_a.py::test_two[a":       statusFailed,
		"astropy/io/tests/test_a.py::test_three":       statusPassed,
		"astropy/io/tests/test_a.py::test_four reason": statusXfail,
	}
	if got := parsePytestV2(log); !reflect.DeepEqual(got, want) {
		t.Fatalf("parsePytestV2 = %v, want %v", got, want)
	}
}

func TestSwegradeParseDjango(t *testing.T) {
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
	want := statusMap{
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
	}
	if got := parseDjango(log); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseDjango = %v, want %v", got, want)
	}
}

func TestSwegradeParseSympy(t *testing.T) {
	log := "test_add ok\n" +
		"test_sub F\n" +
		"test_mul E\n" +
		"________ sympy/core/tests/test_arit.py:test_div ________\n" +
		"test_pow ok [1.2s]\n"
	want := statusMap{
		"test_add":                               statusPassed,
		"test_sub":                               statusFailed,
		"test_mul":                               statusError,
		"sympy/core/tests/test_arit.py:test_div": statusFailed,
	}
	if got := parseSympy(log); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSympy = %v, want %v", got, want)
	}
}

func TestSwegradeParseMatplotlib(t *testing.T) {
	log := "PASSED lib/tests/test_widgets.py::test_click[MouseButton.LEFT]\n" +
		"FAILED lib/tests/test_widgets.py::test_click[MouseButton.RIGHT] - assert\n" +
		"SKIPPED [1] lib/conftest.py:1: no display\n"
	want := statusMap{
		"lib/tests/test_widgets.py::test_click[1]": statusPassed,
		"lib/tests/test_widgets.py::test_click[3]": statusFailed,
	}
	if got := parseMatplotlib(log); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMatplotlib = %v, want %v", got, want)
	}
}

func TestSwegradeParseSeaborn(t *testing.T) {
	log := "FAILED tests/test_a.py::test_one - assert\n" +
		"tests/test_a.py::test_two PASSED [ 50%]\n" +
		"PASSED tests/test_a.py::test_three\n" +
		"tests/test_a.py::test_four SKIPPED\n"
	want := statusMap{
		"tests/test_a.py::test_one":   statusFailed,
		"tests/test_a.py::test_two":   statusPassed,
		"tests/test_a.py::test_three": statusPassed,
	}
	if got := parseSeaborn(log); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseSeaborn = %v, want %v", got, want)
	}
}

func TestSwegradeParserTableCoversVerified(t *testing.T) {
	for _, name := range []string{
		"parse_log_django", "parse_log_sympy", "parse_log_sphinx", "parse_log_matplotlib",
		"parse_log_scikit", "parse_log_astropy", "parse_log_xarray", "parse_log_pytest",
		"parse_log_pylint", "parse_log_requests", "parse_log_seaborn", "parse_log_flask",
	} {
		if parsers[name] == nil {
			t.Errorf("no parser for %s", name)
		}
	}
}
