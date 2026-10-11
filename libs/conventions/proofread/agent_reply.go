package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// RuleBoldLabel reports an agent reply whose list items or paragraphs open
	// with a bold label ("**Tests:** pass").
	RuleBoldLabel Rule = "bold-label"
	// RuleClosingOffer reports an agent reply whose last paragraph offers more
	// work or asks leave to go on ("Want me to", "Should I", "Let me know if").
	RuleClosingOffer Rule = "closing-offer"
	// RuleRequestRecap reports an agent reply that opens by restating what the
	// person asked ("You asked for", "Your question is").
	RuleRequestRecap Rule = "request-recap"
	// RuleOptionList reports an agent reply that lays out labeled options or
	// alternatives ("Option A", "(a) ... (b)", "Two options").
	RuleOptionList Rule = "option-list"
	// RuleUnbackedDone reports a claim in an agent reply that work is done,
	// fixed, verified or passing with no command, output ref, file or link in
	// its paragraph or list item.
	RuleUnbackedDone Rule = "unbacked-done"
	// RuleShortReplyHeading reports a heading in an agent reply shorter than
	// shortReplyWords.
	RuleShortReplyHeading Rule = "short-reply-heading"
	// RuleAgreementOpener reports an agent reply that opens with praise or
	// agreement ("Great question", "You're right", "Got it.").
	RuleAgreementOpener Rule = "agreement-opener"
)

// agentReply is the kind the rules in this file judge. Each advises: whether
// the person asked for the options, the question or the label is in the
// conversation, which the rules cannot read.
var agentReply = []Kind{KindAgentReply}

// agentWords are the kinds the word tells judge: withReply and an agent's
// reply, which its person reads as the agent's own words. chatbot and
// reply-voice stay off the reply, since its offers and openers are this file's
// rules to report.
var agentWords = slices.Concat(withReply, agentReply)

var agentReplyChecks = []check{
	{rule: RuleBoldLabel, on: agentReply, advise: agentReply, judge: boldLabels},
	{rule: RuleClosingOffer, on: agentReply, advise: agentReply, judge: closingOffer},
	{rule: RuleRequestRecap, on: agentReply, advise: agentReply, judge: requestRecap},
	{rule: RuleOptionList, on: agentReply, advise: agentReply, judge: optionList},
	{rule: RuleUnbackedDone, on: agentReply, advise: agentReply, judge: unbackedDone},
	{rule: RuleShortReplyHeading, on: agentReply, advise: agentReply, judge: shortReplyHeading},
	{rule: RuleAgreementOpener, on: agentReply, advise: agentReply, judge: agreementOpener},
}

var agentReplyTexts = map[Rule]ruleText{
	RuleBoldLabel: {
		code:      "PRF8020",
		dimension: DimensionStructure,
		catches:   "list items or paragraphs of an agent reply that open with a bold label",
		why: "A label turns each point into a field of a form, so the person reads the labels and " +
			"rebuilds the sentences. Measured 2026-10-10 over 400 end-of-turn replies sampled from " +
			"one machine's coding-agent sessions (101 projects, median 266 words): it fires on 121 " +
			"(30.3%). Judged as review-reply, bold labels were the largest share of the 85 denials in " +
			"200 replies. It reports the first label and counts the rest. It advises: a long reply may " +
			"need a label to be scanned. Firings are counted, not labeled.",
	},
	RuleClosingOffer: {
		code:      "PRF8021",
		dimension: DimensionStance,
		catches:   "an agent reply whose last paragraph offers more work or asks leave to go on",
		why: "A reflexive offer hands the next decision back to the person and costs them a turn. " +
			"Measured 2026-10-10 over the same 400 replies: it fires on 62 (15.5%), \"Want me to\" in " +
			"most. Of 45 closing offers read, about a third asked for a decision the agent needed (a " +
			"push, a branch name, a choice between designs), so it advises: the rule cannot tell a " +
			"needed question from a reflexive one.",
	},
	RuleRequestRecap: {
		code:      "PRF8022",
		dimension: DimensionEconomy,
		catches:   "an agent reply that opens by restating what the person asked",
		why: "The person knows what they asked, and a restatement delays the answer. Measured " +
			"2026-10-10: it fires on 1 of the 400 replies, and 3 of all 7088 end-of-turn replies on that " +
			"machine open this way, too few to measure precision. It advises: a restatement can also " +
			"correct a misreading.",
	},
	RuleOptionList: {
		code:      "PRF8023",
		dimension: DimensionStructure,
		catches:   "an agent reply that lays out labeled options or alternatives",
		why: "Options the person did not ask for move a decision the agent could make back to them. " +
			"Measured 2026-10-10: it fires on 6 of the 400 replies (1.5%), and a broader pattern found " +
			"158 of all 7088 (2.2%). It advises: the rule cannot see whether the person asked for the " +
			"choices.",
	},
	RuleUnbackedDone: {
		code:      "PRF8024",
		dimension: DimensionEvidence,
		catches:   "a claim of done, fixed, verified or passing with no command, output ref, file or link beside it",
		why: "\"Done and verified\" asks the person to trust a run they cannot open; a backticked command, " +
			"a magus output ref or a file beside the claim lets them check it. Measured 2026-10-10 over " +
			"the 400 replies: it fires on 55 (13.8%), \"Done\", \"Fixed\" and \"gate green\" most. A " +
			"negated or conditional claim (\"not verified\", \"once it passes\") is left alone. It " +
			"advises: the person may have watched the run, and evidence may sit in another paragraph.",
	},
	RuleShortReplyHeading: {
		code:      "PRF8025",
		dimension: DimensionStructure,
		catches:   "a heading in an agent reply under 300 words",
		why: "A reply that short reads in one screen, and a heading there splits it into a page the " +
			"person has to navigate. Measured 2026-10-10 over the 400 replies: 64 carry a heading, and " +
			"it fires on 17 (4.3%) whose prose runs under 300 words. It advises: a heading can still " +
			"mark the one decision the person owes.",
	},
	RuleAgreementOpener: {
		code:      "PRF8026",
		dimension: DimensionStance,
		catches:   "an agent reply that opens with praise or agreement (\"Great question\", \"You're right\")",
		why: "Praise or agreement first answers the person's tone rather than their request, and pushes " +
			"the result down. Measured 2026-10-10 over the 400 replies: it fires on 15 (3.8%), 10 of " +
			"them \"You're right\" or \"You were right\". It advises: \"You're right\" can concede a " +
			"correction the person needs to hear was taken.",
	},
}

// shortReplyWords is the length under which a reply reads in one screen: the
// median of the 400 sampled replies was 266 words.
const shortReplyWords = 300

func boldLabels(in input) []Finding {
	var first *Finding

	n := 0

	for _, ln := range in.prose {
		if !ln.opens || ln.heading {
			continue
		}

		m := boldLabel.FindString(strings.TrimSpace(ln.body()))
		if m == "" {
			continue
		}

		if n++; first == nil {
			first = &Finding{Match: m, Line: ln.line}
		}
	}

	if first == nil {
		return nil
	}

	first.Message = fmt.Sprintf("Open with the subject instead of the bold label '%s'", first.Match)
	if n > 1 {
		first.Message += fmt.Sprintf(" and the %d after it", n-1)
	}

	first.Message += ": write each point as a sentence, as in 'The cache tests pass (`go test ./cache`).'"

	return []Finding{*first}
}

// offer opens a sentence that offers more work or asks leave to continue.
var offer = regexp.MustCompile(`(?i)^(?:(?:so|and|or|otherwise),? )?(?:want me to|do you want(?: me)? to|` +
	`would you like(?: me)? to|should I\b|shall I\b|let me know (?:if|whether|when|how|what)\b|` +
	`if you(?:'d| would)? (?:like|want)(?: (?:me )?to)?[^.?!]{0,60}?,? I(?:'ll| will| can| could)?\b|happy to\b|` +
	`I can (?:also )?[^.?!]{0,80}\bif you(?:'d| would)? (?:like|want)\b|(?:just )?say (?:the word|go)\b)`)

func closingOffer(in input) []Finding {
	paras := paragraphs(in.prose, mentionsMasked)
	for i := len(paras) - 1; i >= 0; i-- {
		para := paras[i]
		if para.head.heading {
			continue
		}

		for _, start := range sentenceStarts(para.text) {
			lead := strings.TrimLeft(para.text[start:], "*_( ")
			if m := offer.FindString(lead); m != "" {
				return []Finding{{
					Message: fmt.Sprintf("End on the result instead of '%s': name the one decision you need, "+
						"or make it and say so.", m),
					Match: m, Line: para.lineAt(start),
				}}
			}
		}

		return nil
	}

	return nil
}

var recap = regexp.MustCompile(`(?i)^(?:you (?:asked|wanted|want|need|were asking|are asking)\b|` +
	`you(?:'re| are) asking\b|you'd like\b|your (?:question|request|ask) (?:is|was)\b|` +
	`the (?:ask|question|request) (?:is|was)\b|so the question is\b|to answer your question\b|` +
	`to (?:recap|restate|summari[sz]e) (?:your|what you|the (?:ask|request|question))\b)`)

func requestRecap(in input) []Finding {
	return openingFinding(in, recap, "Open with the answer instead of '%s': the person knows what they asked.")
}

var agreement = regexp.MustCompile(`(?i)^(?:(?:great|good|excellent|fair|valid|interesting) ` +
	`(?:question|point|catch|call|idea|instinct)\b|(?:yes,? )?you(?:'re| are| were) ` +
	`(?:absolutely |completely |totally |quite )?right\b|` +
	`(?:absolutely|certainly|of course|sure|perfect|got it|understood|makes sense|good call|agreed)\s*[!,.:])`)

func agreementOpener(in input) []Finding {
	return openingFinding(in, agreement,
		"Open with the result or the change instead of '%s': praise or agreement answers the tone, not the request.")
}

// openingFinding reports re matching the first sentence of the reply.
func openingFinding(in input, re *regexp.Regexp, message string) []Finding {
	for _, para := range paragraphs(in.prose, mentionsMasked) {
		if para.head.heading {
			continue
		}

		lead := strings.TrimLeft(para.text, "*_( ")
		if m := re.FindString(lead); m != "" {
			return []Finding{{Message: fmt.Sprintf(message, m), Match: m, Line: para.head.line}}
		}

		return nil
	}

	return nil
}

var labeledOptions = regexp.MustCompile(`(?im)^(?:(?:[-*+]|\d+\.)\s+)?(?:\*\*|__)?(?:option|approach|alternative) ` +
	`(?:[A-D1-4]|one|two|three)\b|\((?:a|1)\)[^()\n]{3,200}\((?:b|2)\)|` +
	`\b(?:two|three|four|a few) (?:options|approaches|alternatives|choices|paths forward|ways (?:forward|to go))\b`)

func optionList(in input) []Finding {
	for _, para := range paragraphs(in.prose, mentionsMasked) {
		if at := labeledOptions.FindStringIndex(para.text); at != nil {
			m := para.text[at[0]:at[1]]

			return []Finding{{
				Message: fmt.Sprintf("Recommend one and say why instead of '%s', unless the person asked to "+
					"choose: options hand back a decision.", strings.TrimSpace(m)),
				Match: m, Line: para.lineAt(at[0]),
			}}
		}
	}

	return nil
}

var (
	// doneClaim is a claim that work is complete or checked. "Done" and
	// "Fixed" count opening a sentence or after a subject, where they report
	// a result rather than describe one ("done better").
	doneClaim = regexp.MustCompile(`(?i)^(?:\*\*|__)?(?:done|fixed)\b|` +
		`\b(?:is|are|all|both|now|everything(?:'s)?|that's|it's) (?:done|fixed)\b|` +
		`\b(?:verified|confirmed)\b|` +
		`\b(?:tests?|suite|gate|ci|checks?|build|lint) (?:all |now |still )?(?:pass(?:es|ed)?|(?:is |are )?green)\b|` +
		`\ball (?:green|pass(?:ing)?)\b|\b(?:it|this|everything) works\b`)

	// doneHedged is a word that makes a sentence's claim conditional, negated
	// or still to come, so it claims nothing done.
	doneHedged = regexp.MustCompile(`(?i)\b(?:not|never|no|nothing|without|isn't|aren't|wasn't|haven't|hasn't|` +
		`didn't|couldn't|can't|cannot|before|until|once|if|when|whether|unless|to be|yet|still to)\b`)

	// filePath is a path or a file name a reader can open.
	filePath = regexp.MustCompile(`[\w.-]+/[\w.-]+/[\w./-]+|[\w.-]+/[\w-]+\.\w+|\b[\w-]+\.(?:go|buzz|md|py|js|ts|tsx|json|ya?ml|toml|` +
		`sh|txt|txtar|lock|html|css)\b`)
)

func unbackedDone(in input) []Finding {
	masked := paragraphs(in.prose, mentionsMasked)
	plain := paragraphs(in.prose, keep)

	var out []Finding

	for i, para := range masked {
		if para.head.heading {
			continue
		}

		last := para.head.line
		if n := len(para.starts); n > 0 {
			last = para.starts[n-1].line
		}

		if cited(plain[i].text, in.source, para.head.line, last) || filePath.MatchString(plain[i].text) {
			continue
		}

		for _, span := range sentenceBounds(para.text) {
			text := para.text[span[0]:span[1]]
			if strings.HasSuffix(strings.TrimSpace(text), "?") {
				continue
			}

			at := doneClaim.FindStringIndex(text)
			if at == nil || doneHedged.MatchString(text) {
				continue
			}

			m := strings.TrimLeft(text[at[0]:at[1]], "*_")
			out = append(out, Finding{
				Message: fmt.Sprintf("Put what shows '%s' beside it: the command in backticks, the output ref, "+
					"or the file, as in 'Tests pass (`go test ./cache`, out1a2b3c4d).'", m),
				Match: m, Line: para.lineAt(span[0] + at[0]),
			})

			break
		}
	}

	return out
}

func shortReplyHeading(in input) []Finding {
	words := 0

	var first *proseLine

	for i, ln := range in.prose {
		if ln.heading {
			if first == nil {
				first = &in.prose[i]
			}

			continue
		}

		words += countWords(mentionsMasked(ln.body()))
	}

	if first == nil || words >= shortReplyWords {
		return nil
	}

	m := strings.TrimSpace(first.body())

	return []Finding{{
		Message: fmt.Sprintf("Drop the heading '%s': a reply of %d words reads in one screen, so lead with "+
			"the result and let paragraphs carry the order.", m, words),
		Match: m, Line: first.line,
	}}
}
