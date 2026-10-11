package proofread

// evidenceCeiling caps a rule's shipped default at the strictest decision its
// labeled cases support, as `proofread calibrate -gates` reports it: advise
// needs a Wilson 95% lower bound on precision of 0.70 over 30 or more labeled
// firings, deny 0.90. A rule leaves this table once its gate passes at its
// declared decision, and a decisions table still sets any rule it names.
//
// Measured 2026-10-10 over the first two-labeler round (1604 firings from the
// AIDev export and this repository, adjudicated): each rule here fell short
// on precision, not on the number of cases.
var evidenceCeiling = map[Rule]Decision{
	RuleVerdict:        DecisionOff,    // lower bound 0.21
	RuleJudgmentAsFact: DecisionOff,    // 0.24
	RuleStaccato:       DecisionOff,    // 0.28
	RuleIntent:         DecisionOff,    // 0.30
	RuleChatbot:        DecisionOff,    // 0.31
	RuleBareImperative: DecisionOff,    // 0.37
	RuleReplyOpener:    DecisionOff,    // 0.44
	RuleCondescension:  DecisionOff,    // 0.45
	RuleRepeatedMarks:  DecisionOff,    // 0.47
	RuleBlame:          DecisionOff,    // 0.52
	RuleContrast:       DecisionOff,    // 0.61
	RuleHedge:          DecisionOff,    // 0.64
	RuleAllCaps:        DecisionOff,    // 0.66
	RuleFiller:         DecisionAdvise, // 0.86
	RuleSubjectMood:    DecisionAdvise, // 0.84
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
