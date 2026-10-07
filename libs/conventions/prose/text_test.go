package prose

import (
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

	assertFindings(t, JudgeText(markdownPage, SurfaceMarkdown), want)
}

func TestJudgeTextReadsAPullRequestTitleAsItsFirstLine(t *testing.T) {
	got := JudgeText("fix: simply pin the key\nPinning the key keeps two racing workers from writing "+
		"different blobs, so the cache now sorts its inputs.", SurfacePullRequest)

	assertFindings(t, got, []Finding{{Rule: RuleFiller, Message: "Drop 'simply': state the fact.", Match: "simply", Line: 1}})
}

// TestJudgeTextOnADocMatchesJudge pins that the doc surface reads text the way
// Judge reads a doc, with the line it found each span on.
func TestJudgeTextOnADocMatchesJudge(t *testing.T) {
	doc := "Resolve returns the path.\n\nIt simply\nrereads a sub-agent - basically twice."

	got := JudgeText(doc, SurfaceDoc)
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

	assertFindings(t, Judge(Symbol{Name: "Resolve", Doc: doc}), want)
}

func TestHeadingMarker(t *testing.T) {
	cases := map[string]int{"# Cache": 2, "###  Cache": 5, "#": 1, "#hashtag": 0, "####### seven": 0, "text": 0}

	for in, want := range cases {
		if got := headingMarker(in); got != want {
			t.Errorf("headingMarker(%q) = %d, want %d", in, got, want)
		}
	}
}
