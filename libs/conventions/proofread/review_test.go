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

// An inline comment's line is its referent, so an anchored reply is not
// judged; the other reply rules still are.
func TestNonspecificJudgesNoAnchoredReply(t *testing.T) {
	const reply = "This is confusing. Do NOT merge."

	only := WithOnly(RuleNonspecific, RuleAllCaps)

	var got []Rule

	for _, f := range JudgeText(reply, KindReviewReply, only, WithAnchored()) {
		got = append(got, f.Rule)
	}

	if want := []Rule{RuleAllCaps}; !reflect.DeepEqual(got, want) {
		t.Errorf("anchored findings = %q, want %q", got, want)
	}

	got = nil

	for _, f := range JudgeText(reply, KindReviewReply, only) {
		got = append(got, f.Rule)
	}

	if want := []Rule{RuleNonspecific, RuleAllCaps}; !reflect.DeepEqual(got, want) {
		t.Errorf("top-level findings = %q, want %q", got, want)
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
		{"a bare verdict on it", KindReviewReply, "Drop this, it's redundant", []string{"1:redundant"}},
		{"a bare request", KindReviewReply, "Do not change this", []string{"1:change this"}},
		{"a bit of a verdict", KindReviewReply, "This is a bit confusing.", []string{"1:confusing"}},
		{"a named subject", KindReviewReply, "The indentation is inconsistent.", nil},
		{"a demonstrative and its noun", KindReviewReply, "This logic is a bit confusing.", nil},
		{"the noun it modifies", KindReviewReply, "Removed unnecessary curly braces.", nil},
		{"a request naming its object", KindReviewReply, "please fix this comment", nil},
		{"a clause naming what is odd", KindReviewReply, "ok, strange that we keep two IDs.", nil},
	})
}

// A failure the reader can go and find is the reason for the change asked.
func TestNonspecificTakesAFindableSymptomAsTheReason(t *testing.T) {
	runTextCases(t, RuleNonspecific, []textCase{
		{"failing tests", KindReviewReply, "some tests are failing, please fix it", nil},
		{"a failed CI run", KindReviewReply, "CI failed. Fix this.", nil},
		{"failed jobs", KindReviewReply, "look at the failed jobs and fix it", nil},
		{"a quoted command", KindReviewReply, "'make dist' is failing, fix it", nil},
		{"a timeout", KindReviewReply, "the tests are timing out. Fix that.", nil},
		{"in the next sentence", KindReviewReply, "the tests are not good.They keep failing", nil},
		{"a compile error", KindReviewReply, "This does not compile. Fix this.", nil},
		{"a failure naming nothing", KindReviewReply, "It failed again, fix it", []string{"1:fix it"}},
		{"nothing works", KindReviewReply, "This doesn't work.", []string{"1:doesn't work"}},
		{"a retry naming nothing", KindReviewReply, "seems like something went wrong, please try again",
			[]string{"1:wrong"}},
	})
}

// A block quote is the comment a reply answers, so it is the reply's referent
// and none of its words are the writer's; a judgment in single quotes is
// mentioned, not made.
func TestNonspecificJudgesOnlyTheWritersOwnWords(t *testing.T) {
	runTextCases(t, RuleNonspecific, []textCase{
		{"a judgment in the quote", KindReviewReply,
			"> This looks wrong to me.\n\nMoved the retry behind a backoff.", nil},
		{"a request after the quote", KindReviewReply,
			"> Each handler opens a pool of its own.\n\nPlease fix this.", nil},
		{"a verdict on the quote", KindReviewReply, "> pinned the linter to the old release\n\nThat's weird.", nil},
		{"an earlier reply quoted", KindReviewReply,
			"On Monday the reviewer wrote:\n> fix it...\n\nDone, the lockfile came from the base branch.", nil},
		{"words in single quotes", KindReviewReply, "You said 'it is wrong here' and I agree.", nil},
		{"the same words unquoted", KindReviewReply, "You said it is wrong here and I agree.", []string{"1:wrong"}},
		{"no quote at all", KindReviewReply, "Please fix this.", []string{"1:fix this"}},
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
