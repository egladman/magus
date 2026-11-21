package proofread

import "regexp"

// RuleNextHousekeeping reports PR administration in follow-up sections.
const RuleNextHousekeeping Rule = "next-housekeeping"

var nextChecks = []check{
	{rule: RuleNextHousekeeping, reads: []Format{FormatMarkdown}, judge: ownWords(nextHousekeeping)},
}

var nextTexts = map[Rule]ruleText{
	RuleNextHousekeeping: {
		code: "PRF1006", dimension: DimensionStructure,
		catches: "a follow-up section that asks someone to check CI, obtain reviews or merge the current pull request",
		why: "Next describes substantive work after the change, such as extending the API or replacing a consumer. " +
			"Checking CI, obtaining approval and merging are administration of the current pull request. " +
			"The rule matches an administrative action and its review or validation target within a follow-up section. " +
			"Adding CI coverage, changing a review system and discussing architecture remain substantive work. " +
			"This is a change-description section contract, not a measured style score.",
	},
}

var nextAdministration = regexp.MustCompile(`(?i)(?:^(?:\[[ xX]\]\s*)?|[.;!?]\s+|\band\s+)\b(?:` +
	`(?:check|verify|review|inspect|monitor|watch|wait|await|run|rerun|re-run|confirm|ensure)\s+` +
	`(?:(?:for|the|all|new|latest|remaining|final|our|my|these|those|github|gitlab|regression|integration|unit)\s+)*` +
	`(?:ci|checks?|pipelines?|test(?:s|\s+suite|\s+results)|build\s+status|review\s+(?:feedback|approval)|approvals?|merge\s+queue)` +
	`|(?:address|resolve|obtain|get|request)\s+(?:the\s+)?(?:reviewer\s+comments|review\s+(?:feedback|approval)|approvals?)` +
	`|(?:merge|land|ship|publish)\s+(?:this|the)\s+(?:pr|pull\s+request|change|branch)` +
	`|(?:merge|land|ship|publish)\s+(?:after|once|when)\s+(?:(?:the|all)\s+)?(?:ci|checks|reviews?|approvals?)` +
	`)\b(?:\s+(?:checks?|results?|status)){0,2}` +
	`(?:[.!?;,]|$|\s+(?:after|before|once|when|until|and|to|pass(?:es|ed)?|finish(?:es|ed)?|complete(?:s|d)?)\b)`)

func nextHousekeeping(in input) []Finding {
	within := futureLines(in.lines, []string{"Next", "Next steps", "Later", "Follow-up", "Follow-ups"})
	var found []Finding
	for _, para := range paragraphs(in.prose, func(text string) string { return text }) {
		if !within[para.head.line] || !nextAdministration.MatchString(mentionsMasked(para.text)) {
			continue
		}
		found = append(found, Finding{
			Message: "Keep Next for substantive work after this change. Remove PR administration such as checking CI, obtaining review or merging.",
			Match:   para.text, Line: para.head.line,
		})
	}
	return found
}
