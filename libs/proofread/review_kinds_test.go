package proofread

import (
	"reflect"
	"strings"
	"testing"
)

func ruleCheck(rule Rule) (check, bool) {
	for _, c := range checks {
		if c.rule == rule {
			return c, true
		}
	}

	return check{}, false
}

func TestReviewKindChecksRunInThisOrder(t *testing.T) {
	want := []Rule{
		RuleNoReason, RuleNitLabel, RuleEmptyRequestChanges, RuleApproveBlocker, RuleBareLGTM, RuleDeferralNoLink,
		RuleDeclineNoReason, RuleImpatience, RuleReviewLength,
	}

	if got := ruleNames(reviewKindChecks); !reflect.DeepEqual(got, want) {
		t.Errorf("reviewKindChecks = %q, want %q", got, want)
	}
}

// Every review text was review-reply before the split, so a rule that judged
// one still judges all three.
func TestReviewRulesJudgeEveryReviewKind(t *testing.T) {
	for _, rule := range []Rule{
		RuleWhyOpener, RuleBareImperative, RuleAllCaps, RuleRepeatedMarks, RuleReplyOpener, RuleJudgmentAsFact,
		RuleStackedHedge, RuleLongThread, RuleBlame,
	} {
		c, ok := ruleCheck(rule)
		if !ok {
			t.Fatalf("no check for %s", rule)
		}

		for _, k := range reviewText {
			if c.declaredDecision(k) == DecisionOff {
				t.Errorf("%s does not judge %s", rule, k)
			}
		}
	}
}

func TestReviewKindRulesAdviseOnTheirKindAlone(t *testing.T) {
	cases := []struct {
		rule  Rule
		text  string
		event Event
		kind  Kind
	}{
		{RuleNoReason, "Use a sync.Map here.", "", KindReviewInline},
		{RuleNitLabel, "nit: this must be guarded by the lock.", "", KindReviewInline},
		{RuleEmptyRequestChanges, "Needs work.", EventRequestChanges, KindReviewSummary},
		{RuleApproveBlocker, "Approving, but the lock must be fixed first.", EventApprove, KindReviewSummary},
		{RuleBareLGTM, "LGTM", EventApprove, KindReviewSummary},
		{RuleDeferralNoLink, "I'll fix this later.", "", KindReviewReply},
		{RuleDeclineNoReason, "Won't fix.", "", KindReviewReply},
		{RuleImpatience, "As I said, the map is shared.", "", KindReviewReply},
	}

	for _, tc := range cases {
		t.Run(string(tc.rule), func(t *testing.T) {
			want := map[Kind]Decision{tc.kind: DecisionAdvise}
			if got := decisions(tc.rule, tc.text, []Option{WithEvent(tc.event)}, reviewText...); !reflect.DeepEqual(got, want) {
				t.Errorf("%s judged %v, want %v", tc.rule, got, want)
			}
		})
	}
}

// A review's verdict is all approve-blocker and empty-request-changes read
// besides the body, so without one they judge nothing.
func TestSummaryRulesNeedTheEvent(t *testing.T) {
	for _, text := range []string{"Needs work.", "Approving, but the lock must be fixed first."} {
		for _, f := range JudgeText(text, KindReviewSummary) {
			if f.Rule == RuleEmptyRequestChanges || f.Rule == RuleApproveBlocker {
				t.Errorf("%q with no event: %s fired", text, f.Rule)
			}
		}
	}
}

func TestNitLabelNamesTheLabelAndTheWord(t *testing.T) {
	runTextCases(t, RuleNitLabel, []textCase{
		{"nit on a demand", KindReviewInline, "nit: this must be guarded.", []string{"1:must"}},
		{"the decoration is not the text", KindReviewInline, "suggestion (non-blocking): rename it.", nil},
		{"a demand on a later line", KindReviewInline, "Nit: the name.\n\nIt is required by the linter.", []string{"3:required"}},
	})
}

func TestBareLGTMReportsTheLineItSitsOn(t *testing.T) {
	runTextCases(t, RuleBareLGTM, []textCase{
		{"the first line", KindReviewSummary, "LGTM", []string{"1:LGTM"}},
		{"after blank lines", KindReviewSummary, "\n\nLGTM!", []string{"3:LGTM!"}},
	})
}

func TestDeferralNoLinkReadsAStandardsNameAsNoTrackerKey(t *testing.T) {
	runTextCases(t, RuleDeferralNoLink, []textCase{
		{"a tracker key", KindReviewReply, "I'll fix this later, see MAG-123.", nil},
		{"an encoding", KindReviewReply, "I'll fix the UTF-8 handling later.", []string{"1:'ll fix the UTF-8 handling later"}},
		{"a hash", KindReviewReply, "We can move to SHA-256 later.", []string{"1:can move to SHA-256 later"}},
		{"a date standard", KindReviewReply, "I'll parse ISO-8601 later.", []string{"1:'ll parse ISO-8601 later"}},
		{"a standard beside a key", KindReviewReply, "I'll switch to SHA-256 later, tracked in MAG-9.", nil},
	})
}

func TestNoReasonReadsIntentAgainstBehaviorAsTheReason(t *testing.T) {
	runTextCases(t, RuleNoReason, []textCase{
		{"doc against behavior", KindReviewInline, "The doc says it returns an error but this returns nil, return the error.", nil},
		{"expected against actual", KindReviewInline, "Expected a copy, but this shares the slice, so copy it", nil},
		{"two sentences", KindReviewInline, "This returns nil on miss; the doc says it returns an error. Return the error.", nil},
		{"the request alone", KindReviewInline, "Return the error here.", []string{"1:Return"}},
		{"an expectation with no behavior", KindReviewInline, "You should return the error here.", []string{"1:should"}},
	})
}

func TestReviewLengthCapsEachKind(t *testing.T) {
	words := func(n int) string { return strings.TrimSpace(strings.Repeat("word ", n)) + "." }

	runTextCases(t, RuleReviewLength, []textCase{
		{"inline at the cap", KindReviewInline, words(60), nil},
		{"inline over", KindReviewInline, words(61), []string{"1:"}},
		{"summary at the cap", KindReviewSummary, words(100), nil},
		{"summary over", KindReviewSummary, words(101), []string{"1:"}},
		{"reply at the cap", KindReviewReply, words(80), nil},
		{"reply over", KindReviewReply, words(81), []string{"1:"}},
		{"a description is not capped", KindChangeDescription, words(300), nil},
	})
}
