package proofread

import (
	"reflect"
	"slices"
	"strconv"
	"testing"
)

func TestToneChecksRunInThisOrder(t *testing.T) {
	want := []Rule{
		RuleBlame, RuleVerdict, RuleAbsolute, RuleIntent, RuleCredit, RuleClaim,
		RuleReplyOpener, RuleJudgmentAsFact, RuleStackedHedge, RuleLongThread,
	}

	if got := ruleNames(toneChecks); !reflect.DeepEqual(got, want) {
		t.Errorf("toneChecks = %q, want %q", got, want)
	}
}

// graded renders each finding of rule as `line:match:decision`.
func graded(findings []Finding, rule Rule) []string {
	var out []string

	for _, f := range findings {
		if f.Rule == rule {
			out = append(out, strconv.Itoa(f.Line)+":"+f.Match+":"+f.Decision.String())
		}
	}

	return out
}

func runDecisionCases(t *testing.T, rule Rule, cases []textCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := graded(JudgeText(tc.text, tc.kind, houseOn), rule)
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s findings:\n got %q\nwant %q", rule, got, tc.want)
			}
		})
	}
}

func TestBlameReportsAPersonOrPastWorkAsTheSubjectOfAFault(t *testing.T) {
	runDecisionCases(t, RuleBlame, []textCase{
		{"a person who should have", KindChangeDescription, pr("- The author should have pinned the key."),
			[]string{"4:The author should have pinned:advise"}},
		{"a pull request that forgot to", KindChangeDescription, pr("- #341 forgot to regenerate the index."),
			[]string{"4:#341 forgot to:advise"}},
		{"you failed to in a reply", KindReviewReply, "You failed to run the suite.", []string{"1:You failed to:advise"}},
		{"whoever wrote", KindReviewReply, "Whoever wrote this left the lock out.", []string{"1:Whoever wrote:advise"}},
		// #439, the worst phrase the research found.
		{"contempt", KindChangeDescription, pr("- The old names read as sloppy."), []string{"4:sloppy:advise"}},
		{"naive said of a decision", KindReviewReply, "The retry was naive.", []string{"1:naive:advise"}},
		// #485, #400 and docs/guides/integrations/ci.md name a role or a technique.
		{"a careless caller", KindChangeDescription, pr("- Tells the careful and the careless caller the same sentence."), nil},
		{"a lazy fetch and a naive clone", KindChangeDescription, pr("- Skips the lazy fetch a naive shallow clone makes."), nil},
		{"a commit that failed to apply", KindChangeDescription, pr("- The commit failed to apply, so the rebase stops."), nil},
		{"the writer owning it", KindReviewReply, "I should have pinned the key; we forgot to.", nil},
		{"a page names its reader's mistake", KindReference, "A variable you forgot to unset leaks.", nil},
	})
}

func TestVerdictReportsAJudgmentInPlaceOfTheBehavior(t *testing.T) {
	runDecisionCases(t, RuleVerdict, []textCase{
		{"predicated", KindChangeDescription, pr("- The cache was broken and the parser is a mess."),
			[]string{"4:broken:advise", "4:mess:advise"}},
		{"hacky", KindReviewReply, "That retry is hacky.", []string{"1:hacky:advise"}},
		// #505, #373 and #369 pick out a thing.
		{"the wrong checkout", KindChangeDescription, pr("- Answered from the wrong checkout; a bad plan and a broken test fail."), nil},
		{"garbage collection", KindChangeDescription, pr("- Runs garbage collection after the build."), nil},
		{"only text to a reviewer", KindReference, "The cache was broken.", nil},
	})
}

func TestAbsoluteReportsNeverAndNobodyAboutThePast(t *testing.T) {
	runDecisionCases(t, RuleAbsolute, []textCase{
		// #401, #162 and #320.
		{"has never", KindChangeDescription, pr("- The queue has never merged a pull request."), []string{"4:has never:advise"}},
		{"never with a past verb", KindChangeDescription, pr("- Verdict inheritance never fired."), []string{"4:never fired:advise"}},
		{"nobody with a past verb", KindReviewReply, "A variable nobody listed reaches the test.", []string{"1:nobody listed:advise"}},
		{"nothing with a past verb", KindReviewReply, "Nothing checked it.", []string{"1:Nothing checked:advise"}},
		{"a contract", KindChangeDescription, pr("- Resolve never returns nil, and the value is never used."), nil},
		{"a present verb ending in ed", KindReviewReply, "Nobody need run it twice.", nil},
		{"never with a passive past verb", KindReviewReply, "It is an old line that was never updated.",
			[]string{"1:never updated:advise"}},
		{"a verb said of code", KindReviewReply, "The field was never used by the compiler.", nil},
		{"has never with a verb said of code", KindReviewReply, "The option had never been read.", nil},
		{"set against what the code did", KindReviewReply, "The results were collected but never posted.", nil},
		{"a supposed case", KindReviewReply, "If the listener never fired, the test would time out.", nil},
		{"a mechanism", KindReviewReply, "The hook never fired when the chain failed.", nil},
		{"a parenthetical note", KindReviewReply, "Drop the mapping (never shipped).", nil},
		{"the writer in the first person", KindReviewReply, "I've never tested it on Windows.", nil},
		{"nothing with no object", KindReviewReply, "Drop the flag nothing ever cleared.", nil},
		{"nothing with a preposition", KindReviewReply, "It reruns though nothing changed in the file.", nil},
		{"a quoted issue", KindReviewReply, "> We never finished it.\n\nThis lands the rest.", nil},
	})
}

func TestIntentReportsAMotiveGivenToAToolOrAPerson(t *testing.T) {
	runDecisionCases(t, RuleIntent, []textCase{
		// #275.
		{"guessed", KindChangeDescription, pr("- magus guessed where it ran."), []string{"4:guessed:advise"}},
		{"pretends and does not care", KindReviewReply, "The cache pretends to be warm and does not care about edits.",
			[]string{"1:pretends:advise", "1:does not care:advise"}},
		{"refuses to understand", KindReviewReply, "The parser refuses to understand tabs.", []string{"1:refuses to understand:advise"}},
		// #426: the guard refusing is a mechanism.
		{"refuses to as a mechanism", KindChangeDescription, pr("- An older build refuses to overwrite a newer stamp."), nil},
		{"lies in", KindReviewReply, "The cause lies in the lock.", nil},
	})
}

func TestCreditReportsARemovalThatNamesNothingItWasFor(t *testing.T) {
	const lead = "A cached replay missed whenever two workers raced on the key, so the cache key now sorts its inputs.\n\n"

	runDecisionCases(t, RuleCredit, []textCase{
		// #509.
		{"a title that removes", KindChangeDescription, "refactor(memory): remove the memory store\n" + lead + "- Moves notes.",
			[]string{"1:remove:advise"}},
		{"a later clause of the title", KindChangeDescription, "refactor!: keep notes; drop the store\n" + lead + "- Moves notes.",
			[]string{"1:drop:advise"}},
		{"a bullet that replaces", KindChangeDescription, pr("- Replaces the per-process cache with a shared one."),
			[]string{"4:Replaces:advise"}},
		{"credit given", KindChangeDescription, pr("- Replaces the per-process cache, which suited one worker, with a shared one."), nil},
		{"credit in another paragraph", KindChangeDescription,
			"refactor: remove the memory store\n" + lead + "The store was built for one session at a time.", nil},
		{"nothing removed", KindChangeDescription, pr("- Sorts the inputs."), nil},
		{"only a pull request", KindReviewReply, "Removes the store.", nil},
	})
}

func TestClaimReportsAClaimWithNoEvidenceBesideIt(t *testing.T) {
	runDecisionCases(t, RuleClaim, []textCase{
		{"a completion", KindChangeDescription, pr("- Fixes the flaky cache test."), []string{"4:Fixes:advise"}},
		{"a comparison", KindChangeDescription, pr("- Makes startup much faster."), []string{"4:faster:advise"}},
		{"a measurement", KindReviewReply, "Startup drops from 410ms to 260ms.", []string{"1:410ms:advise"}},
		{"a percentage and N of M", KindReviewReply, "It passed 50 of 50 runs. The skills shrink 11 percent.",
			[]string{"1:50 of 50:advise", "1:11 percent:advise"}},
		{"a code span", KindChangeDescription, pr("- `TestCacheEvict` passed 50 of 50 runs with `-count=50`."), nil},
		{"a link", KindReviewReply, "Startup drops to 260ms ([run](https://example.com/run/1)).", nil},
		{"a bare URL", KindReviewReply, "Startup drops to 260ms, https://example.com/run/1 has it.", nil},
		{"an issue", KindChangeDescription, pr("- Fixes the flake #512 reported."), nil},
		{"a commit", KindChangeDescription, pr("- Startup drops to 260ms since 4762d2e2d."), nil},
		{"an output ref", KindReviewReply, "Startup drops to 260ms (grd875434f41d23c346).", nil},
		{"evidence elsewhere in the bullet", KindChangeDescription, pr("- Startup drops to 260ms.\n  Measured with `hack/bench/startup.buzz`."), nil},
		{"evidence in another sentence", KindReviewReply, "Startup drops to 260ms. See `hack/bench/startup.buzz`.",
			[]string{"1:260ms:advise"}},
		{"a limit stated", KindReviewReply, "Not measured on Linux, where it may be faster.", nil},
		{"a limits section", KindChangeDescription, pr("## Not verified\n\n- Whether Linux is faster."), nil},
		{"fixes as a noun", KindChangeDescription, pr("- Adds five nesting fixes"), nil},
		{"fixes as a noun before a preposition", KindReviewReply, "I've made partial fixes to the parser.", nil},
		{"a setting after a determiner", KindReviewReply, "Wait with a deadline, not a fixed 60ms.", nil},
		{"a limit's value", KindReviewReply, "It adds another limit of 40%.", nil},
		{"a supposed size", KindReviewReply, "I'm not sure if 16 bytes is enough.", nil},
		{"a confidence", KindReviewReply, "I am 99% sure it does more.", nil},
		{"a quoted figure", KindReviewReply, "The comment says “first 12 bytes”.", nil},
		{"a ratio of counts beside it", KindReviewReply, "Trims the script: 620→213 lines (70% reduction).", nil},
		{"a comparison as a purpose", KindReviewReply, "These are split to keep the binary smaller.", nil},
		{"a comparison as an aside", KindReviewReply, "Drop the polyfill (fewer bytes).", nil},
		{"a measurement after the", KindReviewReply, "The sweep drops from 35s to 1.4s.", []string{"1:35s:advise"}},
		{"the title", KindChangeDescription, "perf: make startup 2x faster\n" +
			"Startup answers before the graph loads, which a profile of `magus ls` showed waiting on it.", nil},
		{"only text to a reviewer", KindReference, "Startup drops to 260ms.", nil},
	})
}

func TestReplyOpenerReportsAReplyThatOpensByContradicting(t *testing.T) {
	runDecisionCases(t, RuleReplyOpener, []textCase{
		{"no", KindReviewReply, "No, the map is shared.", []string{"1:No,:advise"}},
		{"as I said, mid-reply", KindReviewReply, "The map is shared. As I said, it needs a lock.", []string{"1:As I said:advise"}},
		{"wrong and again", KindReviewReply, "Wrong, it races.\n\nAgain, it races.", []string{"1:Wrong:advise", "3:Again,:advise"}},
		// filler reports it, in a reply as anywhere.
		{"actually", KindReviewReply, "Actually, it races.", nil},
		{"no without a comma", KindReviewReply, "No lock guards the map.", nil},
		{"only a reply", KindChangeDescription, pr("- Actually, it races."), nil},
	})
}

func TestJudgmentAsFactReportsARecommendationWithNoReason(t *testing.T) {
	runDecisionCases(t, RuleJudgmentAsFact, []textCase{
		{"should be", KindReviewReply, "This should be a map.", []string{"1:should be:advise"}},
		{"needs to", KindReviewReply, "The retry needs to move into the client.", []string{"1:needs to:advise"}},
		{"with a reason", KindReviewReply, "This should be a map: lookups dominate.", nil},
		{"with because", KindReviewReply, "This must be locked because two goroutines write it.", nil},
		{"labelled as a call", KindReviewReply, "I'd say this should be a map.", nil},
		{"a question", KindReviewReply, "Should this be a map?", nil},
	})
}

func TestStackedHedgeReportsSoftenersAndAnApologyFirst(t *testing.T) {
	runDecisionCases(t, RuleStackedHedge, []textCase{
		{"two softeners", KindReviewReply, "Maybe this could possibly need a lock.", []string{"1:Maybe this could:advise"}},
		{"an apology first", KindReviewReply, "Sorry if this is dumb, but the map races.", []string{"1:Sorry if:advise"}},
		{"one softener", KindReviewReply, "This might need a lock.", nil},
		{"an apology for a delay", KindReviewReply, "Sorry for the delay.", nil},
	})
}

func TestLongThreadReportsTheFourthReplyByOneAuthor(t *testing.T) {
	cases := []struct {
		name  string
		prior int
		want  []Finding
	}{
		{"third reply", 2, nil},
		{"fourth reply", 3, []Finding{{
			Rule: RuleLongThread, Decision: DecisionAdvise,
			Message: "This is reply 4 from you in the thread: offer a call to settle it, as in " +
				"'Want to talk this through for ten minutes?'",
		}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := JudgeText("The map is shared.", KindReviewReply, WithThreadLength(tc.prior), WithOnly(RuleLongThread))
			assertFindings(t, got, tc.want)
		})
	}
}

func TestCondescensionReportsPresumptionOutsideAGuide(t *testing.T) {
	runDecisionCases(t, RuleCondescension, []textCase{
		{"presuming words on a page", KindReference, "Of course the key is sorted, as everyone knows.",
			[]string{"1:Of course:deny", "1:as everyone knows:deny"}},
		{"a booster opening a sentence", KindAgentBrief, "Clearly, the key is sorted. Obviously it holds.",
			[]string{"1:Clearly:deny", "1:Obviously:deny"}},
		{"obviously anywhere in a reply", KindReviewReply, "This obviously needs a lock.", []string{"1:obviously:deny"}},
		// docs/recommendations.md and a blog post.
		{"obviously mid-sentence on a page", KindReference,
			"Two names looked obviously correct, and none is obviously the mistake.", nil},
		// docs/guides/tips.md, CONTRIBUTING.md and #560 use each in its plain sense.
		{"easy, simple and please outside a guide", KindChangeDescription,
			pr("- It is easy to miss, so a simple check runs; please open an issue."), nil},
		{"clearly as a manner", KindReference, "The doc clearly meant to cite a code.", nil},
	})
}

func TestJudgeTextReportsAReplyOnTheReplyRules(t *testing.T) {
	text := "No, I've pushed a fix. Generated with Claude.\n\n## Summary\n\n- **Cache:** sorted"

	var got []string
	for _, f := range JudgeText(text, KindReviewReply, houseOn) {
		got = append(got, string(f.Rule)+" "+strconv.Itoa(f.Line))
	}

	want := []string{"reply-voice 3", "reply-voice 5", "attribution 1", "reply-opener 1"}
	if !slices.Equal(got, want) {
		t.Errorf("findings:\n got %q\nwant %q", got, want)
	}
}

func TestHouseStyleRunsOnlyWhereATableNamesIt(t *testing.T) {
	text := pr("- I moved the cache, and it will sort the sub-agent's keys.")

	var got []Rule
	for _, f := range JudgeText(text, KindChangeDescription) {
		got = append(got, f.Rule)
	}

	if len(got) != 0 {
		t.Errorf("findings with no table: got %q, want none", got)
	}

	got = nil
	for _, f := range JudgeText(text, KindChangeDescription, houseOn) {
		got = append(got, f.Rule)
	}

	if want := []Rule{RuleTerms, RuleTense, RuleTense}; !slices.Equal(got, want) {
		t.Errorf("findings with house style on: got %q, want %q", got, want)
	}
}
