package prose

import (
	"slices"
	"strconv"
	"testing"
)

// textCase judges text of one kind and keeps only the findings of rule, so
// a case sees what its own rule makes of the text.
type textCase struct {
	name string
	kind Kind
	text string
	want []string
}

// matches renders each finding as `line:match`, the two facts a case pins.
func matches(findings []Finding, rule Rule) []string {
	var out []string

	for _, f := range findings {
		if f.Rule == rule {
			out = append(out, strconv.Itoa(f.Line)+":"+f.Match)
		}
	}

	return out
}

func runTextCases(t *testing.T, rule Rule, cases []textCase) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := matches(JudgeText(tc.text, tc.kind), rule)
			if !slices.Equal(got, tc.want) {
				t.Errorf("%s findings:\n got %q\nwant %q", rule, got, tc.want)
			}
		})
	}
}

// pr is a pull request with a title and a lead long enough to pass
// lead-context, followed by body.
func pr(body string) string {
	return "fix(cache): keep the key stable\n" +
		"A cached replay missed whenever two workers raced on the key, so the cache key now " +
		"sorts its inputs before hashing.\n\n" + body
}

func TestReplyVoiceReportsTextAnsweringAnUnseenPrompt(t *testing.T) {
	runTextCases(t, RuleReplyVoice, []textCase{
		{"an opener", KindMarkdown, "This change adds a flag. Here's how it works.", []string{"1:This change", "1:Here's"}},
		{"an opener after a list marker", KindMarkdown, "- I've moved the cache.", []string{"1:I've"}},
		{"this before a verb names the code", KindMarkdown, "This holds the key stable.", nil},
		{"a conversation", KindMarkdown, "The cap is 60 words, as discussed.", []string{"1:as discussed"}},
		{"feedback addressed", KindPullRequest, pr("- Addresses review feedback on the cap."), []string{"4:Addresses review feedback"}},
		// docs/reference/codes/magusfile/MGS1010.md: the reader asked magus, not the author.
		{"you asked on a page", KindMarkdown, "It is not the run you asked for.", nil},
		{"you asked in a pull request", KindPullRequest, pr("- Caps the run, as you asked."), []string{"4:you asked"}},
		{"a bold label", KindMarkdown, "- **Status:** Accepted\n- **Cache**: off", []string{"1:**Status:**", "2:**Cache**:"}},
		// changes/README.md: a fragment opens with a bold headline, a sentence.
		{"a changelog headline", KindMarkdown, "- **Breaking: the `exclusive` option, with no replacement.** Delete the key.", nil},
		{"bold mid-item", KindMarkdown, "- Run it **twice**: once cold.", nil},
		{"a heading on a page", KindMarkdown, "# Cache\n\n## Overview\n\nThe cache stores blobs.", nil},
		{"a heading in a pull request", KindPullRequest, pr("## Testing\n\nRan the suite."), []string{"4:Testing"}},
		{"a stock label", KindPullRequest, pr("**Test plan**\n\n- Ran the suite."), []string{"4:**Test plan**"}},
		{"a quoted opener", KindMarkdown, `The rule refuses "This PR adds" and ` + "`Here's`.", nil},
	})
}

func TestTenseReportsTheFutureAndTheAuthor(t *testing.T) {
	runTextCases(t, RuleTense, []textCase{
		// docs/scope.md: a refusal stated as a promise.
		{"will", KindMarkdown, "magus will not select a version.", []string{"1:will"}},
		{"won't and it'll", KindMarkdown, "It won't retry, and it'll fail.", []string{"1:won't", "1:it'll"}},
		{"at will", KindMarkdown, "Rebuild at will.", nil},
		{"the author changing code", KindMarkdown, "We added a flag, and I have moved the cache.", []string{"1:We added", "1:I have moved"}},
		// docs/doctrine.md speaks as the project.
		{"the project's voice on a page", KindMarkdown, "We believe a warning is a bug. Our users run it in CI.", nil},
		{"the project's voice in a pull request", KindPullRequest, pr("- We believe the cap holds."), nil},
		{"the author in a pull request", KindPullRequest, pr("- I think my cap holds."), []string{"4:I", "4:my"}},
		{"our change", KindPullRequest, pr("- Our fix pins the key."), []string{"4:Our fix"}},
		{"I/O is no pronoun", KindPullRequest, pr("- Moves the I/O off the loop."), nil},
	})
}

func TestHedgeReportsASoftenerAndLeavesPermissionAlone(t *testing.T) {
	runTextCases(t, RuleHedge, []textCase{
		{"may help", KindMarkdown, "The flag may help a slow runner.", []string{"1:may help"}},
		{"might fix", KindPullRequest, pr("- This might fix the flake."), []string{"4:might fix"}},
		{"softeners", KindMarkdown, "It aims to be fast and is arguably simpler. Hopefully it tries to cope.",
			[]string{"1:aims to", "1:arguably", "1:Hopefully", "1:tries to"}},
		{"should probably", KindMarkdown, "You should probably pin it.", []string{"1:should probably"}},
		// docs/concepts/projects.md grants a permission.
		{"may granting permission", KindMarkdown, "A workspace may declare its own spells.", nil},
		{"may stating a contract", KindMarkdown, "The value may be empty.", nil},
		// docs/reference/codes/sandbox/MGS3013.md states a possibility a caller relies on.
		{"might stating a contract", KindMarkdown, "A wait that might still end is never refused.", nil},
		{"may in a permission that names a benefit", KindMarkdown, "Nothing else may make it on your behalf.", nil},
	})
}

func TestAttributionReportsCreditAndNarrativeButNotTheSubject(t *testing.T) {
	runTextCases(t, RuleAttribution, []textCase{
		{"a generated-with line", KindPullRequest, pr("Generated with [Claude Code](https://claude.com/claude-code)"),
			[]string{"4:Generated with [Claude"}},
		{"a trailer", KindMarkdown, "Co-Authored-By: someone", []string{"1:Co-Authored-By"}},
		{"the robot", KindPullRequest, pr("- Pins the key. 🤖"), []string{"4:🤖"}},
		{"written with an AI", KindMarkdown, "This page was written with an AI assistant.", []string{"1:written with an AI assistant"}},
		{"a conversation", KindMarkdown, "As we saw earlier in this conversation, it fails.", []string{"1:earlier in this conversation"}},
		{"iterations", KindMarkdown, "The cap settled after several iterations.", []string{"1:after several iterations"}},
		{"a tool name in a pull request", KindPullRequest, pr("- Claude found the race."), []string{"4:Claude"}},
		{"the agent as the worker in a pull request", KindPullRequest, pr("- The agent found the race."), []string{"4:The agent found"}},
		{"this session in a pull request", KindPullRequest, pr("- Measured in this session."), []string{"4:this session"}},
		// docs/guides/integrations/agents/leases.md and libs/gopherbuzz/docs/codes/BZZ2001.md.
		{"agents as the subject on a page", KindMarkdown,
			"Every state was written by an agent. The agent found nothing. File imports are unavailable in this session.", nil},
		{"the harness and its paths in a pull request", KindPullRequest,
			pr("- Wires the Claude Code hooks under `.claude/` and spells/harness/claude-code."), nil},
		{"a subagent and a prompt", KindPullRequest, pr("- Hands the subagent its prompt through the hook."), nil},
	})
}

func TestFillerWidensForWrittenTextOnly(t *testing.T) {
	runTextCases(t, RuleFiller, []textCase{
		{"the wider list", KindMarkdown, "It actually uses a robust, very seamless cache to leverage blobs.",
			[]string{"1:actually", "1:robust", "1:very", "1:seamless", "1:leverage"}},
		{"just meaning merely", KindMarkdown, "The store is just files. Just run it.", []string{"1:just", "1:Just"}},
		{"just carrying meaning", KindMarkdown,
			"It claims memory, not just cores. Read the CA you just installed. It extracts just the binary.", nil},
		{"just comparing or dating", KindMarkdown, "That is just as true for commits that were just squashed.", nil},
		{"the very one", KindMarkdown, "It names the very commit.", nil},
		{"a doc keeps its list", KindDoc, "It actually uses a robust cache.", nil},
	})
}

func TestLeadContextReportsADescriptionWithoutAReasonFirst(t *testing.T) {
	runTextCases(t, RuleLeadContext, []textCase{
		{"a lead naming the goal", KindPullRequest, pr("- Sorts the inputs."), nil},
		// #525 and #520 opened with a section heading.
		{"a heading", KindPullRequest, "fix: x\n## Summary\n\nSorts the inputs.", []string{"2:## Summary"}},
		{"a list", KindPullRequest, "fix: x\n- Sorts the inputs.", []string{"2:- Sorts the inputs."}},
		{"code", KindPullRequest, "fix: x\n```sh\nmagus run\n```", []string{"2:```sh"}},
		{"an opener", KindPullRequest, "fix: x\n\nThis PR sorts the inputs before hashing, because two workers raced on the key.",
			[]string{"3:This PR"}},
		// #514, the shortest lead among the 60 measured, carries its reason in 14 words.
		{"a short lead", KindPullRequest, "fix: x\nSorts the inputs before hashing.", []string{"2:"}},
		{"no description", KindPullRequest, "fix: x\n\n", []string{"0:"}},
		{"a page has no lead", KindMarkdown, "# Cache\n\n- one", nil},
	})
}

func TestLeadContextOwnsTheLeadsOpenerAndHeading(t *testing.T) {
	got := JudgeText("fix: x\n## Summary\n\nThis PR sorts the inputs.\n\n## Testing", KindPullRequest)

	want := []string{"lead-context 2", "reply-voice 4", "reply-voice 6"}

	var rules []string
	for _, f := range got {
		rules = append(rules, string(f.Rule)+" "+strconv.Itoa(f.Line))
	}

	if !slices.Equal(rules, want) {
		t.Errorf("findings:\n got %q\nwant %q", rules, want)
	}
}
