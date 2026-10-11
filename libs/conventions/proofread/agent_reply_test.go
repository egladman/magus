package proofread

import (
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestAgentReplyChecksRunInThisOrder(t *testing.T) {
	want := []Rule{
		RuleBoldLabel, RuleClosingOffer, RuleRequestRecap, RuleOptionList, RuleUnbackedDone,
		RuleShortReplyHeading, RuleAgreementOpener,
	}

	if got := ruleNames(agentReplyChecks); !reflect.DeepEqual(got, want) {
		t.Errorf("agentReplyChecks = %q, want %q", got, want)
	}
}

func TestAgentReplyRunsTheRulesThatFitAReplyAndLeavesTheRest(t *testing.T) {
	rules := KindRules(KindAgentReply)

	for _, r := range []Rule{
		RuleFiller, RuleHedge, RuleWordy, RuleCondescension, RuleBlame, RuleIntent, RuleClaim, RuleSignpost,
		RuleLeak, RuleBuzzword, RuleBuzzwordWeak, RuleContrast, RuleVague, RuleCloser, RuleIngTail,
		RuleDash, RuleASCII, RuleTerms, RuleBoldLabel, RuleClosingOffer, RuleUnbackedDone,
	} {
		if !slices.Contains(rules, r) {
			t.Errorf("%s does not judge an agent reply", r)
		}
	}

	for _, r := range []Rule{
		RuleReplyVoice, RuleLeadContext, RuleChatbot, RuleAbsolute, RuleVerdict, RuleAttribution, RuleTense,
		RuleReplyOpener, RuleJudgmentAsFact,
	} {
		if slices.Contains(rules, r) {
			t.Errorf("%s judges an agent reply", r)
		}
	}
}

// A Stop hook shows an agent its reply's findings and never blocks, but a
// caller that gates on deny still meets only the rules sure in a reply.
func TestAgentReplyDeniesOnlyTheRulesSureInAReply(t *testing.T) {
	want := map[Rule]Decision{
		RuleCondescension: DecisionDeny, RuleBuzzword: DecisionDeny, RuleVague: DecisionDeny,
		RuleCloser: DecisionDeny, RuleFiller: DecisionAdvise, RuleHedge: DecisionAdvise,
		RuleWordy: DecisionAdvise, RuleBlame: DecisionAdvise, RuleIntent: DecisionAdvise,
		RuleClaim: DecisionAdvise, RuleSignpost: DecisionAdvise, RuleLeak: DecisionAdvise,
		RuleBuzzwordWeak: DecisionAdvise, RuleContrast: DecisionAdvise, RuleIngTail: DecisionAdvise,
	}
	for _, c := range agentReplyChecks {
		want[c.rule] = DecisionAdvise
	}

	for rule, d := range want {
		c, _ := ruleCheck(rule)
		if got := c.defaultDecision(KindAgentReply); got != d {
			t.Errorf("%s decides %s on an agent reply, want %s", rule, got, d)
		}
	}
}

func TestBoldLabelReportsTheFirstLabelAndCountsTheRest(t *testing.T) {
	text := "The rename is pushed.\n\n- **Flags:** the old name is read.\n- **Docs:** regenerated.\n" +
		"- **Tests:** green.\n"

	var got []Finding

	for _, f := range JudgeText(text, KindAgentReply) {
		if f.Rule == RuleBoldLabel {
			got = append(got, f)
		}
	}

	if len(got) != 1 || got[0].Line != 3 || got[0].Match != "**Flags:**" ||
		!strings.Contains(got[0].Message, "the 2 after it") {
		t.Errorf("bold-label findings = %+v, want one at 3:**Flags:** counting 2 more", got)
	}
}

func TestClosingOfferJudgesOnlyTheLastParagraph(t *testing.T) {
	runTextCases(t, RuleClosingOffer, []textCase{
		{"an offer closing the reply", KindAgentReply, "The key sorts.\n\nWant me to push?", []string{"3:Want me to"}},
		{"an offer before the result", KindAgentReply, "Want me to push? I did.\n\nThe key sorts.", nil},
		{"a heading after the offer", KindAgentReply, "The key sorts. Should I push?\n\n## Next", []string{"1:Should I"}},
	})
}

func TestUnbackedDoneTakesEvidenceFromItsParagraph(t *testing.T) {
	runTextCases(t, RuleUnbackedDone, []textCase{
		{"a bare claim", KindAgentReply, "Done. The branch is ready.", []string{"1:Done"}},
		{"a command in the paragraph", KindAgentReply, "Done. The branch is ready:\n`magus run go-test .` passes.", nil},
		{"a ref in another paragraph", KindAgentReply, "Done.\n\nThe run is out1a2b3c4d5e.", []string{"1:Done"}},
		{"one finding per paragraph", KindAgentReply, "Fixed. The gate is green.", []string{"1:Fixed"}},
		{"a negation", KindAgentReply, "The gate has not passed yet.", nil},
	})
}

func TestShortReplyHeadingCountsTheProseOnly(t *testing.T) {
	code := "```\n" + strings.Repeat("word ", 400) + "\n```\n"

	runTextCases(t, RuleShortReplyHeading, []textCase{
		{"a short reply", KindAgentReply, "## Status\n\nThe key sorts.", []string{"1:Status"}},
		{"code does not lengthen it", KindAgentReply, "## Status\n\n" + code + "\nThe key sorts.", []string{"1:Status"}},
		{"a long reply", KindAgentReply, "## Status\n\n" + strings.Repeat("The key sorts its inputs. ", 60), nil},
	})
}
