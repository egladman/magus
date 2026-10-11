package main

import (
	"bytes"
	"encoding/json"
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

	want := "RULE   CASES  TP  FP  FN  TN  PRECISION  RECALL  DISAGREE\n" +
		"hedge  3      1   1   0   1   50.0%      100.0%  0\n"
	if stdout.String() != want {
		t.Errorf("table:\n%s\nwant:\n%s", stdout.String(), want)
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
		"cases and a sample": {[]string{"-sample", "-", "-cases", "d"}, "", "proofread: -cases and -sample cannot be combined"},
		"a bad record":       {[]string{"-sample", "-", "-kind", "message"}, "{}\n{", "proofread: sample line 2: unexpected end of JSON input"},
		"an empty dir":       {[]string{"-cases", "."}, "", "proofread: no case files in ."},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := run(append([]string{"calibrate"}, tc.args...), strings.NewReader(tc.stdin), &stdout, &stderr)
			if code != 1 || strings.TrimSpace(stderr.String()) != tc.want {
				t.Errorf("exit %d, stderr %q, want 1 and %q", code, stderr.String(), tc.want)
			}
		})
	}
}
