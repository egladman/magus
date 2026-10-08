package prose

import (
	"slices"
	"strconv"
	"testing"
)

// textCase judges text on one surface and keeps only the findings of rule, so
// a case sees what its own rule makes of the text.
type textCase struct {
	name    string
	surface Surface
	text    string
	want    []string
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
			got := matches(JudgeText(tc.text, tc.surface), rule)
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
		{"an opener", SurfaceMarkdown, "This change adds a flag. Here's how it works.", []string{"1:This change", "1:Here's"}},
		{"an opener after a list marker", SurfaceMarkdown, "- I've moved the cache.", []string{"1:I've"}},
		{"this before a verb names the code", SurfaceMarkdown, "This holds the key stable.", nil},
		{"a conversation", SurfaceMarkdown, "The cap is 60 words, as discussed.", []string{"1:as discussed"}},
		{"feedback addressed", SurfacePullRequest, pr("- Addresses review feedback on the cap."), []string{"4:Addresses review feedback"}},
		// docs/reference/codes/magusfile/MGS1010.md: the reader asked magus, not the author.
		{"you asked on a page", SurfaceMarkdown, "It is not the run you asked for.", nil},
		{"you asked in a pull request", SurfacePullRequest, pr("- Caps the run, as you asked."), []string{"4:you asked"}},
		{"a bold label", SurfaceMarkdown, "- **Status:** Accepted\n- **Cache**: off", []string{"1:**Status:**", "2:**Cache**:"}},
		// changes/README.md: a fragment opens with a bold headline, a sentence.
		{"a changelog headline", SurfaceMarkdown, "- **Breaking: the `exclusive` option, with no replacement.** Delete the key.", nil},
		{"bold mid-item", SurfaceMarkdown, "- Run it **twice**: once cold.", nil},
		{"a heading on a page", SurfaceMarkdown, "# Cache\n\n## Overview\n\nThe cache stores blobs.", nil},
		{"a heading in a pull request", SurfacePullRequest, pr("## Testing\n\nRan the suite."), []string{"4:Testing"}},
		{"a stock label", SurfacePullRequest, pr("**Test plan**\n\n- Ran the suite."), []string{"4:**Test plan**"}},
		{"a quoted opener", SurfaceMarkdown, `The rule refuses "This PR adds" and ` + "`Here's`.", nil},
	})
}

func TestTenseReportsTheFutureAndTheAuthor(t *testing.T) {
	runTextCases(t, RuleTense, []textCase{
		// docs/scope.md: a refusal stated as a promise.
		{"will", SurfaceMarkdown, "magus will not select a version.", []string{"1:will"}},
		{"won't and it'll", SurfaceMarkdown, "It won't retry, and it'll fail.", []string{"1:won't", "1:it'll"}},
		{"at will", SurfaceMarkdown, "Rebuild at will.", nil},
		{"the author changing code", SurfaceMarkdown, "We added a flag, and I have moved the cache.", []string{"1:We added", "1:I have moved"}},
		// docs/doctrine.md speaks as the project.
		{"the project's voice on a page", SurfaceMarkdown, "We believe a warning is a bug. Our users run it in CI.", nil},
		{"the project's voice in a pull request", SurfacePullRequest, pr("- We believe the cap holds."), nil},
		{"the author in a pull request", SurfacePullRequest, pr("- I think my cap holds."), []string{"4:I", "4:my"}},
		{"our change", SurfacePullRequest, pr("- Our fix pins the key."), []string{"4:Our fix"}},
		{"I/O is no pronoun", SurfacePullRequest, pr("- Moves the I/O off the loop."), nil},
	})
}

func TestHedgeReportsASoftenerAndLeavesPermissionAlone(t *testing.T) {
	runTextCases(t, RuleHedge, []textCase{
		{"may help", SurfaceMarkdown, "The flag may help a slow runner.", []string{"1:may help"}},
		{"might fix", SurfacePullRequest, pr("- This might fix the flake."), []string{"4:might fix"}},
		{"softeners", SurfaceMarkdown, "It aims to be fast and is arguably simpler. Hopefully it tries to cope.",
			[]string{"1:aims to", "1:arguably", "1:Hopefully", "1:tries to"}},
		{"should probably", SurfaceMarkdown, "You should probably pin it.", []string{"1:should probably"}},
		// docs/concepts/projects.md grants a permission.
		{"may granting permission", SurfaceMarkdown, "A workspace may declare its own spells.", nil},
		{"may stating a contract", SurfaceMarkdown, "The value may be empty.", nil},
		// docs/reference/codes/sandbox/MGS3013.md states a possibility a caller relies on.
		{"might stating a contract", SurfaceMarkdown, "A wait that might still end is never refused.", nil},
		{"may in a permission that names a benefit", SurfaceMarkdown, "Nothing else may make it on your behalf.", nil},
	})
}

func TestAttributionReportsCreditAndNarrativeButNotTheSubject(t *testing.T) {
	runTextCases(t, RuleAttribution, []textCase{
		{"a generated-with line", SurfacePullRequest, pr("Generated with [Claude Code](https://claude.com/claude-code)"),
			[]string{"4:Generated with [Claude"}},
		{"a trailer", SurfaceMarkdown, "Co-Authored-By: someone", []string{"1:Co-Authored-By"}},
		{"the robot", SurfacePullRequest, pr("- Pins the key. 🤖"), []string{"4:🤖"}},
		{"written with an AI", SurfaceMarkdown, "This page was written with an AI assistant.", []string{"1:written with an AI assistant"}},
		{"a conversation", SurfaceMarkdown, "As we saw earlier in this conversation, it fails.", []string{"1:earlier in this conversation"}},
		{"iterations", SurfaceMarkdown, "The cap settled after several iterations.", []string{"1:after several iterations"}},
		{"a tool name in a pull request", SurfacePullRequest, pr("- Claude found the race."), []string{"4:Claude"}},
		{"the agent as the worker in a pull request", SurfacePullRequest, pr("- The agent found the race."), []string{"4:The agent found"}},
		{"this session in a pull request", SurfacePullRequest, pr("- Measured in this session."), []string{"4:this session"}},
		// docs/guides/integrations/agents/leases.md and libs/gopherbuzz/docs/codes/BZZ2001.md.
		{"agents as the subject on a page", SurfaceMarkdown,
			"Every state was written by an agent. The agent found nothing. File imports are unavailable in this session.", nil},
		{"the harness and its paths in a pull request", SurfacePullRequest,
			pr("- Wires the Claude Code hooks under `.claude/` and spells/harness/claude-code."), nil},
		{"a subagent and a prompt", SurfacePullRequest, pr("- Hands the subagent its prompt through the hook."), nil},
	})
}

func TestFillerWidensForWrittenTextOnly(t *testing.T) {
	runTextCases(t, RuleFiller, []textCase{
		{"the wider list", SurfaceMarkdown, "It actually uses a robust, very seamless cache to leverage blobs.",
			[]string{"1:actually", "1:robust", "1:very", "1:seamless", "1:leverage"}},
		{"just meaning merely", SurfaceMarkdown, "The store is just files. Just run it.", []string{"1:just", "1:Just"}},
		{"just carrying meaning", SurfaceMarkdown,
			"It claims memory, not just cores. Read the CA you just installed. It extracts just the binary.", nil},
		{"just comparing or dating", SurfaceMarkdown, "That is just as true for commits that were just squashed.", nil},
		{"the very one", SurfaceMarkdown, "It names the very commit.", nil},
		{"a doc keeps its list", SurfaceDoc, "It actually uses a robust cache.", nil},
	})
}

func TestLeadContextReportsADescriptionWithoutAReasonFirst(t *testing.T) {
	runTextCases(t, RuleLeadContext, []textCase{
		{"a lead naming the goal", SurfacePullRequest, pr("- Sorts the inputs."), nil},
		// #525 and #520 opened with a section heading.
		{"a heading", SurfacePullRequest, "fix: x\n## Summary\n\nSorts the inputs.", []string{"2:## Summary"}},
		{"a list", SurfacePullRequest, "fix: x\n- Sorts the inputs.", []string{"2:- Sorts the inputs."}},
		{"code", SurfacePullRequest, "fix: x\n```sh\nmagus run\n```", []string{"2:```sh"}},
		{"an opener", SurfacePullRequest, "fix: x\n\nThis PR sorts the inputs before hashing, because two workers raced on the key.",
			[]string{"3:This PR"}},
		// #514, the shortest lead among the 60 measured, carries its reason in 14 words.
		{"a short lead", SurfacePullRequest, "fix: x\nSorts the inputs before hashing.", []string{"2:"}},
		{"no description", SurfacePullRequest, "fix: x\n\n", []string{"0:"}},
		{"a page has no lead", SurfaceMarkdown, "# Cache\n\n- one", nil},
	})
}

func TestLeadContextOwnsTheLeadsOpenerAndHeading(t *testing.T) {
	got := JudgeText("fix: x\n## Summary\n\nThis PR sorts the inputs.\n\n## Testing", SurfacePullRequest)

	want := []string{"lead-context 2", "reply-voice 4", "reply-voice 6"}

	var rules []string
	for _, f := range got {
		rules = append(rules, string(f.Rule)+" "+strconv.Itoa(f.Line))
	}

	if !slices.Equal(rules, want) {
		t.Errorf("findings:\n got %q\nwant %q", rules, want)
	}
}
