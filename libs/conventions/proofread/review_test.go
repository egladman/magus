package proofread

import (
	"reflect"
	"testing"
)

func TestReviewChecksRunInThisOrder(t *testing.T) {
	want := []Rule{RuleNonspecific, RuleWhyOpener, RuleBareImperative, RuleAllCaps, RuleRepeatedMarks}

	if got := ruleNames(reviewChecks); !reflect.DeepEqual(got, want) {
		t.Errorf("reviewChecks = %q, want %q", got, want)
	}
}

func TestReviewRulesAdviseOnAReplyOnly(t *testing.T) {
	every := []Kind{
		KindDocComment, KindReference, KindGuide, KindChangeDescription, KindAgentInstructions, KindReviewReply,
	}
	cases := []struct {
		rule Rule
		text string
	}{
		{RuleNonspecific, "This is confusing."},
		{RuleWhyOpener, "Why did you add the lock?"},
		{RuleBareImperative, "Fix this."},
		{RuleAllCaps, "Do NOT merge."},
		{RuleRepeatedMarks, "Is it locked??"},
	}

	for _, tc := range cases {
		t.Run(string(tc.rule), func(t *testing.T) {
			want := map[Kind]Decision{KindReviewReply: DecisionAdvise}
			if got := decisions(tc.rule, tc.text, nil, every...); !reflect.DeepEqual(got, want) {
				t.Errorf("%s judged %v, want %v", tc.rule, got, want)
			}
		})
	}
}

func TestNonspecificReportsAJudgmentNamingNothingToActOn(t *testing.T) {
	runTextCases(t, RuleNonspecific, []textCase{
		{"a verdict", KindReviewReply, "This is confusing.", []string{"1:confusing"}},
		{"a request to change it", KindReviewReply, "Please fix this.", []string{"1:fix this"}},
		{"reported once", KindReviewReply, "This is wrong. It is also redundant.", []string{"1:wrong"}},
		{"an identifier in code", KindReviewReply, "This is confusing: `cacheKey` reads twice.", nil},
		{"a camel-case identifier", KindReviewReply, "Passing cacheKey here is wrong.", nil},
		{"a path", KindReviewReply, "This is redundant with internal/cache/key.go.", nil},
		{"a line", KindReviewReply, "This is unclear, see line 42.", nil},
		{"an example", KindReviewReply, "This is unclear, e.g. what happens on a miss.", nil},
		{"a suggestion block", KindReviewReply, "This is wrong.\n\n```suggestion\nreturn nil\n```", nil},
		{"a reason", KindReviewReply, "This is redundant because the caller already sorts.", nil},
		{"praise", KindReviewReply, "Thanks, looks good.", nil},
		{"a question", KindReviewReply, "Does the caller hold the lock here?", nil},
	})
}

func TestWhyOpenerReportsAQuestionAboutTheAuthorsChoice(t *testing.T) {
	runTextCases(t, RuleWhyOpener, []textCase{
		{"why did you", KindReviewReply, "Why did you add the lock?", []string{"1:Why did you"}},
		{"why would you", KindReviewReply, "Looks close. Why would you copy the map?", []string{"1:Why would you"}},
		{"after emphasis", KindReviewReply, "**Why are you** sorting twice?", []string{"1:Why are you"}},
		{"a why about the code", KindReviewReply, "Why does the cache miss here?", nil},
		{"mid-sentence", KindReviewReply, "I see why you added the lock.", nil},
		{"quoted", KindReviewReply, "The guide says to avoid \"Why did you\" openers.", nil},
	})
}

func TestBareImperativeReportsACommandWithNoReason(t *testing.T) {
	runTextCases(t, RuleBareImperative, []textCase{
		{"fix this", KindReviewReply, "Fix this.", []string{"1:Fix this"}},
		{"please", KindReviewReply, "Please remove the test.", []string{"1:remove the test"}},
		{"no period", KindReviewReply, "Delete this test", []string{"1:Delete this test"}},
		{"a reason after", KindReviewReply, "Remove the test. It duplicates the one above, so it adds nothing.", nil},
		{"a purpose", KindReviewReply, "Remove it to reduce noise.", nil},
		{"a long command", KindReviewReply, "Move the retry into the client with the backoff it already has.", nil},
		{"a command naming code", KindReviewReply, "Use `sync.Once`.", nil},
		{"a noun", KindReviewReply, "Fix is in the next commit.", nil},
		{"a question", KindReviewReply, "Could we drop this?", nil},
	})
}

func TestAllCapsReportsWordsWrittenForEmphasis(t *testing.T) {
	runTextCases(t, RuleAllCaps, []textCase{
		{"one emphatic word", KindReviewReply, "Do NOT merge this.", []string{"1:NOT"}},
		{"a shouted run", KindReviewReply, "THIS IS WRONG.", []string{"1:THIS IS WRONG"}},
		{"acronyms", KindReviewReply, "The REST API returns JSON over HTTP.", nil},
		{"a run of acronyms", KindReviewReply, "Pin the AWS SDK version.", nil},
		{"in code", KindReviewReply, "Set `NOT_NULL` on the column.", nil},
		{"an identifier", KindReviewReply, "Read MAX_RETRIES first.", nil},
	})
}

func TestRepeatedMarksReportsARunOfMarks(t *testing.T) {
	runTextCases(t, RuleRepeatedMarks, []textCase{
		{"two questions", KindReviewReply, "Is this locked??", []string{"1:??"}},
		{"mixed", KindReviewReply, "It deletes the cache?!", []string{"1:?!"}},
		{"exclamations", KindReviewReply, "Nice catch!!!", []string{"1:!!!"}},
		{"one mark", KindReviewReply, "Is this locked?", nil},
		{"an operator in code", KindReviewReply, "Use `a ?? b` here.", nil},
	})
}
