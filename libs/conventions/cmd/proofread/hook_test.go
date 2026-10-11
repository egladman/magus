package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/proofread"
)

// A stale suppression is a finding on every kind, so these tests make one on
// a known line instead of depending on the prose rules a kind runs.
const stale = "proofread:ignore filler quoting the user"

func TestStripCommitCommentsDropsCommentsAndEverythingAfterTheScissors(t *testing.T) {
	cases := []struct {
		name       string
		in         string
		wantKept   string
		wantOrigin []int
	}{
		{"no comments", "Fix it\n\nBody.\n", "Fix it\n\nBody.\n", []int{1, 2, 3}},
		{"comment lines", "# top\nFix it\n# middle\nBody.\n", "Fix it\nBody.\n", []int{2, 4}},
		{"an indented hash stays", "Fix it\n  # code\n", "Fix it\n  # code\n", []int{1, 2}},
		{"the scissors line", "Fix it\n# ------------------------ >8 ------------------------\ndiff simply\n", "Fix it\n", []int{1}},
		{"nothing but comments", "# a\n# b\n", "", nil},
	}

	for _, tc := range cases {
		kept, origin := stripCommitComments(tc.in)
		if kept != tc.wantKept || !reflect.DeepEqual(origin, tc.wantOrigin) {
			t.Errorf("%s: got %q %v, want %q %v", tc.name, kept, origin, tc.wantKept, tc.wantOrigin)
		}
	}
}

func TestFromFilesReadsACommitMessageFromAFileOnlyWhenNamed(t *testing.T) {
	if fromFiles(proofread.KindCommitMessage, nil) || !fromFiles(proofread.KindCommitMessage, []string{"m"}) {
		t.Error("a commit message is read from stdin unless a file is named")
	}

	if !fromFiles(proofread.KindReference, nil) || fromFiles(proofread.KindChangeDescription, []string{"m"}) {
		t.Error("reference reads files, change-description never does")
	}
}

func TestRunJudgesACommitMessageFileWithoutItsCommentLines(t *testing.T) {
	message := "# Please enter the commit message. " + stale + "\n" +
		"Fix the parser\n" +
		"\n" +
		"# A comment. " + stale + "\n" +
		"Body line. " + stale + "\n" +
		"# ------------------------ >8 ------------------------\n" +
		"diff: " + stale + "\n"
	path := writeFile(t, t.TempDir(), "COMMIT_EDITMSG", message)

	var got []finding

	runInto(t, []string{"commit-message", path}, &got)

	if len(got) != 1 {
		t.Fatalf("got %+v, want the one stale suppression in the body", got)
	}

	f := got[0]
	if f.Rule != string(proofread.RuleSuppressionUnused) || f.Line != 5 || f.Source != path+":5" || f.Node != path ||
		f.Kind != "commit-message" || f.Match != "proofread:ignore filler quoting the user" {
		t.Errorf("finding = %+v, want %s on line 5 of %s", f, proofread.RuleSuppressionUnused, path)
	}
}

func TestRunJudgesACommitMessageOnStdinAsItWas(t *testing.T) {
	var got []finding

	code, out, errOut := runArgs(t, []string{"commit-message"}, "Fix the parser\n\nBody. "+stale+"\n")
	if code != exitOK || errOut != "" {
		t.Fatalf("run: code %d stderr %q", code, errOut)
	}

	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Node != "commit-message" || got[0].Source != "commit-message:3" {
		t.Errorf("findings = %+v, want one on line 3, named by the kind", got)
	}
}

func TestRunJudgesNothingInAMessageOfOnlyComments(t *testing.T) {
	path := writeFile(t, t.TempDir(), "COMMIT_EDITMSG", "# Please enter a message.\n# "+stale+"\n")

	assertArgs(t, []string{"commit-message", path}, "", "[]\n")
}

func TestRunFailsAHookOnADeniedCommitMessage(t *testing.T) {
	dir := t.TempDir()
	bad := writeFile(t, dir, "BAD", "# comment\nFix the parser\n\nBody. "+stale+"\n")
	args := []string{"commit-message", "-format", "text", "-fail-on", "deny", bad}

	code, out, errOut := runArgs(t, args, "")
	if code != exitFindings || !strings.HasPrefix(out, bad+":4:") || !strings.Contains(out, "suppression-unused [deny]") ||
		errOut != "proofread: 1 finding (1 deny, 0 advise)\n" {
		t.Errorf("a denied message: code %d stdout %q stderr %q", code, out, errOut)
	}

	if code, _, _ := runArgs(t, []string{"commit-message", "-fail-on", "deny", dir + "/missing"}, ""); code != exitError {
		t.Errorf("a missing message file: code %d, want %d", code, exitError)
	}
}

func TestRunAppliesAPathScopedDecisionToACommitMessageFile(t *testing.T) {
	dir := t.TempDir()
	message := writeFile(t, dir, "MSG", "Fix the parser\n\nBody. "+stale+"\n")
	table := writeFile(t, dir, "d.json", `{"paths":{"**/MSG":{"suppression-unused":"off"}}}`)

	assertArgs(t, []string{"commit-message", "-decisions", table, message}, "", "[]\n")
}
