package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const nameSuffix = `{"node":"n","source":"","language":"","rule":"name-suffix","message":"Rename 'configFor': no function or method name ends in the word Of or For.","match":"configFor"}`

func runJudge(t *testing.T, stdin string) (code int, stdout, stderr string) {
	t.Helper()

	var out, errOut bytes.Buffer

	code = run(nil, strings.NewReader(stdin), &out, &errOut)

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

func TestRunJudgesMarkdownFilesInArgumentOrder(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.md"), filepath.Join(dir, "b.md")

	if err := os.WriteFile(a, []byte("# A\n\nIt simply works.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(b, []byte("- **Cache:** on\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer

	code := run([]string{"-kind", "markdown", b, a}, strings.NewReader(""), &out, &errOut)

	want := `[` +
		`{"node":"` + b + `","source":"` + b + `:1","language":"markdown","rule":"reply-voice","message":"Drop the bold label '**Cache:**': write the item as a sentence that opens with its subject.","match":"**Cache:**"},` +
		`{"node":"` + a + `","source":"` + a + `:3","language":"markdown","rule":"filler","message":"Drop 'simply': state the fact.","match":"simply"}]` + "\n"
	if code != 0 || out.String() != want || errOut.String() != "" {
		t.Errorf("run:\n got code %d stdout %q stderr %q\nwant code 0 stdout %q", code, out.String(), errOut.String(), want)
	}
}

func TestRunJudgesASkillSourceAtItsSourceLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "SKILL.md")
	body := "# Skill\n\n{{if .Full}}One.\n\nTwo.\n{{end}}\nRun it in order to replay.\n"

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer

	code := run([]string{"-kind", "skill-source", path}, strings.NewReader(""), &out, &errOut)

	want := `[{"node":"` + path + `","source":"` + path + `:7","language":"skill-source","rule":"wordy",` +
		`"message":"Write 'to', not 'in order to'.","match":"in order to"}]` + "\n"
	if code != 0 || out.String() != want || errOut.String() != "" {
		t.Errorf("run:\n got code %d stdout %q stderr %q\nwant code 0 stdout %q", code, out.String(), errOut.String(), want)
	}
}

func TestRunJudgesAGuideOnTheGuideRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guide.md")

	if err := os.WriteFile(path, []byte("# Guide\n\nThen we run it.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer

	code := run([]string{"-kind", "guide", path}, strings.NewReader(""), &out, &errOut)

	want := `[{"node":"` + path + `","source":"` + path + `:3","language":"guide","rule":"second-person",` +
		`"message":"Address the reader as you, not 'we': a guide speaks to the person following it, ` +
		`and names magus or the project where it means them.","match":"we"}]` + "\n"
	if code != 0 || out.String() != want || errOut.String() != "" {
		t.Errorf("run:\n got code %d stdout %q stderr %q\nwant code 0 stdout %q", code, out.String(), errOut.String(), want)
	}
}

func TestRunJudgesAPullRequestFromStdin(t *testing.T) {
	var out, errOut bytes.Buffer

	code := run([]string{"-kind", "pull-request"}, strings.NewReader("fix: pin the key\n## Summary\n"), &out, &errOut)

	want := `[{"node":"pull-request","source":"pull-request:2","language":"pull-request","rule":"lead-context",` +
		`"message":"It opens with a heading: open the description with a paragraph naming the goal behind the change ` +
		`and why this code stands in its way, then the bullets.","match":"## Summary"}]` + "\n"
	if code != 0 || out.String() != want || errOut.String() != "" {
		t.Errorf("run:\n got code %d stdout %q stderr %q\nwant code 0 stdout %q", code, out.String(), errOut.String(), want)
	}
}

func TestRunExitsOneOnAFlagItCannotUse(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		wantStderr string
	}{
		{"unknown kind", []string{"-kind", "doc"}, "judge-docs: unknown kind \"doc\": want markdown, guide, skill, skill-source or pull-request\n"},
		{"a path for symbols", []string{"a.md"}, "judge-docs: symbols are read from stdin; a path needs -kind markdown\n"},
		{"a path for a pull request", []string{"-kind", "pull-request", "a.md"}, "judge-docs: a pull request is read from stdin, not from a path\n"},
		{"a missing file", []string{"-kind", "markdown", "missing.md"}, "judge-docs: read missing.md: open missing.md: no such file or directory\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer

			code := run(tc.args, strings.NewReader("[]"), &out, &errOut)
			if code != 1 || out.String() != "" || errOut.String() != tc.wantStderr {
				t.Errorf("run: got code %d stdout %q stderr %q, want code 1 stderr %q", code, out.String(), errOut.String(), tc.wantStderr)
			}
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
