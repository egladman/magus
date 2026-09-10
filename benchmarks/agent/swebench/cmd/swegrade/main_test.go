package main

import (
	"encoding/json"
	"testing"
)

func wrapped(body string) string {
	return "setup\n" + markerStart + "\n" + body + markerEnd + "\ncleanup\n"
}

func sampleRow(t *testing.T) instance {
	t.Helper()
	var row instance
	err := json.Unmarshal([]byte(`{"instance_id":"x__x-1","log_parser":"parse_log_pytest",`+
		`"FAIL_TO_PASS":"[\"tests/t.py::new\"]",`+
		`"PASS_TO_PASS":["tests/t.py::old", "tests/t.py::param[a"]}`), &row)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func TestSwegradeResolved(t *testing.T) {
	row := sampleRow(t)
	v := grade(row, wrapped("PASSED tests/t.py::new\nPASSED tests/t.py::old\nXFAIL tests/t.py::param[a-b]\n"))
	if !v.Resolved || v.Error != "" {
		t.Fatalf("want resolved, got %+v", v)
	}
	if v.PassToPass["tests/t.py::param[a"] != statusXfail {
		t.Fatalf("truncated id did not resolve by prefix: %+v", v)
	}
}

func TestSwegradeUnresolved(t *testing.T) {
	row := sampleRow(t)
	cases := map[string]string{
		"f2p failed":        "FAILED tests/t.py::new - boom\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n",
		"f2p skipped":       "SKIPPED tests/t.py::new\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n",
		"f2p missing":       "PASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n",
		"p2p regressed":     "PASSED tests/t.py::new\nERROR tests/t.py::old\nPASSED tests/t.py::param[a-b]\n",
		"p2p missing":       "PASSED tests/t.py::new\nPASSED tests/t.py::param[a-b]\n",
		"prefix disagrees":  "PASSED tests/t.py::new\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\nFAILED tests/t.py::param[a-c]\n",
		"exit code no fail": "PASSED tests/t.py::new\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n" + markerEnd + "\n" + markerExitCode + ": 2\n",
	}
	for name, body := range cases {
		if v := grade(row, wrapped(body)); v.Resolved {
			t.Errorf("%s: want unresolved, got %+v", name, v)
		}
	}
}

func TestSwegradeSkippedMaintains(t *testing.T) {
	row := sampleRow(t)
	v := grade(row, wrapped("PASSED tests/t.py::new\nSKIPPED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n"))
	if !v.Resolved {
		t.Fatalf("a skipped PASS_TO_PASS test is not a regression: %+v", v)
	}
}

func TestSwegradeRejectedLogs(t *testing.T) {
	row := sampleRow(t)
	all := "PASSED tests/t.py::new\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n"
	for name, log := range map[string]string{
		"no markers":   all,
		"patch failed": ">>>>> Patch Apply Failed\n" + wrapped(all),
		"timed out":    wrapped(all) + ">>>>> Tests Timed Out\n",
	} {
		v := grade(row, log)
		if v.Resolved || v.Error == "" {
			t.Errorf("%s: want a rejected log, got %+v", name, v)
		}
		if v.FailToPass["tests/t.py::new"] != statusMissing {
			t.Errorf("%s: a rejected log must report every test MISSING, got %+v", name, v)
		}
	}
}

func TestSwegradeFallsBackToWholeLog(t *testing.T) {
	row := sampleRow(t)
	log := "PASSED tests/t.py::new\nPASSED tests/t.py::old\nPASSED tests/t.py::param[a-b]\n" +
		markerStart + "\n" + markerEnd + "\n"
	if v := grade(row, log); !v.Resolved {
		t.Fatalf("results outside the markers must still count: %+v", v)
	}
}
