package proofread

// weights are what one advise finding costs its text's [Grade]: the Wilson 95%
// lower bound on the rule's precision in the latest round that scored it, where
// that round labeled 30 or more firings (testdata/rounds). internal/calibrate
// recomputes each. A rule absent here weighs 0.
var weights = map[Rule]float64{
	RuleAbsolute:          0.07, // round 4: 6/40
	RuleAgreementOpener:   0.91, // round 1: 40/40
	RuleAllCaps:           0.52, // round 3: 27/40
	RuleBareImperative:    0.57, // round 3: 29/40
	RuleBlame:             0.79, // round 3: 34/37
	RuleBodySeparator:     0.91, // round 1: 40/40
	RuleBoldLabel:         0.91, // round 1: 40/40
	RuleBuzzword:          0.50, // round 2: 26/40
	RuleBuzzwordWeak:      0.14, // round 2: 10/40
	RuleClaim:             0.40, // round 4: 22/40
	RuleClosingOffer:      0.80, // round 2: 37/40
	RuleCredit:            0.71, // round 2: 34/40
	RuleFiller:            0.65, // round 3: 32/40
	RuleHedge:             0.62, // round 3: 31/40
	RuleIssueRepro:        0.50, // round 4: 26/40
	RuleJudgmentAsFact:    0.24, // round 3: 15/40
	RuleLeadContext:       0.55, // round 2: 28/40
	RuleLeak:              0.57, // round 1: 29/40
	RuleLongThread:        0.91, // round 2: 40/40
	RuleNominalizations:   0.52, // round 2: 27/40
	RuleNonspecific:       0.29, // round 7: 17/40
	RuleParticiples:       0.83, // round 7: 38/40
	RuleRepeatedMarks:     0.90, // round 3: 34/34
	RuleReplyOpener:       0.37, // round 3: 21/40
	RuleReplyVoice:        0.55, // round 1: 28/40
	RuleShortReplyHeading: 0.91, // round 1: 40/40
	RuleStackedHedge:      0.77, // round 1: 36/40
	RuleSubjectLength:     0.91, // round 1: 40/40
	RuleSubjectMood:       0.80, // round 3: 37/40
	RuleSubjectPeriod:     0.91, // round 1: 40/40
	RuleUnbackedDone:      0.71, // round 1: 34/40
	RuleVerdict:           0.26, // round 3: 16/40
	RuleWordy:             0.57, // round 1: 29/40
}

// unmeasured ship off until a round measures 30 or more fresh firings and gives
// them a weight; a decisions table still sets any rule it names. Each entry
// gives its latest round's correct firings over all labeled.
var unmeasured = map[Rule]bool{
	RuleSecondPerson:              true, // round 5: no fresh firing
	RuleChangelogHeading:          true, // round 4: no fresh firing
	RuleIngTail:                   true, // round 4: 3/17
	RuleOptionList:                true, // round 4: 0/1
	RuleStaccato:                  true, // round 3: 6/12
	RuleIntent:                    true, // round 3: 1/4
	RuleChatbot:                   true, // round 3: 13/20
	RuleCondescension:             true, // round 3: 20/26
	RuleContrast:                  true, // round 3: 4/5
	RuleNoReason:                  true, // no labeled firing yet
	RuleNitLabel:                  true, // no labeled firing yet
	RuleEmptyRequestChanges:       true, // no labeled firing yet
	RuleApproveBlocker:            true, // no labeled firing yet
	RuleBareLGTM:                  true, // no labeled firing yet
	RuleDeferralNoLink:            true, // no labeled firing yet
	RuleDeclineNoReason:           true, // no labeled firing yet
	RuleImpatience:                true, // no labeled firing yet
	RuleListMarks:                 true, // no labeled firing yet
	RuleListParallel:              true, // no labeled firing yet
	RuleDateFormat:                true, // no labeled firing yet
	RuleCompactFiller:             true, // no labeled firing yet
	RuleCompactMarkup:             true, // no labeled firing yet
	RuleCompactCostlyAbbrev:       true, // no labeled firing yet
	RuleCompactSymbolNegation:     true, // no labeled firing yet
	RuleCompactLine:               true, // no labeled firing yet
	RuleCompactDuplicate:          true, // no labeled firing yet
	RuleSkillDescriptionBudget:    true, // no labeled firing yet
	RuleSkillSetDescriptionBudget: true, // no labeled firing yet
	RuleInstructionPosition:       true, // no labeled firing yet
}
