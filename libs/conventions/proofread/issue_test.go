package proofread

import "testing"

func TestIssueReproAdvisesABugReportThatCannotBeReproduced(t *testing.T) {
	advise := func(match string) []Finding {
		return []Finding{{
			Rule: RuleIssueRepro,
			Message: "Say what happened against what you expected, or the steps to reproduce it: a reader cannot " +
				"start on a defect they cannot see.",
			Match: match, Line: 1, Decision: DecisionAdvise,
		}}
	}

	cases := []struct {
		name, text string
		want       []Finding
	}{
		{
			name: "a crash and nothing to follow",
			text: "magus affected crashes on a detached HEAD\n\nIt crashes when I run it in CI.",
			want: advise("crashes"),
		},
		{
			name: "a title alone",
			text: "The guard fails open in a split worktree",
			want: advise("fails"),
		},
		{
			name: "a report with expected and actual",
			text: "magus affected crashes on a detached HEAD\n\n## Expected\n\nA list of projects.\n\n## Actual\n\nA panic.",
		},
		{
			name: "steps to reproduce",
			text: "The cache key changes after a rename\n\nSteps to reproduce:\n\n1. Rename a file.\n2. Run `magus ls`.",
		},
		{
			name: "an inline instead of",
			text: "The queue reports a wrong state\n\nIt reports WAIT_BASE_RED instead of PASS on a green candidate.",
		},
		{
			name: "a feature request has no defect word",
			text: "Make merge queue validation fast and observable\n\n" +
				"## Problem\n\nA single change can spend more than 20 minutes in merge queue validation.",
			want: nil,
		},
		{name: "the other real issue", text: "Merge queue\n\nTrack the queue's rollout."},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertFindings(t, JudgeText(tc.text, KindIssue, WithOnly(RuleIssueRepro)), tc.want)
		})
	}
}

// An issue is held to the word and claim rules as a description is, with its
// first line as a title.
func TestIssueMeetsTheWordAndClaimRules(t *testing.T) {
	const text = "Make merge queue validation fast and observable\n\n" +
		"A single change can spend more than 20 minutes in merge queue validation.\n"

	got := JudgeText(text, KindIssue)
	if len(got) != 1 || got[0].Rule != RuleClaim || got[0].Match != "20 minutes" || got[0].Line != 3 ||
		got[0].Decision != DecisionAdvise {
		t.Errorf("got %+v, want one advised claim on line 3", got)
	}
}

func TestIssueReadsItsFirstLineAsATitleAndNoFrontMatter(t *testing.T) {
	got := JudgeText("---\nsimply a bug\n---\nbody", KindIssue, WithOnly(RuleFiller))
	if len(got) != 1 || got[0].Line != 2 {
		t.Errorf("got %+v, want a finding on line 2: a rule is not front matter", got)
	}
}
