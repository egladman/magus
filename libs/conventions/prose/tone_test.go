package prose

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

// graded renders each finding of rule as `line:match:severity`.
func graded(findings []Finding, rule Rule) []string {
	var out []string

	for _, f := range findings {
		if f.Rule == rule {
			out = append(out, strconv.Itoa(f.Line)+":"+f.Match+":"+string(f.Severity))
		}
	}

	return out
}

func runSeverityCases(t *testing.T, rule Rule, cases []textCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := graded(JudgeText(tc.text, tc.kind), rule)
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s findings:\n got %q\nwant %q", rule, got, tc.want)
			}
		})
	}
}

func TestBlameReportsAPersonOrPastWorkAsTheSubjectOfAFault(t *testing.T) {
	runSeverityCases(t, RuleBlame, []textCase{
		{"a person who should have", KindPullRequest, pr("- The author should have pinned the key."),
			[]string{"4:The author should have:error"}},
		{"a pull request that forgot to", KindPullRequest, pr("- #341 forgot to regenerate the index."),
			[]string{"4:#341 forgot to:error"}},
		{"you failed to in a reply", KindReply, "You failed to run the suite.", []string{"1:You failed to:error"}},
		{"whoever wrote", KindReply, "Whoever wrote this left the lock out.", []string{"1:Whoever wrote:error"}},
		// #439, the worst phrase the research found.
		{"contempt", KindPullRequest, pr("- The old names read as sloppy."), []string{"4:sloppy:error"}},
		{"naive said of a decision", KindReply, "The retry was naive.", []string{"1:naive:error"}},
		// #485, #400 and docs/guides/integrations/ci.md name a role or a technique.
		{"a careless caller", KindPullRequest, pr("- Tells the careful and the careless caller the same sentence."), nil},
		{"a lazy fetch and a naive clone", KindPullRequest, pr("- Skips the lazy fetch a naive shallow clone makes."), nil},
		{"a commit that failed to apply", KindPullRequest, pr("- The commit failed to apply, so the rebase stops."), nil},
		{"the writer owning it", KindReply, "I should have pinned the key; we forgot to.", nil},
		{"a page names its reader's mistake", KindMarkdown, "A variable you forgot to unset leaks.", nil},
	})
}

func TestVerdictReportsAJudgmentInPlaceOfTheBehavior(t *testing.T) {
	runSeverityCases(t, RuleVerdict, []textCase{
		{"predicated", KindPullRequest, pr("- The cache was broken and the parser is a mess."),
			[]string{"4:broken:advisory", "4:mess:advisory"}},
		{"hacky", KindReply, "That retry is hacky.", []string{"1:hacky:advisory"}},
		// #505, #373 and #369 pick out a thing.
		{"the wrong checkout", KindPullRequest, pr("- Answered from the wrong checkout; a bad plan and a broken test fail."), nil},
		{"garbage collection", KindPullRequest, pr("- Runs garbage collection after the build."), nil},
		{"only text to a teammate", KindMarkdown, "The cache was broken.", nil},
	})
}

func TestAbsoluteReportsNeverAndNobodyAboutThePast(t *testing.T) {
	runSeverityCases(t, RuleAbsolute, []textCase{
		// #401, #162 and #320.
		{"has never", KindPullRequest, pr("- The queue has never merged a pull request."), []string{"4:has never:advisory"}},
		{"never with a past verb", KindPullRequest, pr("- Verdict inheritance never fired."), []string{"4:never fired:advisory"}},
		{"nobody with a past verb", KindReply, "A variable nobody listed reaches the test.", []string{"1:nobody listed:advisory"}},
		{"nothing with a past verb", KindReply, "Nothing checked it.", []string{"1:Nothing checked:advisory"}},
		{"a contract", KindPullRequest, pr("- Resolve never returns nil, and the value is never used."), nil},
		{"a present verb ending in ed", KindReply, "Nobody need run it twice.", nil},
	})
}

func TestIntentReportsAMotiveGivenToAToolOrAPerson(t *testing.T) {
	runSeverityCases(t, RuleIntent, []textCase{
		// #275.
		{"guessed", KindPullRequest, pr("- magus guessed where it ran."), []string{"4:guessed:advisory"}},
		{"pretends and does not care", KindReply, "The cache pretends to be warm and does not care about edits.",
			[]string{"1:pretends:advisory", "1:does not care:advisory"}},
		{"refuses to understand", KindReply, "The parser refuses to understand tabs.", []string{"1:refuses to understand:advisory"}},
		// #426: the guard refusing is a mechanism.
		{"refuses to as a mechanism", KindPullRequest, pr("- An older build refuses to overwrite a newer stamp."), nil},
		{"lies in", KindReply, "The cause lies in the lock.", nil},
	})
}

func TestCreditReportsARemovalThatNamesNothingItWasFor(t *testing.T) {
	const lead = "A cached replay missed whenever two workers raced on the key, so the cache key now sorts its inputs.\n\n"

	runSeverityCases(t, RuleCredit, []textCase{
		// #509.
		{"a title that removes", KindPullRequest, "refactor(memory): remove the memory store\n" + lead + "- Moves notes.",
			[]string{"1:remove:advisory"}},
		{"a later clause of the title", KindPullRequest, "refactor!: keep notes; drop the store\n" + lead + "- Moves notes.",
			[]string{"1:drop:advisory"}},
		{"a bullet that replaces", KindPullRequest, pr("- Replaces the per-process cache with a shared one."),
			[]string{"4:Replaces:advisory"}},
		{"credit given", KindPullRequest, pr("- Replaces the per-process cache, which suited one worker, with a shared one."), nil},
		{"credit in another paragraph", KindPullRequest,
			"refactor: remove the memory store\n" + lead + "The store was built for one session at a time.", nil},
		{"nothing removed", KindPullRequest, pr("- Sorts the inputs."), nil},
		{"only a pull request", KindReply, "Removes the store.", nil},
	})
}

func TestClaimReportsAClaimWithNoEvidenceBesideIt(t *testing.T) {
	runSeverityCases(t, RuleClaim, []textCase{
		{"a completion", KindPullRequest, pr("- Fixes the flaky cache test."), []string{"4:Fixes:advisory"}},
		{"a comparison", KindPullRequest, pr("- Makes startup much faster."), []string{"4:faster:advisory"}},
		{"a measurement", KindReply, "Startup drops from 410ms to 260ms.", []string{"1:410ms:advisory"}},
		{"a percentage and N of M", KindReply, "It passed 50 of 50 runs. The skills shrink 11 percent.",
			[]string{"1:50 of 50:advisory", "1:11 percent:advisory"}},
		{"a code span", KindPullRequest, pr("- `TestCacheEvict` passed 50 of 50 runs with `-count=50`."), nil},
		{"a link", KindReply, "Startup drops to 260ms ([run](https://example.com/run/1)).", nil},
		{"a bare URL", KindReply, "Startup drops to 260ms, https://example.com/run/1 has it.", nil},
		{"an issue", KindPullRequest, pr("- Fixes the flake #512 reported."), nil},
		{"a commit", KindPullRequest, pr("- Startup drops to 260ms since 4762d2e2d."), nil},
		{"an output ref", KindReply, "Startup drops to 260ms (grd875434f41d23c346).", nil},
		{"evidence elsewhere in the bullet", KindPullRequest, pr("- Startup drops to 260ms.\n  Measured with `hack/bench/startup.buzz`."), nil},
		{"evidence in another sentence", KindReply, "Startup drops to 260ms. See `hack/bench/startup.buzz`.",
			[]string{"1:260ms:advisory"}},
		{"a limit stated", KindReply, "Not measured on Linux, where it may be faster.", nil},
		{"a limits section", KindPullRequest, pr("## Not verified\n\n- Whether Linux is faster."), nil},
		{"fixes as a noun", KindPullRequest, pr("- Adds five nesting fixes"), nil},
		{"the title", KindPullRequest, "perf: make startup 2x faster\n" +
			"Startup answers before the graph loads, which a profile of `magus ls` showed waiting on it.", nil},
		{"only text to a teammate", KindMarkdown, "Startup drops to 260ms.", nil},
	})
}

func TestReplyOpenerReportsAReplyThatOpensByContradicting(t *testing.T) {
	runSeverityCases(t, RuleReplyOpener, []textCase{
		{"no", KindReply, "No, the map is shared.", []string{"1:No,:error"}},
		{"as I said, mid-reply", KindReply, "The map is shared. As I said, it needs a lock.", []string{"1:As I said:error"}},
		{"wrong and again", KindReply, "Wrong, it races.\n\nAgain, it races.", []string{"1:Wrong:error", "3:Again,:error"}},
		// filler reports it, in a reply as anywhere.
		{"actually", KindReply, "Actually, it races.", nil},
		{"no without a comma", KindReply, "No lock guards the map.", nil},
		{"only a reply", KindPullRequest, pr("- Actually, it races."), nil},
	})
}

func TestJudgmentAsFactReportsARecommendationWithNoReason(t *testing.T) {
	runSeverityCases(t, RuleJudgmentAsFact, []textCase{
		{"should be", KindReply, "This should be a map.", []string{"1:should be:advisory"}},
		{"needs to", KindReply, "The retry needs to move into the client.", []string{"1:needs to:advisory"}},
		{"with a reason", KindReply, "This should be a map: lookups dominate.", nil},
		{"with because", KindReply, "This must be locked because two goroutines write it.", nil},
		{"labelled as a call", KindReply, "I'd say this should be a map.", nil},
		{"a question", KindReply, "Should this be a map?", nil},
	})
}

func TestStackedHedgeReportsSoftenersAndAnApologyFirst(t *testing.T) {
	runSeverityCases(t, RuleStackedHedge, []textCase{
		{"two softeners", KindReply, "Maybe this could possibly need a lock.", []string{"1:Maybe this could:advisory"}},
		{"an apology first", KindReply, "Sorry if this is dumb, but the map races.", []string{"1:Sorry if:advisory"}},
		{"one softener", KindReply, "This might need a lock.", nil},
		{"an apology for a delay", KindReply, "Sorry for the delay.", nil},
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
			Rule: RuleLongThread, Severity: SeverityAdvisory,
			Message: "This is reply 4 from you in the thread: offer a call to settle it, as in " +
				"'Want to talk this through for ten minutes?'",
		}}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := JudgeText("The map is shared.", KindReply, WithThreadLength(tc.prior), WithOnly(RuleLongThread))
			assertFindings(t, got, tc.want)
		})
	}
}

func TestCondescensionReportsPresumptionOutsideAGuide(t *testing.T) {
	runSeverityCases(t, RuleCondescension, []textCase{
		{"presuming words on a page", KindMarkdown, "Of course the key is sorted, as everyone knows.",
			[]string{"1:Of course:error", "1:as everyone knows:error"}},
		{"a booster opening a sentence", KindSkill, "Clearly, the key is sorted. Obviously it holds.",
			[]string{"1:Clearly:error", "1:Obviously:error"}},
		{"obviously anywhere in a reply", KindReply, "This obviously needs a lock.", []string{"1:obviously:error"}},
		// docs/recommendations.md and a blog post.
		{"obviously mid-sentence on a page", KindMarkdown,
			"Two names looked obviously correct, and none is obviously the mistake.", nil},
		// docs/guides/tips.md, CONTRIBUTING.md and #560 use each in its plain sense.
		{"easy, simple and please outside a guide", KindPullRequest,
			pr("- It is easy to miss, so a simple check runs; please open an issue."), nil},
		{"clearly as a manner", KindMarkdown, "The doc clearly meant to cite a code.", nil},
	})
}

func TestJudgeTextReportsAReplyOnTheReplyRules(t *testing.T) {
	text := "No, I've pushed a fix. Generated with Claude.\n\n## Summary\n\n- **Cache:** sorted"

	var got []string
	for _, f := range JudgeText(text, KindReply) {
		got = append(got, string(f.Rule)+" "+strconv.Itoa(f.Line))
	}

	want := []string{"reply-voice 3", "reply-voice 5", "attribution 1", "reply-opener 1"}
	if !slices.Equal(got, want) {
		t.Errorf("findings:\n got %q\nwant %q", got, want)
	}
}

func TestCollaborativeProfileLeavesHouseStyleOut(t *testing.T) {
	text := pr("- I moved the cache, and it will sort the sub-agent's keys.")

	var got []Rule
	for _, f := range JudgeText(text, KindPullRequest, WithProfile(ProfileCollaborative)) {
		got = append(got, f.Rule)
	}

	if len(got) != 0 {
		t.Errorf("collaborative findings: got %q, want none", got)
	}

	if plain := JudgeText(text, KindPullRequest); len(plain) == 0 {
		t.Error("plain findings: got none, want tense and terms")
	}
}
