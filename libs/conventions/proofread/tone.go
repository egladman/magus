package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// The rules in this file judge posture in text written to the people working
// on a change: a pull request and a reply in its review. Text loses its tone on
// the way to a reader, who fills the gap with intent the writer never had, so
// each rule names the posture it catches and how to state the same fact.
// docs/decisions/0008-plain-writing.md records the evidence.

const (
	// RuleBlame reports a person or past work as the subject of a fault, and
	// words of contempt for code or a decision.
	RuleBlame Rule = "blame"
	// RuleVerdict reports a judgment word standing in for the behavior it
	// judges ("was broken", "a mess").
	RuleVerdict Rule = "verdict"
	// RuleAbsolute reports never, nobody or nothing as a claim about the past
	// ("has never fired", "nobody checked").
	RuleAbsolute Rule = "absolute"
	// RuleIntent reports a motive given to a tool or a person ("guessed",
	// "pretends").
	RuleIntent Rule = "intent"
	// RuleCredit reports a pull request that removes or replaces something and
	// says nothing of what it was for.
	RuleCredit Rule = "credit"
	// RuleClaim reports a measurement, a comparison or a completion with no
	// evidence in its sentence or bullet.
	RuleClaim Rule = "claim"
	// RuleReplyOpener reports a sentence of a reply that opens by
	// contradicting ("No,", "As I said").
	RuleReplyOpener Rule = "reply-opener"
	// RuleJudgmentAsFact reports a recommendation in a reply stated as a fact,
	// with no reason given.
	RuleJudgmentAsFact Rule = "judgment-as-fact"
	// RuleStackedHedge reports two softeners in one sentence of a reply, or an
	// apology before its point.
	RuleStackedHedge Rule = "stacked-hedge"
	// RuleLongThread reports a reply that is its author's fourth or later in a
	// thread, where a call settles faster than another reply.
	RuleLongThread Rule = "long-thread"
)

var (
	pullRequestOnly = []Kind{KindChangeDescription}
	replyOnly       = []Kind{KindReviewReply}
	// agentTone is toneKinds and an agent's reply. verdict and absolute stay off
	// the reply: over 400 sampled replies they fired on 40 and 73, nearly all a
	// status ("nothing pushed", "the test was wrong") rather than a posture.
	agentTone = slices.Concat(toneKinds, agentReply)
)

// toneChecks are the rules for posture: blame, verdicts, absolutes, intent,
// credit, claims without evidence and the review-reply rules. They run after
// [coreChecks]. A rule whose words have senses it cannot tell apart from the
// one it means advises: of these, only blame and reply-opener measured no
// false positive over the last 200 merged pull requests.
var toneChecks = []check{
	{rule: RuleBlame, on: agentTone, advise: agentReply, judge: ownWords(blame)},
	{rule: RuleVerdict, on: toneKinds, advise: toneKinds, judge: ownWords(verdict)},
	{rule: RuleAbsolute, on: toneKinds, advise: toneKinds, judge: absolute},
	{rule: RuleIntent, on: agentTone, advise: agentTone, judge: ownWords(intent)},
	{rule: RuleCredit, on: pullRequestOnly, advise: pullRequestOnly, judge: creditEarlier},
	{rule: RuleClaim, on: agentTone, advise: agentTone, judge: claim},
	{rule: RuleReplyOpener, on: replyOnly, judge: replyOpening},
	{rule: RuleJudgmentAsFact, on: replyOnly, advise: replyOnly, judge: judgmentAsFact},
	{rule: RuleStackedHedge, on: replyOnly, advise: replyOnly, judge: stackedHedge},
	{rule: RuleLongThread, on: replyOnly, advise: replyOnly, judge: longThread},
}

// matchFindings reports each span pattern finds in the prose of in, less those exempt
// rejects, with message formatted around the match.
func matchFindings(in input, pattern *regexp.Regexp, exempt func(text string, at []int) bool, message string) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, at := range pattern.FindAllStringSubmatchIndex(para.text, -1) {
			if exempt != nil && exempt(para.text, at) {
				continue
			}

			m := para.text[at[0]:at[1]]
			out = append(out, Finding{Message: fmt.Sprintf(message, m), Match: m, Line: para.lineAt(at[0])})
		}
	}

	return out
}

// quotedElement opens or closes an element a pasted issue, comment or prompt
// arrives in.
var quotedElement = regexp.MustCompile(`<(/?)(?:issue_description|issue_title|comment_new|comment_old|comments|` +
	`agent_instructions)\b[^>]*>`)

// quotesOthers are the kinds whose blockquote holds another author's words. A
// page's blockquote is its own callout.
var quotesOthers = slices.Concat(toneKinds, agentReply)

// quotedLines reports, for each line of in.source, whether it holds another
// author's words: a blockquote, or a line inside a [quotedElement].
func quotedLines(in input) []bool {
	out := make([]bool, len(in.source))
	blockquote := slices.Contains(quotesOthers, in.kind)
	depth := 0

	for i, ln := range in.source {
		out[i] = depth > 0 || (blockquote && quoteMarker.MatchString(ln) && strings.TrimSpace(ln) != "")

		for _, m := range quotedElement.FindAllStringSubmatch(ln, -1) {
			if m[1] == "" {
				depth++
				out[i] = true
			} else if depth > 0 {
				depth--
			}
		}
	}

	return out
}

// ownWords drops each finding of judge on a line that quotes another author:
// the writer is answerable only for their own words.
func ownWords(judge func(in input) []Finding) func(in input) []Finding {
	return func(in input) []Finding { return unquoted(in, judge(in)) }
}

// unquoted drops each of found, judged from in, on a line that quotes another
// author.
func unquoted(in input, found []Finding) []Finding {
	quoted := quotedLines(in)

	return slices.DeleteFunc(found, func(f Finding) bool {
		return f.Line > 0 && f.Line <= len(quoted) && quoted[f.Line-1]
	})
}

// singleQuoted reports whether offset at of text sits inside a span opened by a
// single quote after a space or bracket and closed by one before a space or
// punctuation, as in 'NEVER CANCEL'. An apostrophe inside a word opens and
// closes nothing.
func singleQuoted(text string, at int) bool {
	open := -1

	for i := 0; i < len(text); i++ {
		if text[i] != '\'' {
			continue
		}

		before := i == 0 || strings.ContainsRune(" ([{", rune(text[i-1]))
		after := i+1 == len(text) || strings.ContainsRune(" .,;:!?)]}", rune(text[i+1]))

		switch {
		case open < 0 && before && !after:
			open = i
		case open >= 0 && after && !before:
			if open < at && at < i {
				return true
			}

			open = -1
		}
	}

	return false
}

// sentenceBefore is the part of text's sentence that precedes offset at.
func sentenceBefore(text string, at int) string {
	return text[sentenceAt(text, at):at]
}

// sentenceHolding is the whole sentence of text that holds offset at.
func sentenceHolding(text string, at int) string {
	for _, b := range sentenceBounds(text) {
		if b[0] <= at && at < b[1] {
			return text[b[0]:b[1]]
		}
	}

	return text
}

// parenthesized reports whether offset at of text sits inside parentheses.
func parenthesized(text string, at int) bool {
	return strings.Count(text[:at], "(") > strings.Count(text[:at], ")")
}

// predicative are the words before an adjective that says something of its
// subject ("the fix was naive", "read as sloppy") rather than naming a kind of
// thing ("a naive tail", "the wrong checkout").
var predicative = wordSet("is", "are", "was", "were", "be", "been", "being", "as", "got", "get", "gets",
	"became", "seems", "seemed", "looks", "looked", "s", "re")

var (
	// counterfactual is a person, or a numbered piece of work, as the subject
	// of what it should have done. "The commit failed to apply" is a mechanism,
	// so only a person fails to. "should have" counts only before a past
	// participle: "you should have a tests.ts file" is possession, "should have
	// to" an obligation, and "should have been" a passive whose subject is
	// nearly always a thing ("dropped when they should have been kept"). "they"
	// names files and rows as often as people, so it counts only when it
	// forgot or neglected to.
	counterfactual = regexp.MustCompile(`(?i)\b(?:you|he|she|someone|somebody|the (?:original )?author|` +
		`the reviewer)\s+(?:should(?:'ve| have)\s+` + participle + `|failed to|forgot to|neglected to)\b|` +
		`\bthey\s+(?:forgot|neglected) to\b|(?:#\d+|\b(?:the|that|this) (?:PR|pull request|commit|squash))\s+` +
		`(?:should(?:'ve| have)\s+` + participle + `|forgot to|neglected to)\b|` +
		`\bwhoever (?:wrote|added|made|built|left)\b`)

	// contempt judges the people behind code or a decision. "lazy" is left
	// out: in all 7 labeled firings it named evaluation on demand ("lazy
	// decoding", "the computation is lazy").
	contempt = regexp.MustCompile(`(?i)\b(?:sloppy|sloppily|incompetent(?:ly)?|stupid|idiotic|moronic|braindead|` +
		`crazy|insane(?:ly)?|dumb|naive|careless)\b`)

	// technique are the contempt words that also name a technique or a role
	// before a noun: "a naive tail", "the careless caller", "a dumb base
	// implementation".
	technique = wordSet("naive", "careless", "dumb")

	// overdoing are the words before a "crazy" that means overdoing it: "go
	// too crazy", "how crazy we want to go".
	overdoing = wordSet("go", "goes", "going", "went", "gone", "too", "how")

	// denied ends the part of a sentence before a contempt word that denies
	// it: "Not crazy:", "it is not crazy", "rather than sloppy", "not because
	// anyone was careless". "not doing stupid pipelines" still counts.
	denied = regexp.MustCompile(`(?i)(?:^\W*not|\b(?:is|are|was|were|be|it's|that's) not|\b(?:isn't|aren't|wasn't|` +
		`weren't)|\brather than)\s+$|\bnot because\b`)

	// ownFault is a writer owning the fault a contempt word names: "sloppy
	// phrasing on my part", "careless; I should have worked on a copy".
	ownFault = regexp.MustCompile(`(?i)\b(?:my|on me|I should(?:'ve| have))\b`)

	// hypothetical are the words before a counterfactual's subject that make
	// it a supposed case or its object: "if they forgot to", "to whoever wrote
	// the review".
	hypothetical = wordSet("if", "when", "unless", "to")
)

// participle is a past participle after "should have": a regular one, or an
// irregular one a blame names. "been" is left out.
const participle = `(?:\w+ed|done|made|run|put|set|kept|left|caught|thought|known|sent|built|written|told|taken|` +
	`given|chosen|seen|read|got|gotten|brought|found|held|said|shown|split|spent|understood|hit|cut|let|` +
	`thrown|forgotten|spoken|begun)`

// blame reports a person or past work as the subject of a fault and words of
// contempt for code or a decision. A writer who means "I" or "we" owns the
// fault, which reads as candor, so the first person is left alone.
//
// It judges only text written to reviewers: a page names its reader's possible
// mistakes ("a variable you forgot to unset") and argues in an essay's voice.
func blame(in input) []Finding {
	const message = "Describe what happened instead of '%s', as in 'the rename left the old key' rather than " +
		"'someone forgot to update the key'."

	out := matchFindings(in, counterfactual, func(text string, at []int) bool {
		return hypothetical[prevWord(text, at[0])] || singleQuoted(text, at[0])
	}, message)
	out = append(out, matchFindings(in, contempt, contemptExempt, message)...)

	return out
}

// contemptExempt reports a contempt word used for something other than
// contempt: a technique before a noun, part of a name ("crazy-max",
// "dumb-init", "TERM=dumb"), a capitalized word inside a sentence (a title,
// "Keep It Simple, Stupid"), "crazy" as overdoing it, a word the sentence
// denies, and a fault the writer owns.
func contemptExempt(text string, at []int) bool {
	word := text[at[0]:at[1]]
	lower := strings.ToLower(word)
	prev := prevWord(text, at[0])
	before := sentenceBefore(text, at[0])

	switch {
	case denied.MatchString(before), ownFault.MatchString(sentenceHolding(text, at[0])):
		return true
	case at[0] > 0 && strings.ContainsRune("-/=_@.", rune(text[at[0]-1])),
		at[1] < len(text) && strings.ContainsRune("-/_", rune(text[at[1]])):
		return true
	case unicode.IsUpper(rune(word[0])) && midSentence(text, at[0]):
		return true
	case lower == "crazy" && overdoing[prev]:
		return true
	}

	return technique[lower] && !predicative[prev]
}

// midSentence reports whether the text before at ends in a word or a comma
// rather than a sentence end, a bracket or emphasis.
func midSentence(text string, at int) bool {
	before := strings.TrimRight(text[:at], " ")
	if before == "" {
		return false
	}

	last := rune(before[len(before)-1])

	return unicode.IsLetter(last) || unicode.IsDigit(last) || last == ','
}

var (
	verdictWord = regexp.MustCompile(`(?i)\b(?:broken|wrong|bad|messy|hacky|ugly|terrible|horrible|awful|` +
		`nightmare|mess|garbage|ridiculous)\b`)

	// selecting are the verdict words that pick out a thing before a noun ("a
	// broken test", "the wrong checkout", "a bad plan") rather than judge it.
	selecting = wordSet("broken", "wrong", "bad")

	// verdictCause names the behavior beside a verdict word where
	// [reasonGiven] does not: "wrong whenever the fields differ", "causes the
	// path to be wrong".
	verdictCause = regexp.MustCompile(`(?i)\b(?:whenever|until|while|causes?|caused|more often than)\b`)

	// literalState is a subject whose verdict word states a plain fact, a
	// broken test or link, or a test's status ("marked as broken").
	literalState = regexp.MustCompile(`(?i)\b(?:tests?|links?|mark(?:ed|s|ing)?)\b`)

	// verdictNegated ends the words before a verdict that deny it: "ensure
	// nothing was broken", "not garbage".
	verdictNegated = regexp.MustCompile(`(?i)\b(?:nothing|no|not|none)\b[^.,;:]{0,40}$`)

	// supposed are the words before a "be" that make a verdict a possible or
	// later state: "may be broken", "will be wrong until it is tagged".
	supposed = wordSet("may", "might", "could", "would", "will", "should")

	// verbParticle follows a verdict word used as a verb: "broken into lines",
	// "mess around", "mess with the service".
	verbParticle = regexp.MustCompile(`^\s+(?:into|up|down|out|off|around|with)\b`)
)

// verdict reports a judgment word about earlier work, which tells the reader
// what to think in place of what happened. A verdict with the behavior beside
// it, in its sentence or a later one of its paragraph, is left alone: the
// reader has the facts.
func verdict(in input) []Finding {
	return matchFindings(in, verdictWord, verdictExempt, "Name the behavior '%s' stands for, as in 'the cache "+
		"kept the old key after a rename' rather than 'the cache was broken'.")
}

func verdictExempt(text string, at []int) bool {
	w := strings.ToLower(text[at[0]:at[1]])
	prev := prevWord(text, at[0])
	before := sentenceBefore(text, at[0])
	sentence := sentenceHolding(text, at[0])
	words := strings.Fields(strings.ToLower(before))

	switch {
	case at[0] > 0 && text[at[0]-1] == '-', at[1] < len(text) && text[at[1]] == '-':
		return true
	case selecting[w] && !predicative[prev]:
		return true
	case w == "garbage" && strings.HasPrefix(text[at[1]:], " collect"):
		return true
	case (w == "broken" || w == "mess") && prev != "a" && prev != "the" && verbParticle.MatchString(text[at[1]:]):
		return true
	case reasonGiven.MatchString(sentence), verdictCause.MatchString(sentence):
		return true
	case literalState.MatchString(before), verdictNegated.MatchString(before):
		return true
	case len(words) >= 2 && words[len(words)-1] == "be" && supposed[words[len(words)-2]]:
		return true
	}

	return sentenceAt(text, at[0]) != sentenceAt(text, len(text)-1)
}

var (
	// pastAbsolute is never, nobody or nothing with a verb in the past tense.
	// Each group holds the verb.
	pastAbsolute = regexp.MustCompile(`(?i)\b(?:has|have|had) never\b|` +
		`\bnever (?:once |ever )?(\w+ed|ran|saw|got|was|were|did|knew|broke|wrote|found|caught|made|took|hit)\b|` +
		`\b(nobody|no one|nothing) (?:ever )?(\w+ed|ran|saw|got|did|knew|broke|wrote|found|caught|made|took|hit)\b`)

	// edPresent end in "ed" and are not in the past tense.
	edPresent = wordSet("need", "feed", "speed", "seed", "proceed", "succeed", "exceed", "bleed", "breed", "embed",
		"shed")

	// contract are the words before "never" that make it a statement of what
	// the code does ("is never used", "can never fire").
	contract = wordSet("is", "are", "be", "am", "s", "re", "can", "will", "may", "must", "should", "would", "could",
		"does", "do")

	// codeVerbs are said of what code does with code or data, not of
	// anyone's work: "the field was never used", "a visitor never called",
	// "a version that never existed". Each is something a reader can check.
	codeVerbs = wordSet("used", "called", "referenced", "invoked", "exported", "imported", "read", "executed",
		"populated", "cached", "tracked", "existed", "instantiated", "initialized", "assigned", "accessed")

	// mechanismAfter is a condition after a "never", which makes it a bug's
	// mechanism: "never fired when the chain failed". A "because" is left
	// out: "never fired after a rename, because ..." still dates a history.
	mechanismAfter = regexp.MustCompile(`(?i)\b(?:when|whenever)\b`)

	// supposedOrContrasted opens a clause that supposes the absolute ("if the
	// listener never fired") or sets it against what the code did do
	// ("created but never used").
	supposedOrContrasted = regexp.MustCompile(`(?i)\b(?:if|unless|but)\b`)

	// firstPersonBefore is the writer as the subject: "I've never used it".
	firstPersonBefore = regexp.MustCompile(`(?i)\b(?:I|we)(?:'ve|'d| have| had)?\s+$`)

	// nothingNoObject are the words after "nothing <verb>" that show the verb
	// has no object: "the directives nothing required", "nothing changed in
	// the definition". "Nothing checked it" names the work left undone.
	nothingNoObject = wordSet("in", "on", "at", "to", "for", "with", "from", "by", "or", "and", "but", "about",
		"here", "there", "yet", "since")
)

// absolute reports never, nobody or nothing about the past: an absolute over
// every run since a change reads as a verdict on whoever made it. "never
// returns nil" states a contract and is left alone, as is what code or data
// did or lacks (guideline: judge by the subject): a verb said of code, a
// supposed or contrasted case, a mechanism, a parenthetical note, the writer
// in the first person, and a "nothing" with no object.
func absolute(in input) []Finding {
	return unquoted(in, matchFindings(in, pastAbsolute, absoluteExempt,
		"Say when and how often in place of '%s', as in 'fired 0 times in 40 runs since #558'."))
}

func absoluteExempt(text string, at []int) bool {
	for _, g := range []int{2, 6} {
		if at[g] >= 0 && edPresent[strings.ToLower(text[at[g]:at[g+1]])] {
			return true
		}
	}

	before := sentenceBefore(text, at[0])
	if parenthesized(text, at[0]) || firstPersonBefore.MatchString(before) ||
		supposedOrContrasted.MatchString(before) {
		return true
	}

	if at[4] >= 0 {
		return strings.EqualFold(text[at[4]:at[5]], "nothing") && !hasObjectAfter(text[at[1]:])
	}

	if strings.HasPrefix(strings.ToLower(text[at[0]:]), "never") && contract[prevWord(text, at[0])] {
		return true
	}

	after := text[at[1]:]

	for _, b := range sentenceBounds(text) {
		if b[0] <= at[0] && at[0] < b[1] {
			after = text[at[1]:max(at[1], b[1])]
		}
	}

	return codeVerbs[verbAfterNever(text[at[0]:])] || mechanismAfter.MatchString(after)
}

// verbAfterNever is the first word after "never" in rest, past "once",
// "ever" and "been", lowercased.
func verbAfterNever(rest string) string {
	i := strings.Index(strings.ToLower(rest), "never")
	if i < 0 {
		return ""
	}

	for _, w := range strings.Fields(rest[i+len("never"):]) {
		w = strings.ToLower(strings.Trim(w, ".,;:!?()\"'"))
		if w != "once" && w != "ever" && w != "been" {
			return w
		}
	}

	return ""
}

// hasObjectAfter reports whether rest, the text after "nothing <verb>", opens
// with the verb's object.
func hasObjectAfter(rest string) bool {
	next := leadingWord(strings.TrimLeft(rest, " "))

	return next != "" && !nothingNoObject[strings.ToLower(next)]
}

var (
	// motive leaves out "don't care" and "do not care": in all 8 labeled
	// firings the subject was the writer, their team or a user stating a
	// preference ("we don't care about windows support").
	motive = regexp.MustCompile(`(?i)\b(?:pretend(?:s|ed|ing)?|lie(?:s|d)?|lying|guess(?:es|ed)|` +
		`(?:doesn't|does not|didn't|did not) care|hat(?:es|ed)|` +
		`refus(?:es|ed) to (?:understand|listen|accept|admit|believe|learn))\b`)

	// liesWhere follows a "lies" that places something: "the cause lies in".
	liesWhere = regexp.MustCompile(`^ (?:in|within|between|at|on|outside|beyond|with|behind|ahead|under|` +
		`elsewhere|here|there|upstream|downstream|flat)\b`)

	// lyingObject follows a "lying" that means laying a thing down: "lying the
	// phone flat".
	lyingObject = regexp.MustCompile(`^ (?:the|a|an|it|its)\b`)

	// clauseEnd follows a word that ends its clause.
	clauseEnd = regexp.MustCompile(`^\s*(?:[.,;:!?)]|$)`)

	// placing opens a clause whose closing "lies" places something: "where
	// the real issue lies".
	placing = regexp.MustCompile(`(?i)\bwhere\b`)

	// guessedClause follows a "guessed" that is a verb with an object or a
	// clause ("guessed where it ran", "guessed the ID"), not an adjective
	// before a noun ("the guessed page", "propagate guessed map types").
	guessedClause = regexp.MustCompile(`^\s*(?:$|[.,;:!?)]|(?:where|what|which|that|how|whether|the|a|an|at|it|its|` +
		`wrong|right|from)\b)`)

	// indifferent are the subjects of a "doesn't care" that states a person's
	// preference rather than give a tool a motive.
	indifferent = wordSet("i", "we", "you", "they", "user", "users", "people")

	// firstPersonAny is the writer, alone or with their team.
	firstPersonAny = regexp.MustCompile(`(?i)\b(?:i|i'm|i've|i'd|my|me|we|our|us)\b`)
)

// intent reports a motive given to a tool or a person. A tool has a mechanism,
// and a person given a motive in writing reads it as an accusation. The writer
// owning a motive ("my first command guessed", "we should not pretend") is
// candor, and is left alone.
func intent(in input) []Finding {
	return matchFindings(in, motive, intentExempt, "Describe the mechanism in place of '%s', as in 'magus read "+
		"the platform from the first binary on PATH' rather than 'magus guessed'.")
}

func intentExempt(text string, at []int) bool {
	word := strings.ToLower(text[at[0]:at[1]])
	rest := text[at[1]:]
	before := sentenceBefore(text, at[0])

	switch {
	case at[0] > 0 && strings.ContainsRune("-/_.", rune(text[at[0]-1])),
		at[1] < len(text) && strings.ContainsRune("-/_.", rune(text[at[1]])) && !clauseEnd.MatchString(rest):
		return true
	case firstPersonAny.MatchString(before):
		return true
	case strings.HasPrefix(word, "li") && liesWhere.MatchString(rest),
		strings.HasPrefix(word, "lie") && clauseEnd.MatchString(rest) && placing.MatchString(before),
		word == "lying" && (liesWhere.MatchString(rest) || lyingObject.MatchString(rest)):
		return true
	case word == "guessed":
		return !guessedClause.MatchString(rest)
	case strings.HasSuffix(word, "care"):
		return indifferent[prevWord(text, at[0])]
	case strings.HasPrefix(text[at[0]:at[1]], "Pretend"):
		return strings.TrimLeft(before, " *_(") == ""
	}

	return false
}

var (
	// removal opens a clause that removes or replaces something.
	removal = regexp.MustCompile(`^(?:[Rr]emov(?:e|es)|[Dd]rops?|[Dd]elet(?:e|es)|[Rr]eplac(?:e|es)|` +
		`[Rr]etir(?:e|es)|[Ss]upersed(?:e|es)|[Rr]ips? out)\b`)

	// creditGiven says what something was for or did well.
	creditGiven = regexp.MustCompile(`(?i)\b(?:(?:was|were) (?:for|built (?:for|to)|meant (?:for|to)|` +
		`there (?:for|to)|(?:added|introduced|written) (?:for|to|so|when|because)|right|a good fit|` +
		`the right call)|suited|served|existed (?:to|for|so)|made sense|worked (?:well|while|when|for)|` +
		`did (?:well|its job)|at the time|good fit)\b`)

	// conventionalType is a conventional commit subject's type and scope.
	conventionalType = regexp.MustCompile(`^\w+(?:\([^)]*\))?!?:\s*`)
)

// creditEarlier reports a pull request that removes or replaces something and says
// nothing of what it was for. Measured over the last 200 merged pull
// requests, 16 removed something by a clause of the title or the opening of
// a sentence, and none said what it had been for.
func creditEarlier(in input) []Finding {
	paras := paragraphs(in.prose, mentionsMasked)

	var found *Finding

	for _, para := range paras {
		if creditGiven.MatchString(para.text) {
			return nil
		}

		if found != nil || para.head.heading {
			continue
		}

		var opens []int

		if para.head.line == 1 {
			title := conventionalType.FindStringIndex(para.text)
			start := 0

			if title != nil {
				start = title[1]
			}

			opens = append(opens, start)

			for i := start; i < len(para.text); i++ {
				if para.text[i] == ';' {
					opens = append(opens, i+1+len(para.text[i+1:])-len(strings.TrimLeft(para.text[i+1:], " ")))
				}
			}
		} else {
			for _, s := range sentenceStarts(para.text) {
				opens = append(opens, s+len(para.text[s:])-len(strings.TrimLeft(para.text[s:], "*_(")))
			}
		}

		for _, s := range opens {
			if m := removal.FindString(para.text[s:]); m != "" {
				found = &Finding{
					Message: fmt.Sprintf("Say what the earlier design was for beside '%s', in one clause, as in "+
						"'A per-process cache suited one worker; it stops holding at eight.'", m),
					Match: m, Line: para.lineAt(s),
				}

				break
			}
		}
	}

	if found == nil {
		return nil
	}

	return []Finding{*found}
}

var (
	// measurement is a number with a unit of time or size, a percentage, a
	// ratio ("3x") or "N of M".
	measurement = regexp.MustCompile(`\b\d+(?:\.\d+)?(?:\s?(?:ms|s|secs?|seconds?|minutes?|mins?|h|hours?|us|µs|` +
		`ns|[KMGT]i?B|kB|bytes?)\b|x\b|%|\s?percent\b)|\b\d+ of (?:the |its |their )?\d+\b`)

	comparison = regexp.MustCompile(`(?i)\b(?:faster|slower|smaller|fewer|cheaper|quicker|more reliable)\b`)

	completion = regexp.MustCompile(`(?i)\b(?:fixes|eliminates|prevents|guarantees|no longer flakes?)\b`)

	// citation names an issue, a pull request or a magus output ref.
	citation = regexp.MustCompile(`#\d+\b|\b(?:grd|out)[0-9a-f]{8,}\b`)

	// commitHash is a run of hex that names a commit when it holds a digit.
	commitHash = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

	// linked is a link as written, before its target is blanked.
	linked = regexp.MustCompile(`https?://|\]\(|\]\[`)
)

// claim reports a measurement, a comparison or a completion with no evidence
// in the same sentence, or in the same list item: a code span, a link, an
// issue or pull request, a commit or a magus output ref. A sentence that
// states a limit ("Not measured on Linux") is exempt.
//
// It advises: over the last 200 merged pull requests it fired in 21, and
// about half of those state a setting ("a 300s bound") rather than measure.
func claim(in input) []Finding {
	const message = "Put the evidence for '%s' in the same sentence or bullet: the test or command in backticks, " +
		"a link, an issue or commit, or a run's output ref, as in 'Cold `magus ls` drops from 410ms to 260ms " +
		"(5 runs each, `hack/bench/startup.buzz`).'"

	masked := paragraphs(in.prose, mentionsMasked)
	plain := paragraphs(in.prose, keep)
	limits := limitSections(masked)

	var out []Finding

	for i, para := range masked {
		if para.head.heading || limits[i] || (titled(in.kind) && para.head.line == 1) {
			continue
		}

		for _, span := range claimSpans(para) {
			text := para.text[span[0]:span[1]]

			at := firstClaim(text)
			if at == nil || statesLimit(para, false, span[0]+at[0]) ||
				cited(plain[i].text[span[0]:span[1]], in.source, para.lineAt(span[0]), para.lineAt(span[1]-1)) {
				continue
			}

			m := text[at[0]:at[1]]
			out = append(out, Finding{Message: fmt.Sprintf(message, m), Match: m, Line: para.lineAt(span[0] + at[0])})
		}
	}

	return out
}

// claimSpans are where a claim's evidence may sit: the whole of a list item,
// or one sentence of any other paragraph.
func claimSpans(para paragraph) [][2]int {
	if para.head.item {
		return [][2]int{{0, len(para.text)}}
	}

	return sentenceBounds(para.text)
}

// sentenceBounds returns where each sentence of text starts and ends.
func sentenceBounds(text string) [][2]int {
	starts := sentenceStarts(text)
	out := make([][2]int, len(starts))

	for i, s := range starts {
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1]
		}

		out[i] = [2]int{s, end}
	}

	return out
}

// firstClaim returns where the first claim in text sits, or nil. A "fixes"
// with no object is the noun ("five nesting fixes"), not a claim, and a
// figure that states a setting or is derived from counts beside it is no
// measurement.
func firstClaim(text string) []int {
	var first []int

	for _, re := range []*regexp.Regexp{measurement, comparison, completion} {
		for _, at := range re.FindAllStringIndex(text, -1) {
			switch {
			case re == completion && !hasObject(text[at[1]:]),
				re == measurement && notMeasured(text, at),
				re == comparison && (parenthesized(text, at[0]) || purposeBefore.MatchString(text[:at[0]])):
				continue
			}

			if first == nil || at[0] < first[0] {
				first = at
			}

			break
		}
	}

	return first
}

var (
	// settingBefore is what makes the figure after it a setting or a size the
	// writer chose rather than a result: a determiner ("a 300s bound", "the
	// 5x multiplier", "a fixed 60ms"), a limit's noun ("a limit of 40%"), a
	// supposition ("if 16 bytes is enough").
	settingBefore = regexp.MustCompile(`(?i)(?:\b(?:a|an|the|this|that|each|every|per|its|their)` +
		`(?:\s+(?:fixed|default|hard|flat|single|new|maximum|minimum))?|` +
		`\b(?:limit|bound|cap|threshold|budget|ceiling|deadline|timeout|maximum|minimum) of|\b(?:if|whether))\s*$`)

	// certaintyAfter makes a percentage the writer's confidence: "99% sure".
	certaintyAfter = regexp.MustCompile(`^\s*(?:sure|certain|confident)\b`)

	// countedChange is a before and after count of lines or files, which a
	// reader checks in the diff, so a ratio beside it is derived from them:
	// "620→213 lines (70% reduction)".
	countedChange = regexp.MustCompile(`\d+\s*(?:→|->)\s*\d+\s+(?:lines?|files?)\b`)

	// purposeBefore puts a comparison in a design's purpose, not its result:
	// "to keep the generated code smaller".
	purposeBefore = regexp.MustCompile(`(?i)\bto (?:keep|make|stay|remain)\b[^.;:,]*$`)

	// notAnObject are the words after a "fixes" that show it is the noun:
	// "key fixes in", "partial fixes to", "menu fixes and".
	notAnObject = wordSet("in", "to", "and", "or", "for", "from", "of", "with", "on", "at", "that", "which", "are",
		"were", "is", "was")
)

// notMeasured reports whether the measurement at at in text states a setting,
// a confidence or a ratio derived from counts beside it, or quotes a figure.
func notMeasured(text string, at []int) bool {
	before := text[:at[0]]
	quoted := strings.Count(before, "“") > strings.Count(before, "”") || strings.Count(before, `"`)%2 == 1

	return quoted || settingBefore.MatchString(before) || certaintyAfter.MatchString(text[at[1]:]) ||
		countedChange.MatchString(text)
}

func hasObject(rest string) bool {
	rest = strings.TrimLeft(rest, " ")
	if rest == "" || !(unicode.IsLetter(rune(rest[0])) || rest[0] == '#' || rest[0] == '`') {
		return false
	}

	return !notAnObject[strings.ToLower(leadingWord(rest))]
}

// cited reports evidence in text, the claim's sentence or item as written,
// or a link on its source lines from first to last.
func cited(text string, source []string, first, last int) bool {
	if strings.Contains(text, "`") || citation.MatchString(text) {
		return true
	}

	for _, h := range commitHash.FindAllString(text, -1) {
		if strings.ContainsAny(h, "0123456789") {
			return true
		}
	}

	for l := first; l <= last && l <= len(source); l++ {
		if l > 0 && linked.MatchString(source[l-1]) {
			return true
		}
	}

	return false
}

// contradiction opens a sentence by telling the other person they are wrong.
// "Actually," is left to filler, which reports it in a reply too.
// "Wrong" counts only as an exclamation: before a noun it labels a finding
// ("Wrong indentation", "**Wrong config key**").
var contradiction = regexp.MustCompile(`^(?:No,|Wrong(?:[,.!]|$)|As I (?:said|mentioned|wrote)\b|Again,|` +
	`Like I said\b)`)

// conceded is a "No," that agrees with the other person: "No, this wasn't
// intentional, good catch."
var conceded = regexp.MustCompile(`(?i)\bgood catch\b|\byou(?:'re| are) (?:right|correct)\b`)

// replyOpening reports a sentence of a reply that opens by contradicting,
// which reads as winning an argument whatever follows it. A "No," answering
// the question the reply quotes just above it is an answer, not a
// contradiction.
func replyOpening(in input) []Finding {
	var out []Finding

	quoted := quotedLines(in)

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		answers := answersQuestion(in.source, quoted, para.head.line)

		for _, s := range sentenceStarts(para.text) {
			m := contradiction.FindString(strings.TrimLeft(para.text[s:], "*_("))
			if m == "No," && ((answers && s == 0) || conceded.MatchString(sentenceHolding(para.text, s))) {
				continue
			}

			if strings.HasPrefix(m, "Wrong") {
				m = "Wrong"
			}

			if m != "" {
				out = append(out, Finding{
					Message: fmt.Sprintf("Drop '%s' and open with the fact and its evidence, as in 'This needs a "+
						"lock: the map is written from two goroutines.'", m),
					Match: m, Line: para.lineAt(s),
				})
			}
		}
	}

	return out
}

// answersQuestion reports whether the last non-blank line of source before
// line, 1-based, quotes a question.
func answersQuestion(source []string, quoted []bool, line int) bool {
	for i := line - 2; i >= 0 && i < len(source); i-- {
		if strings.TrimSpace(source[i]) != "" {
			return quoted[i] && strings.HasSuffix(strings.TrimSpace(source[i]), "?")
		}
	}

	return false
}

var (
	recommendation = regexp.MustCompile(`\b(?:[Ss]hould(?: be)?|[Nn]eeds? to(?: be)?|[Mm]ust(?: be)?)\b`)

	// reasonGiven is a clause that gives the reason for a recommendation.
	reasonGiven = regexp.MustCompile(`[:;]|\b(?:because|since|so|as|otherwise|given|if|unless|when|which|` +
		`to avoid|to keep)\b`)

	// ownedCall labels a recommendation as the writer's own call.
	ownedCall = regexp.MustCompile(`\bI(?:'d| would| think| prefer| suggest| guess| don't think| do not think|` +
		`'m (?:fairly |pretty )?(?:sure|certain)| will admit)\b`)

	// groundGiven names the purpose or the authority behind a recommendation,
	// which [reasonGiven] leaves out: "to match the guidelines", "per the
	// spec", "for consistency".
	groundGiven = regexp.MustCompile(`(?i)\b(?:to (?:match|ensure|explain|make sure|prevent)|according to|per|` +
		`based on|for consistency)\b`)
)

// judgmentAsFact reports a recommendation stated as a fact with no reason:
// "This should be a map." A question, a recommendation with its reason, and
// one labelled as the writer's call are left alone. So is one in a reply of
// more than one sentence, whose other sentences state the facts it rests on.
// A capitalized "Should" inside a sentence names something ("Should -Throw"),
// "would need" supposes, and a modal in parentheses or single quotes states
// an expectation or quotes one.
func judgmentAsFact(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		spans := sentenceBounds(para.text)
		if len(spans) > 1 {
			continue
		}

		for _, span := range spans {
			text := strings.TrimSpace(para.text[span[0]:span[1]])

			at := recommendation.FindStringIndex(text)
			if at == nil || strings.HasSuffix(text, "?") || reasonGiven.MatchString(text) ||
				ownedCall.MatchString(text) || groundGiven.MatchString(text) ||
				(at[0] > 0 && text[at[0]] == 'S') || prevWord(text, at[0]) == "would" ||
				parenthesized(text, at[0]) || singleQuoted(text, at[0]) {
				continue
			}

			m := text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Give the reason beside '%s', or say it is your call, as in 'I'd use a map "+
					"here: lookups dominate. Open to keeping the slice if order matters.'", m),
				Match: m, Line: para.lineAt(span[0]),
			})
		}
	}

	return out
}

var (
	softener = regexp.MustCompile(`(?i)\b(?:I (?:could|might) be wrong|I'm not sure|not sure|I think|I guess|` +
		`I feel like|maybe|perhaps|possibly|probably|might|could|sort of|kind of|a bit|a little|somewhat)\b`)

	apology = regexp.MustCompile(`^(?:Sorry|Apologies|I'm sorry|I am sorry)(?:,| if\b| but\b| to\b)`)
)

// stackedHedge reports a sentence with two or more softeners, which reads as
// unsure of a point the writer has, and an apology before the point.
func stackedHedge(in input) []Finding {
	const instead = "as in 'This needs a lock: the map is written from two goroutines.'"

	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, span := range sentenceBounds(para.text) {
			text := para.text[span[0]:span[1]]
			lead := strings.TrimLeft(text, "*_( ")

			if m := apology.FindString(lead); m != "" {
				out = append(out, Finding{
					Message: fmt.Sprintf("Drop the apology '%s' and open with the point, %s", m, instead),
					Match:   m, Line: para.lineAt(span[0]),
				})

				continue
			}

			soft := softener.FindAllStringIndex(text, -1)
			if len(soft) < 2 {
				continue
			}

			m := text[soft[0][0]:soft[1][1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop the softeners in '%s': state the point once, and how sure you are, %s",
					m, instead),
				Match: m, Line: para.lineAt(span[0] + soft[0][0]),
			})
		}
	}

	return out
}

// longThreadAfter is how many replies by one author a thread holds before the
// next one is better spent on a call.
const longThreadAfter = 3

// longThread reports a reply that is its author's fourth or later in the
// thread, by the count [WithThreadLength] passes. A long exchange in text reads
// as a stalemate to the people watching it.
func longThread(in input) []Finding {
	if in.opts.threadLength < longThreadAfter {
		return nil
	}

	return []Finding{{Message: fmt.Sprintf("This is reply %d from you in the thread: offer a call to settle it, "+
		"as in 'Want to talk this through for ten minutes?'", in.opts.threadLength+1)}}
}
