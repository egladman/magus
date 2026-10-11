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
// The second group was tuned after labeling round 2 and measured over frozen
// round 4 (178 fresh firings, two labelers, adjudicated): each entry gives its
// correct firings over all labeled, then the lower bound. nonspecific and
// second-person were left out of round 4 for a redesign and keep their round-2
// precision and lower bound. participles' 36/40 is not evidence: in 10 of its
// correct firings a platform footer's clause carries the rate over the cap.
var evidenceCeiling = map[Rule]Decision{
	RuleNonspecific:      DecisionOff, // round 2: 0.07, 0.02
	RuleAbsolute:         DecisionOff, // 6/40, 0.07
	RuleSecondPerson:     DecisionOff, // round 2: 0.19, 0.10
	RuleIssueRepro:       DecisionOff, // 26/40, 0.50
	RuleChangelogHeading: DecisionOff, // no fresh firing
	RuleIngTail:          DecisionOff, // 3/17, 0.06
	RuleClaim:            DecisionOff, // 22/40, 0.40
	RuleOptionList:       DecisionOff, // 0/1
	RuleParticiples:      DecisionOff, // 36/40, 0.77; see above

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
