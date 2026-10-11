package proofread

// evidenceCeiling caps a rule's shipped default at the strictest decision
// fresh labeled firings support: advise needs a Wilson 95% lower bound on
// precision of 0.70 over 30 or more firings, deny 0.90. Fresh means text the
// rule's exceptions were never tuned on, so a rule tightened against one
// round's cases is judged on the next. A rule leaves this table once its
// fresh evidence supports its declared decision, and a decisions table still
// sets any rule it names.
//
// Measured 2026-10-11 over labeling round 3: 458 firings sampled from the
// AIDev export and this repository after the round-1 fixes, labeled by two
// labelers and adjudicated. Each entry gives its correct firings over all
// labeled, then the lower bound. repeated-marks (34/34, 0.90) supports its
// declared advise and is not capped.
//
// The second group was measured over labeling round 2 (707 firings, two
// labelers, adjudicated) on rules no fix round had tuned: each fell short on
// precision, not only on the number of firings, and its entry gives its
// precision and lower bound over every labeled case it has.
var evidenceCeiling = map[Rule]Decision{
	RuleNonspecific:      DecisionOff, // 0.07, 0.02
	RuleAbsolute:         DecisionOff, // 0.18, 0.10
	RuleSecondPerson:     DecisionOff, // 0.19, 0.10
	RuleIssueRepro:       DecisionOff, // 0.26, 0.15
	RuleChangelogHeading: DecisionOff, // 0.27, 0.10
	RuleIngTail:          DecisionOff, // 0.42, 0.28
	RuleClaim:            DecisionOff, // 0.51, 0.37
	RuleOptionList:       DecisionOff, // 0.71, 0.56
	RuleParticiples:      DecisionOff, // 0.81, 0.67

	RuleVerdict:        DecisionOff,    // 16/40, 0.26
	RuleJudgmentAsFact: DecisionOff,    // 15/40, 0.24
	RuleStaccato:       DecisionOff,    // 6/12, 0.25
	RuleIntent:         DecisionOff,    // 1/4, 0.05
	RuleChatbot:        DecisionOff,    // 13/20, 0.43
	RuleBareImperative: DecisionOff,    // 29/40, 0.57
	RuleReplyOpener:    DecisionOff,    // 21/40, 0.37
	RuleCondescension:  DecisionOff,    // 20/26, 0.58
	RuleContrast:       DecisionOff,    // 4/5, 0.38
	RuleHedge:          DecisionOff,    // 31/40, 0.62
	RuleAllCaps:        DecisionOff,    // 27/40, 0.52
	RuleFiller:         DecisionOff,    // 32/40, 0.65
	RuleBlame:          DecisionAdvise, // 34/37, 0.79
	RuleSubjectMood:    DecisionAdvise, // 37/40, 0.80
}

// strictness orders decisions from off to deny.
func strictness(d Decision) int {
	switch d {
	case DecisionDeny:
		return 2
	case DecisionAdvise:
		return 1
	default:
		return 0
	}
}
