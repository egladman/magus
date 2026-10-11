package proofread

import (
	"slices"
	"testing"
)

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
