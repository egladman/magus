package proofread

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestSubjectMoodReportsAPastThirdPersonOrGerundOpener(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{
			name: "third person after a scope",
			text: "feat(proofread): adds a vale spell",
			want: []Finding{moodFinding("adds", "add")},
		},
		{
			name: "past tense, capitalized",
			text: "Fixed the port",
			want: []Finding{moodFinding("Fixed", "Fix")},
		},
		{
			name: "gerund after a breaking prefix",
			text: "fix!: making the key stable",
			want: []Finding{moodFinding("making", "make")},
		},
		{name: "an irregular past", text: "wrote the key down", want: []Finding{moodFinding("wrote", "write")}},
		{name: "the imperative", text: "feat: add an opt-in claude-code-mod harness and a harness_settings op (#577)"},
		{name: "an imperative on main", text: "fix: skip forks pending exec in the pipe walk (#574)"},
		{name: "an acronym lead", text: "URL-parse the port"},
		{name: "a noun a verb list leaves out", text: "address the review"},
		{name: "a word that only starts like a verb", text: "fixture the key"},
		{name: "a revert", text: "Revert \"feat: added a vale spell\"\n\nThis reverts commit 4f9939179."},
		{name: "a fixup", text: "fixup! added a vale spell"},
		{name: "a merge", text: "Merge branch 'main' into fixing-the-key"},
		{name: "a leading space on main", text: " Update actions (#50)"},
		{name: "an empty message", text: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, WithOnly(RuleSubjectMood)), tc.want)
		})
	}
}

func moodFinding(word, want string) Finding {
	return Finding{
		Rule:         RuleSubjectMood,
		Message:      fmt.Sprintf("Open the subject with the imperative: '%s', not '%s'.", want, word),
		Match:        word,
		Line:         1,
		Replacements: []string{want},
	}
}

func TestSubjectMoodPointsAtTheWordAfterAPrefix(t *testing.T) {
	got := JudgeText("feat(proofread): adds a vale spell", KindCommitMessage, WithOnly(RuleSubjectMood))
	if len(got) != 1 || got[0].Column != 18 || got[0].EndColumn != 22 {
		t.Errorf("got %+v, want columns 18 to 22", got)
	}
}

func TestSubjectLengthCapsTheSubjectAt100Bytes(t *testing.T) {
	at := "fix: " + strings.Repeat("x", 95)
	over := at + "y"

	lengthFinding := func(n int) []Finding {
		return []Finding{{
			Rule: RuleSubjectLength,
			Message: fmt.Sprintf("Keep the subject to 100 bytes (this one has %d): cut it, or move the detail to "+
				"the body.", n),
			Line: 1,
		}}
	}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"100 bytes", at, nil},
		{"101 bytes", over, lengthFinding(101)},
		{"a forge's pull request number is not counted", over[:len(over)-1] + " (#546)", nil},
		{"the number does not rescue a longer subject", over + " (#546)", lengthFinding(101)},
		{"the body is not the subject", "fix: add the key\n\n" + strings.Repeat("word ", 40), nil},
		{"a revert keeps the subject it reverts", "Revert \"" + over + "\"", nil},
		{
			"a real subject over the cap, before the hook",
			"split the daemon into magus broker and magus server; tie claims and service references to a connection",
			lengthFinding(len("split the daemon into magus broker and magus server; tie claims and service references to a connection")),
		},
		{"a real subject under it", "fix: drop forwarded args at a cross-project dependency (#546)", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, WithOnly(RuleSubjectLength)), tc.want)
		})
	}
}

func TestSubjectPeriodReportsATrailingFullStop(t *testing.T) {
	cases := []struct {
		name, text string
		want       []Finding
	}{
		{
			name: "a period",
			text: "Fix the port.",
			want: []Finding{{
				Rule: RuleSubjectPeriod, Message: "Drop the period ending the subject: it is a title.",
				Match: "port.", Line: 1, Replacements: []string{"port"},
			}},
		},
		{name: "an ellipsis", text: "wait for the daemon..."},
		{name: "a version", text: "bump go to 1.25"},
		{name: "a period in the body", text: "bump go to 1.25\n\nThe module needs it."},
		{name: "a dotted name in the middle", text: "rename magus.lock to magus.sum"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, WithOnly(RuleSubjectPeriod)), tc.want)
		})
	}
}

func TestBodySeparatorWantsABlankLineAfterTheSubject(t *testing.T) {
	separator := []Finding{{
		Rule:    RuleBodySeparator,
		Message: "Leave a blank line between the subject and the body: git takes the first paragraph as the subject.",
		Line:    2,
	}}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{"a body with no gap", "fix: add the key\nThe key was missing.", separator},
		{"a gap", "fix: add the key\n\nThe key was missing.", nil},
		{"a subject alone", "fix: add the key", nil},
		{"a subject and a trailer", "fix: add the key\nCo-Authored-By: A Person <a@example.com>", nil},
		{"a subject and trailers", "fix: add the key\nFixes: #12\nSigned-off-by: A Person <a@example.com>", nil},
		{"a breaking footer", "fix!: add the key\nBREAKING CHANGE: the old key is gone", nil},
		{"a trailer after prose with no gap", "fix: add the key\nThe key was missing.\nFixes: #12", separator},
		{"a merge", "Merge branch 'main'\nline", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindCommitMessage, WithOnly(RuleBodySeparator)), tc.want)
		})
	}
}

// A commit meets the word rules of a written text, but not the two a pull
// request description alone is held to.
func TestCommitMessageMeetsTheWordRulesButNotTheDescriptionShape(t *testing.T) {
	const msg = "make the agent guard actually fire\n\n" +
		"The hook never fired after a rename, because the matcher kept the old key."

	got := JudgeText(msg, KindCommitMessage)

	var rules []Rule
	for _, f := range got {
		rules = append(rules, f.Rule)
	}

	if want := []Rule{RuleFiller, RuleAbsolute}; !slices.Equal(rules, want) {
		t.Errorf("rules = %v, want %v: %+v", rules, want, got)
	}

	for _, f := range got {
		if f.Rule == RuleLeadContext || f.Rule == RuleCredit {
			t.Errorf("a commit is held to %s: %+v", f.Rule, f)
		}
	}

	if got[0].Match != "actually" || got[0].Line != 1 || got[0].Decision != DecisionDeny {
		t.Errorf("filler: %+v", got[0])
	}

	if got[1].Match != "never fired" || got[1].Line != 3 || got[1].Decision != DecisionAdvise {
		t.Errorf("absolute: %+v", got[1])
	}
}

func TestCommitMessageSkipsPullRequestRules(t *testing.T) {
	for _, r := range []Rule{RuleLeadContext, RuleCredit, RuleReplyOpener, RuleLongThread} {
		for _, k := range KindRules(KindCommitMessage) {
			if k == r {
				t.Errorf("%s judges a commit message", r)
			}
		}
	}

	// A subject such as "drop X" is a removal, which credit asks to explain.
	got := JudgeText("drop the v1 key", KindCommitMessage)
	if len(got) != 0 {
		t.Errorf("got %+v, want none", got)
	}
}
