package proofread

import (
	"regexp"
	"slices"
	"strings"
)

// RuleIssueRepro reports a bug report with neither what was observed against
// what was expected, nor steps to reproduce it.
const RuleIssueRepro Rule = "issue-repro"

var issueKind = []Kind{KindIssue}

// issueChecks hold the issue rule and the release rules: the text a project
// writes for the people who track it.
var issueChecks = slices.Concat([]check{
	{rule: RuleIssueRepro, on: issueKind, advise: issueKind, judge: issueRepro},
}, changelogChecks)

var issueTexts = mergeTexts(map[Rule]ruleText{
	RuleIssueRepro: {
		code:    "PRF1020",
		catches: "a bug report with neither what happened against what was expected, nor steps to reproduce it",
		why: "A maintainer cannot start on a defect they cannot see. A title with a defect word (\"crash\", " +
			"\"fails\", \"regression\") that is followed by no \"expected\", \"actual\", \"observed\", \"steps " +
			"to reproduce\" or \"instead of\" sends the first reply to asking for them. It advises: the " +
			"title is read for the defect and the body for the cue, and either can be phrased another way. " +
			"Neither of this repository's two issues has a defect title, so it measured no firing.",
	},
}, changelogTexts)

var (
	// bugTitle reads the title for the words a report of a defect uses.
	bugTitle = regexp.MustCompile(`(?i)\b(?:bug|crash(?:es|ed|ing)?|panic(?:s|ked)?|fails?|failing|failed|failure|` +
		`regression|broken|hangs?|freezes?|doesn'?t|does not|wrong|incorrect|unexpected)\b`)

	// reproCue is a word a body that shows the defect uses somewhere.
	reproCue = regexp.MustCompile(`(?i)\b(?:expected|expect|actual|observed|steps? to reproduce|reproduc\w*|repro|` +
		`what happened|instead of|should have)\b`)
)

func issueRepro(in input) []Finding {
	if len(in.source) == 0 || !bugTitle.MatchString(in.source[0]) {
		return nil
	}

	if reproCue.MatchString(strings.Join(in.source[1:], "\n")) {
		return nil
	}

	return []Finding{{
		Message: "Say what happened against what you expected, or the steps to reproduce it: a reader cannot start " +
			"on a defect they cannot see.",
		Match: bugTitle.FindString(in.source[0]),
		Line:  1,
	}}
}
