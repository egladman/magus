package prose

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// The rules in this file judge posture in text written to the people working
// on a change: a pull request and a reply in its review. Text loses its tone on
// the way to a reader, who fills the gap with intent the writer never had, so
// each rule names the posture it catches and how to state the same fact.
// docs/decisions/0008-writing-a-teammate-reads.md records the evidence.

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
)

// toneChecks are the rules for posture: blame, verdicts, absolutes, intent,
// credit, claims without evidence and the review-reply rules. They run after
// [coreChecks]. A rule whose words have senses it cannot tell apart from the
// one it means advises: of these, only blame and reply-opener measured no
// false positive over the last 200 merged pull requests.
var toneChecks = []check{
	{rule: RuleBlame, on: teammate, judge: blame},
	{rule: RuleVerdict, on: teammate, advise: teammate, judge: verdict},
	{rule: RuleAbsolute, on: teammate, advise: teammate, judge: absolute},
	{rule: RuleIntent, on: teammate, advise: teammate, judge: intent},
	{rule: RuleCredit, on: pullRequestOnly, advise: pullRequestOnly, judge: creditEarlier},
	{rule: RuleClaim, on: teammate, advise: teammate, judge: claim},
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

// predicative are the words before an adjective that says something of its
// subject ("the fix was naive", "read as sloppy") rather than naming a kind of
// thing ("a naive tail", "the wrong checkout").
var predicative = wordSet("is", "are", "was", "were", "be", "been", "being", "as", "got", "get", "gets",
	"became", "seems", "seemed", "looks", "looked", "s", "re")

var (
	// counterfactual is a person, or a numbered piece of work, as the subject
	// of what it should have done. "The commit failed to apply" is a mechanism,
	// so only a person fails to.
	counterfactual = regexp.MustCompile(`(?i)\b(?:you|they|he|she|someone|somebody|the (?:original )?author|` +
		`the reviewer)\s+(?:should(?:'ve| have)|failed to|forgot to|neglected to)\b|(?:#\d+|\b(?:the|that|this) ` +
		`(?:PR|pull request|commit|squash))\s+(?:should(?:'ve| have)|forgot to|neglected to)\b|` +
		`\bwhoever (?:wrote|added|made|built|left)\b`)

	// contempt judges the people behind code or a decision.
	contempt = regexp.MustCompile(`(?i)\b(?:sloppy|sloppily|incompetent(?:ly)?|stupid|idiotic|moronic|braindead|` +
		`crazy|insane(?:ly)?|dumb|lazy|naive|careless)\b`)

	// technique are the contempt words that also name a technique or a role
	// before a noun: "a lazy fetch", "a naive tail", "the careless caller".
	technique = wordSet("lazy", "naive", "careless")
)

// blame reports a person or past work as the subject of a fault and words of
// contempt for code or a decision. A writer who means "I" or "we" owns the
// fault, which reads as candor, so the first person is left alone.
//
// It judges only text written to teammates: a page names its reader's possible
// mistakes ("a variable you forgot to unset") and argues in an essay's voice.
func blame(in input) []Finding {
	const message = "Describe what happened instead of '%s', as in 'the rename left the old key' rather than " +
		"'someone forgot to update the key'."

	out := matchFindings(in, counterfactual, nil, message)
	out = append(out, matchFindings(in, contempt, func(text string, at []int) bool {
		return technique[strings.ToLower(text[at[0]:at[1]])] && !predicative[prevWord(text, at[0])]
	}, message)...)

	return out
}

var (
	verdictWord = regexp.MustCompile(`(?i)\b(?:broken|wrong|bad|messy|hacky|ugly|terrible|horrible|awful|` +
		`nightmare|mess|garbage|ridiculous)\b`)

	// selecting are the verdict words that pick out a thing before a noun ("a
	// broken test", "the wrong checkout", "a bad plan") rather than judge it.
	selecting = wordSet("broken", "wrong", "bad")
)

// verdict reports a judgment word about earlier work, which tells the reader
// what to think in place of what happened.
func verdict(in input) []Finding {
	return matchFindings(in, verdictWord, func(text string, at []int) bool {
		w := strings.ToLower(text[at[0]:at[1]])

		return (selecting[w] && !predicative[prevWord(text, at[0])]) ||
			(w == "garbage" && strings.HasPrefix(text[at[1]:], " collect"))
	}, "Name the behavior '%s' stands for, as in 'the cache kept the old key after a rename' rather than "+
		"'the cache was broken'.")
}

var (
	// pastAbsolute is never, nobody or nothing with a verb in the past tense.
	// Each group holds the verb.
	pastAbsolute = regexp.MustCompile(`(?i)\b(?:has|have|had) never\b|` +
		`\bnever (?:once |ever )?(\w+ed|ran|saw|got|was|were|did|knew|broke|wrote|found|caught|made|took|hit)\b|` +
		`\b(?:nobody|no one|nothing) (?:ever )?(\w+ed|ran|saw|got|did|knew|broke|wrote|found|caught|made|took|hit)\b`)

	// edPresent end in "ed" and are not in the past tense.
	edPresent = wordSet("need", "feed", "speed", "seed", "proceed", "succeed", "exceed", "bleed", "breed", "embed",
		"shed")

	// contract are the words before "never" that make it a statement of what
	// the code does ("is never used", "can never fire").
	contract = wordSet("is", "are", "be", "am", "s", "re", "can", "will", "may", "must", "should", "would", "could",
		"does", "do")
)

// absolute reports never, nobody or nothing about the past: an absolute over
// every run since a change reads as a verdict on whoever made it. "never
// returns nil" states a contract and is left alone.
func absolute(in input) []Finding {
	return matchFindings(in, pastAbsolute, func(text string, at []int) bool {
		for g := 2; g+1 < len(at); g += 2 {
			if at[g] >= 0 && edPresent[strings.ToLower(text[at[g]:at[g+1]])] {
				return true
			}
		}

		return strings.HasPrefix(strings.ToLower(text[at[0]:]), "never") && contract[prevWord(text, at[0])]
	}, "Say when and how often in place of '%s', as in 'fired 0 times in 40 runs since #558'.")
}

var (
	motive = regexp.MustCompile(`(?i)\b(?:pretend(?:s|ed|ing)?|lie(?:s|d)?|lying|guess(?:es|ed)|` +
		`(?:doesn't|does not|didn't|did not|don't|do not) care|hat(?:es|ed)|` +
		`refus(?:es|ed) to (?:understand|listen|accept|admit|believe|learn))\b`)

	// liesWhere follows a "lies" that places something: "the cause lies in".
	liesWhere = regexp.MustCompile(`^ (?:in|within|between|at|on|outside|beyond|with|behind|ahead|under|` +
		`elsewhere|here|there|upstream|downstream|flat)\b`)
)

// intent reports a motive given to a tool or a person. A tool has a mechanism,
// and a person given a motive in writing reads it as an accusation.
func intent(in input) []Finding {
	return matchFindings(in, motive, func(text string, at []int) bool {
		return strings.HasPrefix(strings.ToLower(text[at[0]:at[1]]), "li") && liesWhere.MatchString(text[at[1]:])
	}, "Describe the mechanism in place of '%s', as in 'magus read the platform from the first binary on PATH' "+
		"rather than 'magus guessed'.")
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
		if para.head.heading || limits[i] || (in.kind == KindChangeDescription && para.head.line == 1) {
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
// with no object is the noun ("five nesting fixes"), not a claim.
func firstClaim(text string) []int {
	var first []int

	for _, re := range []*regexp.Regexp{measurement, comparison, completion} {
		for _, at := range re.FindAllStringIndex(text, -1) {
			if re == completion && !hasObject(text[at[1]:]) {
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

func hasObject(rest string) bool {
	rest = strings.TrimLeft(rest, " ")

	return rest != "" && (unicode.IsLetter(rune(rest[0])) || rest[0] == '#' || rest[0] == '`')
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
var contradiction = regexp.MustCompile(`^(?:No,|Wrong\b|As I (?:said|mentioned|wrote)\b|Again,|Like I said\b)`)

// replyOpening reports a sentence of a reply that opens by contradicting,
// which reads as winning an argument whatever follows it.
func replyOpening(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, s := range sentenceStarts(para.text) {
			if m := contradiction.FindString(strings.TrimLeft(para.text[s:], "*_(")); m != "" {
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

var (
	recommendation = regexp.MustCompile(`\b(?:[Ss]hould(?: be)?|[Nn]eeds? to(?: be)?|[Mm]ust(?: be)?)\b`)

	// reasonGiven is a clause that gives the reason for a recommendation.
	reasonGiven = regexp.MustCompile(`[:;]|\b(?:because|since|so|as|otherwise|given|if|unless|when|which|` +
		`to avoid|to keep)\b`)

	// ownedCall labels a recommendation as the writer's own call.
	ownedCall = regexp.MustCompile(`\bI(?:'d| would| think| prefer| suggest)\b`)
)

// judgmentAsFact reports a recommendation stated as a fact with no reason:
// "This should be a map." A question, a recommendation with its reason, and
// one labelled as the writer's call are left alone.
func judgmentAsFact(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, span := range sentenceBounds(para.text) {
			text := strings.TrimSpace(para.text[span[0]:span[1]])

			at := recommendation.FindStringIndex(text)
			if at == nil || strings.HasSuffix(text, "?") || reasonGiven.MatchString(text) || ownedCall.MatchString(text) {
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
