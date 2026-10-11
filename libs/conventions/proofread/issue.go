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
		code:      "PRF1020",
		dimension: DimensionEvidence,
		catches:   "a bug report with neither what happened against what was expected, nor steps to reproduce it",
		why: "A maintainer cannot start on a defect they cannot see. A title with a defect word (\"crash\", " +
			"\"fails\", \"regression\") that is followed by no \"expected\", \"actual\", \"observed\", \"steps " +
			"to reproduce\" or \"instead of\" sends the first reply to asking for them. It advises: the " +
			"title is read for the defect and the body for the cue, and either can be phrased another way. " +
			"Neither of this repository's two issues has a defect title, so it measured no firing. " +
			"Measured 2026-10-11 over the adjudicated labeling round 2, on pull requests read as issues: " +
			"26.2% precision (11 of 42) before, 84.6% (11 of 13) after. A title whose type or opening verb " +
			"names other work (\"test:\", \"Add a regression test\"), whose defect word names a topic " +
			"(\"crash detection\") or sits in code is no report, and a body that says what should happen, " +
			"what happened in the past tense, numbered steps or a quote of the original report shows the " +
			"defect. It stays off until a fresh labeling round measures it.",
	},
}, changelogTexts)

var (
	// bugTitle reads the title for the words a report of a defect uses.
	bugTitle = regexp.MustCompile(`(?i)\b(?:bug|crash(?:es|ed|ing)?|panic(?:s|ked)?|fails?|failing|failed|failure|` +
		`regression|broken|hangs?|freezes?|doesn'?t|does not|wrong|incorrect|unexpected)\b`)

	// reproCue is a word a body that shows the defect uses somewhere: the
	// expected state ("should", "rather than", "now"), the observed one in
	// the past tense ("the tool crashed on"), or steps.
	reproCue = regexp.MustCompile(`(?i)\b(?:expected|expect|actual|observed|steps? to reproduce|reproduc\w*|repro|` +
		`what happened|instead of|rather than|should|now|crashed|failed|hung|froze|panicked|stopped)\b`)

	// numberedStep and quotedReport are a body's steps and a quote of the
	// original report, which carries what it saw.
	numberedStep = regexp.MustCompile(`(?m)^\s*\d+\.\s`)
	quotedReport = regexp.MustCompile(`(?m)^\s*>`)

	// titleTag opens a title with a tag or a type: "[WIP]", "[Bug]:", "fix(cli):".
	titleTag = regexp.MustCompile(`^(?:\s*\[[^\]]*\]:?)*\s*(?:(\w+)(?:\([^)]*\))?!?:\s*)?`)

	// notBugTypes are the change types that say a title is not a defect.
	notBugTypes = wordSet("docs", "test", "tests", "chore", "refactor", "feat", "ci", "build", "style", "perf")

	// workVerbs open a title that names work rather than a defect: "Add a
	// regression test", "Allow nullable exceptions for Failure".
	workVerbs = wordSet("implement", "enhance", "allow", "clarify", "complete", "capture", "catch", "introduce",
		"support", "refactor", "document", "improve", "rework")

	// defectTopic are the defect nouns that name a topic when another noun
	// follows them: "regression test", "crash detection", "failed payment
	// intents".
	defectTopic = wordSet("regression", "crash", "failure", "bug", "panic", "failed")

	// defectTail are the words after a defect noun that keep it the defect:
	// "a crash on query 17", "a failure in the model".
	defectTail = wordSet("on", "in", "at", "with", "when", "while", "during", "after", "before", "for", "of", "and",
		"or", "by", "from", "if", "under", "since", "caused")
)

// issueRepro reports a defect title whose body shows neither the defect nor
// how to reach it. A title is no defect report when its type or its opening
// verb names other work, or when its defect word only names a topic.
func issueRepro(in input) []Finding {
	if len(in.source) == 0 {
		return nil
	}

	m := defectIn(blankBackticks(in.source[0]))
	if m == "" {
		return nil
	}

	body := strings.Join(in.source[1:], "\n")
	if reproCue.MatchString(body) || numberedStep.MatchString(body) || quotedReport.MatchString(body) {
		return nil
	}

	return []Finding{{
		Message: "Say what happened against what you expected, or the steps to reproduce it: a reader cannot start " +
			"on a defect they cannot see.",
		Match: m,
		Line:  1,
	}}
}

// defectIn returns the defect word a title reports, or "".
func defectIn(title string) string {
	tag := titleTag.FindStringSubmatch(title)
	if notBugTypes[strings.ToLower(tag[1])] {
		return ""
	}

	rest := title[len(tag[0]):]
	first := strings.ToLower(leadingWord(rest))

	if (workVerbs[first] || (imperatives[first] && first != "fix")) && !strings.Contains(strings.ToLower(rest), "fix") {
		return ""
	}

	for _, at := range bugTitle.FindAllStringIndex(rest, -1) {
		w := rest[at[0]:at[1]]
		next := leadingWord(strings.TrimLeft(rest[at[1]:], " "))

		if defectTopic[strings.ToLower(w)] && strings.HasPrefix(rest[at[1]:], " ") && next != "" &&
			!defectTail[strings.ToLower(next)] {
			continue
		}

		return w
	}

	return ""
}
