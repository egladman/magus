package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/proofread"
)

func TestCalibrateReplaysTheCasesItCarries(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"calibrate", "-format", "json"}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}

	var scores []proofread.Score
	if err := json.Unmarshal(stdout.Bytes(), &scores); err != nil {
		t.Fatal(err)
	}

	if len(scores) != len(proofread.Rules()) {
		t.Errorf("%d scores, want one per rule (%d)", len(scores), len(proofread.Rules()))
	}

	for _, s := range scores {
		if s.Disagree != 0 || s.TP == 0 || s.TN == 0 {
			t.Errorf("%s: %+v", s.Rule, s)
		}
	}
}

func TestCalibrateWritesATableFromACasesDir(t *testing.T) {
	dir := t.TempDir()
	data := "-- hit/reference --\nThe flag may help a slow runner.\n-- pass/reference --\nThe value may be empty.\n" +
		"-- fp/reference --\nIt probably holds.\n"

	if err := os.WriteFile(filepath.Join(dir, "hedge.txtar"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"calibrate", "-cases", dir}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}

	want := "RULE   CASES  TP  FP  FN  TN  PRECISION  LOWER  RECALL  HELD_OUT  HELD_OUT_PRECISION  DISAGREE\n" +
		"hedge  3      1   1   0   1   50.0%      9.5%   100.0%  0         -                   0\n"
	if stdout.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestCalibrateGatesTheCasesItCarries(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"calibrate", "-gates", "-format", "json"}, strings.NewReader(""), &stdout, &stderr); code != 1 {
		t.Fatalf("exit %d, want 1 while a rule ships past its cases: %s", code, stderr.String())
	}

	var gates []proofread.Gate
	if err := json.Unmarshal(stdout.Bytes(), &gates); err != nil {
		t.Fatal(err)
	}

	if len(gates) != len(proofread.Rules()) {
		t.Errorf("%d gates, want one per rule (%d)", len(gates), len(proofread.Rules()))
	}

	for _, g := range gates {
		if g.Dimension == "" || g.Pass != (g.Needs != nil && *g.Needs == 0) {
			t.Errorf("%s: %+v", g.Rule, g)
		}
	}
}

// writeCases writes a case file for rule holding hits right firings, wrong
// fp firings and one pass, each line its own text.
func writeCases(t *testing.T, rule, kind, hit, fp, pass string, hits, wrong int) string {
	t.Helper()

	var b strings.Builder

	fmt.Fprintf(&b, "-- hit/%s --\n", kind)

	for i := range hits {
		fmt.Fprintf(&b, hit+"\n", i)
	}

	if wrong > 0 {
		fmt.Fprintf(&b, "-- fp/%s --\n", kind)

		for i := range wrong {
			fmt.Fprintf(&b, fp+"\n", i)
		}
	}

	fmt.Fprintf(&b, "-- pass/%s --\n%s\n", kind, pass)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, rule+".txtar"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestCalibrateGatesPassAndFail(t *testing.T) {
	cases := map[string]struct {
		hits, wrong int
		code        int
		row         string
	}{
		"deny at 0 of 35 passes": {35, 0, 0, "hedge  evidence   deny     deny      35       0      100.0%     90.1%  " +
			"100.0%              pass  -\n"},
		"deny at 0 of 10 needs 25": {10, 0, 1, "hedge  evidence   deny     off       10       0      100.0%     72.2%  " +
			"100.0%              fail  +25\n"},
		"deny at 1 of 10 cannot reach it": {9, 1, 1, "hedge  evidence   deny     off       10       1      90.0%      " +
			"59.6%  100.0%              fail  unreachable\n"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := writeCases(t, "hedge", "reference", "Runner %d may help.", "It probably holds for %d.",
				"The value may be empty.", tc.hits, tc.wrong)

			var stdout, stderr bytes.Buffer

			code := run([]string{"calibrate", "-gates", "-cases", dir}, strings.NewReader(""), &stdout, &stderr)
			if code != tc.code {
				t.Fatalf("exit %d, want %d: %s%s", code, tc.code, stdout.String(), stderr.String())
			}

			lines := strings.SplitAfter(stdout.String(), "\n")
			if len(lines) < 2 || lines[1] != tc.row {
				t.Errorf("table:\n%s\nwant the row:\n%s", stdout.String(), tc.row)
			}

			if !strings.Contains(stdout.String(), "Not measured here, and also needed for deny: firings from two sources "+
				"and a not-useful rate under 10% in use.\n") {
				t.Errorf("no unmeasured line:\n%s", stdout.String())
			}
		})
	}
}

func TestCalibrateGatesAHouseRuleWithNoDenyLine(t *testing.T) {
	dir := writeCases(t, "dash", "reference", "A range %d\u20149.", "", "A range 1 to 9.", 1, 0)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"calibrate", "-gates", "-cases", dir}, strings.NewReader(""), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s%s", code, stdout.String(), stderr.String())
	}

	want := "0 of 1 rules ship a decision their labeled firings do not support. " +
		"NEEDS is the labeled firings still to add at the rule's current precision.\n"
	if !strings.HasSuffix(stdout.String(), want) || !strings.Contains(stdout.String(), "dash  conventions  off") {
		t.Errorf("table:\n%s", stdout.String())
	}
}

func TestCalibrateRatesASampleByGroup(t *testing.T) {
	sample := `{"user_type":"User","body":"Actually, it simply races."}
{"user_type":"User","body":"The map races."}

{"user_type":"Bot","body":"Great question, the key sorts.","is_reply":true}
{"body":"No group here."}
{"user_type":"Bot","body":""}
`

	var stdout, stderr bytes.Buffer

	args := []string{"calibrate", "-sample", "-", "-kind", "review-reply", "-group", "user_type", "-format", "json"}
	if code := run(args, strings.NewReader(sample), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}

	var groups []proofread.GroupRates
	if err := json.Unmarshal(stdout.Bytes(), &groups); err != nil {
		t.Fatal(err)
	}

	texts := map[string]int{}
	filler := map[string]int{}

	for _, g := range groups {
		texts[g.Group] = g.Texts

		for _, r := range g.Rules {
			if r.Rule == proofread.RuleFiller {
				filler[g.Group] = r.Findings
			}
		}
	}

	if want := map[string]int{"(none)": 1, "Bot": 1, "User": 2}; !maps.Equal(texts, want) {
		t.Errorf("texts per group = %v, want %v", texts, want)
	}

	if filler["User"] != 2 {
		t.Errorf("filler findings for User = %d, want 2", filler["User"])
	}
}

func TestCalibrateJoinsFieldsIntoOneText(t *testing.T) {
	sample := `{"title":"fix: x","body":"- Sorts the inputs.","merged":true}` + "\n"

	var stdout, stderr bytes.Buffer

	args := []string{"calibrate", "-sample", "-", "-kind", "change-description", "-field", "title,body", "-group", "merged"}
	if code := run(args, strings.NewReader(sample), &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}

	if !strings.Contains(stdout.String(), "true   1      lead-context") {
		t.Errorf("table holds no lead-context row for group true:\n%s", stdout.String())
	}
}

func TestCalibrateRefusesFlagsItCannotUse(t *testing.T) {
	cases := map[string]struct {
		args  []string
		stdin string
		want  string
	}{
		"an operand":            {[]string{"x"}, "", `proofread: calibrate takes no operand, got "x"`},
		"a format":              {[]string{"-format", "xml"}, "", `proofread: -format "xml" is neither table nor json`},
		"a kind with no sample": {[]string{"-kind", "review-reply"}, "", "proofread: -kind and -group need -sample"},
		"a sample with no kind": {[]string{"-sample", "-"}, "", "proofread: -sample needs -kind"},
		"an unknown kind":       {[]string{"-sample", "-", "-kind", "poem"}, "", `proofread: unknown kind "poem"`},
		"a template kind": {
			[]string{"-sample", "-", "-kind", "agent-instructions-template"}, "",
			"proofread: -kind agent-instructions-template renders a template first and cannot be sampled",
		},
		"cases and a sample": {
			[]string{"-sample", "-", "-cases", "d"}, "",
			"proofread: -cases and -gates replay the labeled cases and cannot be combined with -sample",
		},
		"gates and a sample": {
			[]string{"-sample", "-", "-gates"}, "",
			"proofread: -cases and -gates replay the labeled cases and cannot be combined with -sample",
		},
		"a bad record": {[]string{"-sample", "-", "-kind", "message"}, "{}\n{", "proofread: sample line 2: unexpected end of JSON input"},
		"an empty dir": {[]string{"-cases", "."}, "", "proofread: no case files in ."},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(append([]string{"calibrate"}, tc.args...), strings.NewReader(tc.stdin), &stdout, &stderr)
			if code != 2 || strings.TrimSpace(stderr.String()) != tc.want {
				t.Errorf("exit %d, stderr %q, want 2 and %q", code, stderr.String(), tc.want)
			}
		})
	}
}
