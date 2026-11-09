package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/proofread"
)

// seriesCommits are twelve commits that each hold one series of three: each
// is ordinary alone, and twelve of them are a habit.
func seriesCommits() string {
	var texts []string

	for _, area := range []string{"parser", "writer", "loader", "tests", "docs", "cache", "index", "server",
		"client", "config", "logger", "router"} {
		texts = append(texts, "update the "+area+"\n\nThe "+area+" reads the key, the value and the stamp "+
			"before it writes anything back to the store on disk.")
	}

	return strings.Join(texts, "\x00")
}

func TestSetJudgesNULSeparatedTextsAsOne(t *testing.T) {
	code, stdout, stderr := runVoiceArgs(t, seriesCommits(), "commit-message", "--stdin", "-set", "-o", "json",
		"-decisions", houseTable(t, proofread.RuleTriad))

	var got []finding
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("decode %q: %v (stderr %q)", stdout, err, stderr)
	}

	if len(got) == 0 || code != exitFindings {
		t.Fatalf("set = %+v, code %d, want triad findings across the set", got, code)
	}

	for _, f := range got {
		if f.Node != setNode || f.Rule != string(proofread.RuleTriad) || f.Line != 0 {
			t.Errorf("finding = %+v, want a triad finding on the set with no line", f)
		}
	}
}

func TestSetSaysWhenTheSetIsTooShort(t *testing.T) {
	_, stdout, stderr := runVoiceArgs(t, "fix the cache\x00fix the docs", "commit-message", "--stdin", "-set")

	if stdout != "[]\n" || !strings.Contains(stderr, "the set of 2 texts holds under 200 words") {
		t.Errorf("short set: stdout %q stderr %q", stdout, stderr)
	}
}

func TestSetNeedsStdin(t *testing.T) {
	dir := t.TempDir()
	path := writeFile(t, dir, "notes.md", "Some notes.\n")

	if code, _, stderr := runVoiceArgs(t, "", "reference", "-set", path); code != exitError ||
		!strings.Contains(stderr, "--set reads its texts from stdin") {
		t.Errorf("set from a file: code %d stderr %q", code, stderr)
	}
}
