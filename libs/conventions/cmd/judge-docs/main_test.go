package main

import (
	"bytes"
	"strings"
	"testing"
)

const nameSuffix = `{"node":"n","source":"","language":"","rule":"name-suffix","message":"Rename 'configFor': no function or method name ends in the word Of or For.","match":"configFor"}`

func runJudge(t *testing.T, stdin string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code = run(strings.NewReader(stdin), &out, &errOut)

	return code, out.String(), errOut.String()
}

func assertRun(t *testing.T, stdin string, wantCode int, wantStdout, wantStderr string) {
	t.Helper()

	code, stdout, stderr := runJudge(t, stdin)
	if code != wantCode || stdout != wantStdout || stderr != wantStderr {
		t.Errorf("run:\n got code %d stdout %q stderr %q\nwant code %d stdout %q stderr %q",
			code, stdout, stderr, wantCode, wantStdout, wantStderr)
	}
}

func TestRunWritesAFindingForAJudgedDoc(t *testing.T) {
	in := `[{"node":"n1","source":"a.go","language":"go","name":"Resolve","kind":"function","owner":"","doc":"Resolve simply returns the path."}]`
	want := `[{"node":"n1","source":"a.go","language":"go","rule":"filler","message":"Drop 'simply': state the fact.","match":"simply"}]` + "\n"

	assertRun(t, in, 0, want, "")
}

func TestRunWritesAnEmptyArrayWhenNothingIsFound(t *testing.T) {
	in := `[{"node":"n1","source":"a.go","language":"go","name":"Resolve","kind":"function","owner":"","doc":"Resolve returns the path."}]`

	assertRun(t, in, 0, "[]\n", "")
}

func TestRunWritesAnEmptyArrayForAnEmptyInput(t *testing.T) {
	assertRun(t, `[]`, 0, "[]\n", "")
}

func TestRunKeepsInputOrderThenFindingOrder(t *testing.T) {
	in := `[` +
		`{"node":"b","name":"Run","kind":"function","doc":"Run simply hands work to a sub-agent."},` +
		`{"node":"a","name":"Open","kind":"function","doc":"Open simply opens."}]`
	want := `[` +
		`{"node":"b","source":"","language":"","rule":"filler","message":"Drop 'simply': state the fact.","match":"simply"},` +
		`{"node":"b","source":"","language":"","rule":"terms","message":"Write 'subagent', not 'sub-agent'.","match":"sub-agent"},` +
		`{"node":"a","source":"","language":"","rule":"filler","message":"Drop 'simply': state the fact.","match":"simply"}]` + "\n"

	assertRun(t, in, 0, want, "")
}

func TestRunJudgesANameSuffixOnlyOnACallable(t *testing.T) {
	cases := []struct {
		kind string
		want string
	}{
		{"function", "[" + nameSuffix + "]\n"},
		{"method", "[" + nameSuffix + "]\n"},
		{"type", "[]\n"},
		{"", "[]\n"},
	}

	for _, tc := range cases {
		t.Run("kind "+tc.kind, func(t *testing.T) {
			in := `[{"node":"n","name":"configFor","kind":"` + tc.kind + `","doc":""}]`

			assertRun(t, in, 0, tc.want, "")
		})
	}
}

func TestRunExitsOneOnInputThatIsNotAnArrayOfRecords(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantStderr string
	}{
		{"malformed", `[{"node":`, "judge-docs: read symbols: unexpected EOF\n"},
		{"empty stdin", ``, "judge-docs: read symbols: EOF\n"},
		{"unknown field", `[{"node":"n","extra":1}]`, "judge-docs: read symbols: json: unknown field \"extra\"\n"},
		{"trailing data", `[] []`, "judge-docs: read symbols: data after the array\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertRun(t, tc.in, 1, "", tc.wantStderr)
		})
	}
}
