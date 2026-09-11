// Command swegrade turns a SWE-bench eval log into a verdict.
//
//	swegrade --instance <row.json> < eval.log
//
// The row is one line of the dataset (verified.jsonl or pilot.jsonl). stdout is
// a JSON object with resolved, fail_to_pass and pass_to_pass, the last two
// mapping every expected test to the status the log gave it, or MISSING. The
// exit status is 0 when resolved, 1 when not, and 2 when the input is unusable.
//
// The rule is the reference harness's (swebench/harness/grading.py, MIT, see
// parsers.go): a FAIL_TO_PASS test resolves on PASSED or XFAIL, a PASS_TO_PASS
// test is maintained on PASSED, XFAIL or SKIPPED, and an expected test the log
// never names counts as failed. Truncated parametrized ids, which Verified
// carries in bulk, resolve by prefix when every candidate agrees on the outcome.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/egladman/magus/internal/json"
)

const (
	markerStart     = ">>>>> Start Test Output"
	markerEnd       = ">>>>> End Test Output"
	markerExitCode  = ">>>>> Test Exit Code"
	statusMissing   = "MISSING"
	exitUnresolved  = 1
	exitInputBroken = 2
)

// badMarkers are the eval script's own failure announcements; a log carrying one
// describes a run that never reached the tests, whatever else it contains.
var badMarkers = []string{
	">>>>> Patch Apply Failed",
	">>>>> Reset Failed",
	">>>>> Tests Errored",
	">>>>> Tests Timed Out",
}

var exitCodeLine = regexp.MustCompile(regexp.QuoteMeta(markerExitCode) + `:\s*(-?\d+)`)

type instance struct {
	InstanceID string   `json:"instance_id"`
	LogParser  string   `json:"log_parser"`
	FailToPass testList `json:"FAIL_TO_PASS"`
	PassToPass testList `json:"PASS_TO_PASS"`
}

// testList accepts both encodings the dataset uses for its test columns: a JSON
// array, and a JSON array serialized into a string, which is what the Hugging
// Face rows API returns.
type testList []string

func (t *testList) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		b = []byte(s)
	}
	var names []string
	if err := json.Unmarshal(b, &names); err != nil {
		return err
	}
	*t = names
	return nil
}

type verdict struct {
	Resolved   bool              `json:"resolved"`
	FailToPass map[string]string `json:"fail_to_pass"`
	PassToPass map[string]string `json:"pass_to_pass"`
	// Error names why a log was rejected before any test was graded; empty when
	// the verdict came from parsed results.
	Error string `json:"error,omitempty"`
}

func main() {
	resolved, err := run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "swegrade: %v\n", err)
		os.Exit(exitInputBroken)
	}
	if !resolved {
		os.Exit(exitUnresolved)
	}
}

// run grades stdin against the --instance row and prints the verdict; an error
// means the input was unusable and no verdict was printed.
func run() (resolved bool, err error) {
	rowPath := flag.String("instance", "", "path to the instance row (one line of verified.jsonl)")
	flag.Parse()
	if *rowPath == "" {
		return false, errors.New("--instance is required")
	}
	rowBytes, err := os.ReadFile(*rowPath)
	if err != nil {
		return false, err
	}
	var row instance
	if err := json.Unmarshal(rowBytes, &row); err != nil {
		return false, fmt.Errorf("%s: %w", *rowPath, err)
	}
	if _, ok := parsers[row.LogParser]; !ok {
		return false, fmt.Errorf("%s names log_parser %q, which is not ported", row.InstanceID, row.LogParser)
	}
	log, err := io.ReadAll(os.Stdin)
	if err != nil {
		return false, fmt.Errorf("reading stdin: %w", err)
	}
	v := grade(row, string(log))
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return false, err
	}
	fmt.Println(string(out))
	return v.Resolved, nil
}

// grade applies the resolution rule to one log. The caller has already checked
// that row.LogParser is ported.
func grade(row instance, log string) verdict {
	v := verdict{FailToPass: map[string]string{}, PassToPass: map[string]string{}}
	for _, name := range row.FailToPass {
		v.FailToPass[name] = statusMissing
	}
	for _, name := range row.PassToPass {
		v.PassToPass[name] = statusMissing
	}

	sm, err := parseLog(parsers[row.LogParser], log)
	if err != nil {
		v.Error = err.Error()
		return v
	}

	resolved := true
	for _, name := range row.FailToPass {
		status := resolveStatus(name, sm)
		v.FailToPass[name] = status
		if !passing(status) {
			resolved = false
		}
	}
	for _, name := range row.PassToPass {
		status := resolveStatus(name, sm)
		v.PassToPass[name] = status
		if !passing(status) && status != statusSkipped {
			resolved = false
		}
	}
	v.Resolved = resolved
	return v
}

func passing(status string) bool {
	return status == statusPassed || status == statusXfail
}

// parseLog mirrors get_logs_eval: a bad marker or a missing output marker rejects
// the log outright; otherwise the region between the markers is parsed, falling
// back to the whole log when the region yields nothing, since some runners
// interleave stdout and stderr across the markers. A recorded non-zero test
// exit status with no failure parsed means the log is not describing the run
// that happened, and is rejected too.
func parseLog(parse func(string) statusMap, log string) (statusMap, error) {
	for _, marker := range badMarkers {
		if strings.Contains(log, marker) {
			return nil, fmt.Errorf("log carries %q", marker)
		}
	}
	if !strings.Contains(log, markerStart) || !strings.Contains(log, markerEnd) {
		return nil, errors.New("log carries no test output markers, so the test patch never ran")
	}
	_, region, _ := strings.Cut(log, markerStart)
	region, _, _ = strings.Cut(region, markerEnd)
	sm := parse(region)
	if len(sm) == 0 {
		sm = parse(log)
	}
	if g := exitCodeLine.FindStringSubmatch(log); len(g) > 1 && g[1] != "0" && len(sm) > 0 {
		failed := false
		for _, status := range sm {
			if status == statusFailed || status == statusError {
				failed = true
				break
			}
		}
		if !failed {
			return nil, fmt.Errorf("test command exited %s while the log reports no failure", g[1])
		}
	}
	return sm, nil
}

// resolveStatus looks a dataset test id up in the parsed map, or MISSING. An id
// with more "[" than "]" was truncated mid-parameter when the dataset was built;
// for those only, a prefix match wins when every candidate grades the same way.
// The lowest matching key is reported so the verdict does not depend on map order.
func resolveStatus(name string, sm statusMap) string {
	if status, ok := sm[name]; ok {
		return status
	}
	if strings.Count(name, "[") <= strings.Count(name, "]") {
		return statusMissing
	}
	var lowest string
	found := false
	for key, status := range sm {
		if !strings.HasPrefix(key, name) {
			continue
		}
		// Upstream's _resolve_case compares passing alone, so SKIPPED and FAILED
		// candidates count as agreeing and the lowest key decides. Kept as it is
		// because a verdict that differs from upstream is a grading bug here,
		// whatever one thinks of the rule.
		if found && passing(status) != passing(sm[lowest]) {
			return statusMissing
		}
		if !found || key < lowest {
			lowest, found = key, true
		}
	}
	if !found {
		return statusMissing
	}
	return sm[lowest]
}
