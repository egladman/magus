package proofread

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The inputs are real text from this repository, kept as .txt so the
// Markdown lint leaves them alone.
var benchInputs = []struct {
	file string
	kind Kind
}{
	{"change-description.txt", KindChangeDescription},
	{"commit-message.txt", KindCommitMessage},
	{"review-reply.txt", KindReviewReply},
	{"reference.txt", KindReference},
}

func BenchmarkJudgeText(b *testing.B) {
	for _, in := range benchInputs {
		text, err := os.ReadFile(filepath.Join("testdata", "bench", in.file))
		if err != nil {
			b.Fatal(err)
		}

		b.Run(string(in.kind), func(b *testing.B) {
			b.SetBytes(int64(len(text)))
			b.ReportAllocs()

			for b.Loop() {
				JudgeText(string(text), in.kind)
			}
		})
	}
}

// Every line below that is not prose carries a word the filler rule refuses,
// so a line the reader fails to skip shows up as a finding.
const markdownPage = `---
title: Simply a page
---

# Cache

<!-- simply a comment
   that runs on -->
The cache simply stores blobs, keyed by ` + "`simply`" + `.

` + "```sh" + `
magus simply runs
` + "```" + `

    simply indented code

| Flag | Meaning |
| ---- | ------- |
| -x   | simply  |

Flag | Meaning
---- | -------
-x   | simply

[a link](https://example.com/simply) and <https://simply.example.com> and https://simply.example.com/x.

[simply]: https://example.com/simply

> A quote simply quoted.

- An item
  that simply wraps.`

func TestJudgeTextSkipsWhatIsNotProse(t *testing.T) {
	want := []Finding{
		{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 9},
		{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 29},
		{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 32},
	}

	assertFindings(t, JudgeText(markdownPage, KindReference, houseOn), want)
}

// A page's description is the sentence a search result shows, so it is prose; every
// other front matter key, and the YAML quotes around the value, are not.
func TestJudgeTextJudgesTheFrontMatterDescription(t *testing.T) {
	page := "---\ntitle: Simply a page\ndescription: \"How the cache simply stores blobs: by key.\"\n" +
		"tags: [simply]\n---\n\n# Cache\n"

	assertFindings(t, JudgeText(page, KindReference, houseOn),
		[]Finding{{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 3}})
}

func TestJudgeTextReadsAPullRequestTitleAsItsFirstLine(t *testing.T) {
	got := JudgeText("fix: simply pin the key\nPinning the key keeps two racing workers from writing "+
		"different blobs, so the cache now sorts its inputs.", KindChangeDescription, houseOn)

	assertFindings(t, got, []Finding{{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 1}})
}

// TestJudgeTextOnADocMatchesJudge pins that the doc kind reads text the way
// Judge reads a doc, with the line it found each span on.
func TestJudgeTextOnADocMatchesJudge(t *testing.T) {
	doc := "Resolve returns the path.\n\nIt simply\nrereads a sub-agent - basically twice."

	got := JudgeText(doc, KindDocComment, houseOn)
	want := []Finding{
		{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 3},
		{Rule: RuleFiller, Message: "Drop 'basically': state the fact.", Match: "basically", Line: 4},
		{Rule: RuleTerms, Message: "Write 'subagent', not 'sub-agent'.", Match: "sub-agent", Line: 4},
		{Rule: RuleAside, Message: asideMessage, Match: "sub-agent - basically", Line: 4},
	}

	assertFindings(t, got, want)

	for i := range want {
		want[i].Line = 0
	}

	assertFindings(t, Judge(Symbol{Name: "Resolve", Doc: doc}, houseOn), want)
}

// changes/unreleased/on-actions.md wraps a command's code span across a line
// ending, which CommonMark reads as one span.
func TestJudgeTextReadsACodeSpanWrappedAcrossLines(t *testing.T) {
	cases := []struct {
		name string
		text string
		want []int
	}{
		{"two lines of a list item", "- Run `magus simply\n  runs -- ci` and it simply works.", []int{2}},
		{"three lines", "Run `magus\nsimply\nruns` now.", nil},
		{"a line that only closes the span", "Run `magus simply\n` and it simply works.", []int{2}},
		{"a span left open at the paragraph's end", "A stray ` here\n\nIt simply works.", []int{3}},
		{"a list item ends the paragraph", "- Run `magus\n- It simply works.`", []int{2}},
		{"a fence is not a span", "```\nsimply `\n```\nIt simply works.", []int{4}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []int

			for _, f := range JudgeText(tc.text, KindReference, WithOnly(RuleFiller)) {
				got = append(got, f.Line)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("filler lines: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestJudgeTextReadsTheTitleOfACommitAndAnIssueAsAParagraphOfItsOwn(t *testing.T) {
	const title = "perf: drop the cold start from 410ms to 260ms"

	for _, kind := range []Kind{KindCommitMessage, KindIssue, KindChangeDescription} {
		t.Run(string(kind), func(t *testing.T) {
			got := JudgeText(title+"\nCold start drops from 410ms to 260ms.", kind, WithOnly(RuleClaim))

			var lines []int
			for _, f := range got {
				lines = append(lines, f.Line)
			}

			if !slices.Equal(lines, []int{2}) {
				t.Errorf("claim lines: got %v, want only the body's: a title states a change, not evidence", lines)
			}
		})
	}
}

func TestCLIHelpIsPlainTextWithNoUrlsAndNoMarkdown(t *testing.T) {
	cases := []struct {
		name, text string
		want       []int
	}{
		{"a url is not prose", "Read https://example.com/simply for the flags", nil},
		{"a code span is a literal", "Pass `simply` to the flag", nil},
		{"a word outside both", "Simply pass the flag", []int{1}},
		{"a second line", "Pass the flag.\nIt simply works.", []int{2}},
		{"a link target is not stripped as Markdown", "See [the docs](simply)", []int{1}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []int
			for _, f := range JudgeText(tc.text, KindCLIHelp, WithOnly(RuleFiller)) {
				got = append(got, f.Line)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("filler lines: got %v, want %v", got, tc.want)
			}
		})
	}
}

// Each new kind meets the word rules that read another person's words, and
// not the rules whose measured precision belongs to a pull request.
func TestNewKindsMeetTheRulesThatFitThem(t *testing.T) {
	cases := []struct {
		kind    Kind
		in, out []Rule
	}{
		{
			KindCommitMessage,
			[]Rule{RuleFiller, RuleHedge, RuleWordy, RuleBuzzword, RuleBlame, RuleAbsolute, RuleClaim, RuleVerdict,
				RuleIntent, RuleSubjectMood, RuleSubjectLength, RuleSubjectPeriod, RuleBodySeparator},
			[]Rule{RuleLeadContext, RuleCredit, RuleStaccato, RuleHeadingCase, RuleChangelogGroup, RuleHelpSentence},
		},
		{
			KindIssue,
			[]Rule{RuleFiller, RuleHedge, RuleBuzzword, RuleBlame, RuleClaim, RuleStaccato, RuleHeadingCase,
				RuleIssueRepro},
			[]Rule{RuleLeadContext, RuleCredit, RuleSubjectMood, RuleChangelogHeading},
		},
		{
			KindReleaseNotes,
			[]Rule{RuleFiller, RuleHedge, RuleBuzzword, RuleStaccato, RuleChangelogGroup, RuleChangelogEntry},
			[]Rule{RuleLeadContext, RuleBlame, RuleClaim, RuleChangelogHeading, RuleSubjectMood},
		},
		{
			KindChangelog,
			[]Rule{RuleFiller, RuleHedge, RuleBuzzword, RuleChangelogHeading, RuleChangelogGroup, RuleChangelogEntry},
			[]Rule{RuleLeadContext, RuleBlame, RuleStaccato, RuleHeadingCase, RuleSubjectMood},
		},
	}

	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			got := KindRules(tc.kind)

			for _, r := range tc.in {
				if !slices.Contains(got, r) {
					t.Errorf("%s does not judge %s", r, tc.kind)
				}
			}

			for _, r := range tc.out {
				if slices.Contains(got, r) {
					t.Errorf("%s judges %s", r, tc.kind)
				}
			}
		})
	}
}

func TestHeadingMarker(t *testing.T) {
	cases := map[string]int{"# Cache": 2, "###  Cache": 5, "#": 1, "#hashtag": 0, "####### seven": 0, "text": 0}

	for in, want := range cases {
		if got := headingMarker(in); got != want {
			t.Errorf("headingMarker(%q) = %d, want %d", in, got, want)
		}
	}
}

// The footer and summaries a platform appends, as the AIDev export shows them,
// and an open template checkbox. Each carries a word filler or attribution
// refuses; only lines 6, 7 and 31 are the author's, an open to-do among them.
const pullRequestWithBoilerplate = "fix: keep the key stable\n" +
	"The key sorts its inputs before hashing.\n" +
	"\n" +
	"- [ ] Have you simply run the tests?\n" +
	"  It simply wraps.\n" +
	"- [ ] Rerun the flaky test, simply to be sure.\n" +
	"- The worker simply waits.\n" +
	"\n" +
	"> [!WARNING]\n" +
	">\n" +
	"> <details>\n" +
	"> <summary>Firewall rules blocked me from connecting to one or more addresses</summary>\n" +
	">\n" +
	"> If you need me to simply access something, an agent can either:\n" +
	"> </details>\n" +
	"\n" +
	"<!-- This is an auto-generated comment: release notes -->\n" +
	"## Automated summary\n" +
	"- It simply works.\n" +
	"<!-- end of auto-generated comment: release notes -->\n" +
	"\n" +
	"Created from an editor via the [pull-request extension](https://example.com/extension).\n" +
	"\n" +
	"<!-- START CODING AGENT TIPS -->\n" +
	"---\n" +
	"\n" +
	"\U0001F4A1 You can make the coding agent smarter by setting up custom instructions, customizing its development " +
	"environment and configuring Model Context Protocol (MCP) servers. Learn more " +
	"[coding agent tips](https://example.com/tips) in the docs.\n" +
	"\n" +
	"\U0001F4AC Share your feedback on a coding agent for the chance to win a $200 gift card! " +
	"Click [here](https://survey3.medallia.com/?x) to start the survey.\n" +
	"\n" +
	"The agent simply opened this without a test."

func TestJudgeTextSkipsWhatAPlatformWrote(t *testing.T) {
	got := ruleLines(JudgeText(pullRequestWithBoilerplate, KindChangeDescription, houseOn), RuleFiller, RuleAttribution)

	if want := []string{"filler:6", "filler:7", "filler:31"}; !slices.Equal(got, want) {
		t.Errorf("findings: got %q, want %q", got, want)
	}
}

// The prompts and pasted summaries a platform writes into a pull request, and
// a template's category checkbox; only lines 2, 5, 15 and 30 are the author's.
const pullRequestWithAgentText = "fix: keep the key stable\n" +
	"The key simply sorts its inputs.\n" +
	"\n" +
	"- [ ] Public API: Breaking change (it simply renames the key)\n" +
	"- [ ] Docs: simply update the guide (after the merge)\n" +
	"\n" +
	"[Agent summary]\n" +
	"This pull request simply sorts the key. [[1]](diffhunk://#diff-1a)\n" +
	"\n" +
	"* It simply hashes the inputs. [[2]](diffhunk://#diff-2b)\n" +
	"\n" +
	"These changes simply keep the key stable.\n" +
	"\n" +
	"## Testing\n" +
	"The tests simply pass.\n" +
	"\n" +
	"<!-- START CODING AGENT SUFFIX -->\n" +
	"\n" +
	"<details>\n" +
	"\n" +
	"<summary>Original prompt</summary>\n" +
	"\n" +
	"> <issue_title>The key simply drifts</issue_title>\n" +
	"> <issue_description>It simply drifts.</issue_description>\n" +
	"\n" +
	"</details>\n" +
	"*This pull request was created as a result of the following prompt from an agent chat.*\n" +
	"> Simply keep the key stable.\n" +
	"\n" +
	"Fixes #12, which simply drifted."

func TestJudgeTextSkipsAPromptOrSummaryAPlatformQuoted(t *testing.T) {
	got := ruleLines(JudgeText(pullRequestWithAgentText, KindChangeDescription, houseOn), RuleFiller)

	if want := []string{"filler:2", "filler:5", "filler:15", "filler:30"}; !slices.Equal(got, want) {
		t.Errorf("findings: got %q, want %q", got, want)
	}
}

// A marker with nothing of its platform's under it blanks no author text.
func TestJudgeTextKeepsWhatFollowsAMarkerWithNoPlatformText(t *testing.T) {
	text := "fix: keep the key stable\nThe key sorts its inputs.\n\n" +
		"[Agent summary]\nIt simply sorts the key.\n\n" +
		"<!-- START CODING AGENT SUFFIX -->\n\nIt simply hashes.\n\n" +
		"*Created from a prompt.*\n> It simply drifts."

	if got, want := ruleLines(JudgeText(text, KindChangeDescription, houseOn), RuleFiller), []string{"filler:5", "filler:9", "filler:12"}; !slices.Equal(got, want) {
		t.Errorf("findings: got %q, want %q", got, want)
	}
}

func TestAgentTipMatchesUnderAnyProductName(t *testing.T) {
	for _, line := range []string{
		"\U0001F4A1 You can make an agent smarter by setting up custom instructions, customizing its development environment.",
		"You can make the coding agent smarter by setting up custom instructions, customizing its development environment.",
	} {
		if !agentTip.MatchString(line) {
			t.Errorf("agentTip does not match %q", line)
		}
	}

	if agentTip.MatchString("You can make the build smarter by caching its inputs.") {
		t.Error("agentTip matches a sentence with no custom instructions in it")
	}
}

// A review bot that writes a whole reply opens it with its marker; the same
// marker further down, or the same words without it, are judged.
func TestJudgeTextSkipsAReplyABotWroteWhole(t *testing.T) {
	const notice = "Something went wrong, please try again."

	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"opened by the marker", "\n<!-- This is an auto-generated reply by an automated reviewer -->\n" + notice, nil},
		{"no marker", notice, []string{"nonspecific:1"}},
		{"the marker after the text", notice + "\n<!-- This is an auto-generated reply by an automated reviewer -->",
			[]string{"nonspecific:1"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ruleLines(JudgeText(tc.text, KindReviewReply, houseOn), RuleNonspecific); !slices.Equal(got, tc.want) {
				t.Errorf("findings: got %q, want %q", got, tc.want)
			}
		})
	}
}

// Only the platform's own shape is boilerplate: a marker with no partner
// leaves the text after it judged, and so does a sentence quoting a tip.
func TestJudgeTextKeepsATextThatOnlyResemblesBoilerplate(t *testing.T) {
	text := "fix: keep the key stable\nThe key sorts its inputs.\n\n" +
		"<!-- This is an auto-generated comment: release notes -->\nIt simply works.\n\n" +
		"They said you can make an agent smarter by simply asking."

	if got, want := ruleLines(JudgeText(text, KindChangeDescription, houseOn), RuleFiller), []string{"filler:5", "filler:7"}; !slices.Equal(got, want) {
		t.Errorf("findings: got %q, want %q", got, want)
	}
}

// ruleLines renders each finding of rules as rule:line, sorted by line.
func ruleLines(found []Finding, rules ...Rule) []string {
	var out []string

	slices.SortStableFunc(found, func(a, b Finding) int { return a.Line - b.Line })

	for _, f := range found {
		if slices.Contains(rules, f.Rule) {
			out = append(out, fmt.Sprintf("%s:%d", f.Rule, f.Line))
		}
	}

	return out
}
