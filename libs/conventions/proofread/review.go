package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// RuleNonspecific reports a review reply that judges code or asks for a
	// change and names nothing to act on: no code, path, line, identifier,
	// example or reason.
	RuleNonspecific Rule = "nonspecific"
	// RuleWhyOpener reports a review reply sentence that opens by asking the
	// author why they did something ("Why did you").
	RuleWhyOpener Rule = "why-opener"
	// RuleBareImperative reports a short command in a review reply with no
	// reason anywhere in the reply ("Fix this.").
	RuleBareImperative Rule = "bare-imperative"
	// RuleAllCaps reports words written in capitals for emphasis in a review
	// reply.
	RuleAllCaps Rule = "all-caps"
	// RuleRepeatedMarks reports a run of question or exclamation marks in a
	// review reply ("??", "!!").
	RuleRepeatedMarks Rule = "repeated-marks"
)

var reviewChecks = []check{
	{rule: RuleNonspecific, on: replyOnly, advise: replyOnly, judge: nonspecific},
	{rule: RuleWhyOpener, on: replyOnly, advise: replyOnly, judge: whyOpener},
	{rule: RuleBareImperative, on: replyOnly, advise: replyOnly, judge: bareImperative},
	{rule: RuleAllCaps, on: replyOnly, advise: replyOnly, judge: allCaps},
	{rule: RuleRepeatedMarks, on: replyOnly, advise: replyOnly, judge: repeatedMarks},
}

var reviewTexts = map[Rule]ruleText{
	RuleNonspecific: {
		code:    "PRF8010",
		catches: "a review reply that judges or asks for a change and names no code, path, line, example or reason",
		why: "Gunawardena et al. (CSCW 2022) define destructive criticism as feedback that is nonspecific " +
			"and inconsiderate, and over half of their respondents had received it in the past year; Bosu " +
			"et al. (MSR 2015) found a third of review comments were not useful. Measured 2026-10-10 over " +
			"the AIDev review comments: 2.92 percent of 39639 written by people and 0.15 percent of 42076 " +
			"written by bots. It advises: an inline comment already sits on its line, which the rule " +
			"cannot see.",
	},
	RuleWhyOpener: {
		code:    "PRF8011",
		catches: "a review reply sentence that opens \"Why did you\" or \"Why would you\"",
		why: "Danescu-Niculescu-Mizil et al. (ACL 2013) found a direct question opening with \"why\" among " +
			"the strongest cues of an impolite request: it asks the author to defend themselves. Asking " +
			"what the code needs (\"Does this need the lock?\") asks the same. Measured 2026-10-10 over " +
			"the AIDev review comments: 0.16 percent of 39639 written by people and none of 42076 written " +
			"by bots. It advises: the author may want the reason on record.",
	},
	RuleBareImperative: {
		code:    "PRF8012",
		catches: "a short command in a review reply that gives no reason anywhere (\"Fix this.\")",
		why: "Danescu-Niculescu-Mizil et al. (ACL 2013) found a bare imperative, and a request opening " +
			"with \"Please\", read as less polite than one that gives its reason or asks. A command of " +
			"five words or fewer counts, unless the reply gives a reason anywhere or the command names " +
			"code. Measured 2026-10-10 over the AIDev review comments: 4.56 percent of 39639 written by " +
			"people and 0.07 percent of 42076 written by bots. It advises: between teammates who share " +
			"the context, a short command can be read as intended.",
	},
	RuleAllCaps: {
		code:    "PRF8013",
		catches: "words in capitals for emphasis in a review reply (\"DO NOT\", \"NEVER\")",
		why: "Capitals read as shouting. An acronym is left alone: the rule reports a run of capital words " +
			"only when it holds an English word such as NOT, NEVER or ALL. Measured 2026-10-10 over the " +
			"AIDev review comments: 0.41 percent of 39639 written by people and 0.37 percent of 42076 " +
			"written by bots, which capitalize ALL and ANY in technical prose too, so the rule does not " +
			"tell the two apart and only advises.",
	},
	RuleRepeatedMarks: {
		code:    "PRF8014",
		catches: "a run of question or exclamation marks in a review reply (\"??\", \"!!\", \"?!\")",
		why: "Repeated marks read as exasperation where one mark asks the same question. Measured " +
			"2026-10-10 over the AIDev review comments: 0.17 percent of 39639 written by people and 0.05 " +
			"percent of 42076 written by bots. Code spans are masked, so an operator such as ?? passes.",
	},
}

var (
	// judgment is a verdict on code, or a request for a change to a bare
	// this, it or that.
	judgment = regexp.MustCompile(`(?i)\b(?:wrong|bad|incorrect|confusing|unclear|weird|odd|strange|ugly|messy|` +
		`hacky|sloppy|overkill|over-?complicated|too (?:complex|complicated)|(?:doesn't|does not) make sense|` +
		`not (?:right|correct|good|ideal|great|clean|needed|necessary)|unnecessary|redundant|pointless|useless|` +
		`inconsistent|fragile|problematic|(?:won't|will not|doesn't|does not) work|I (?:don't|do not) like|` +
		`not a fan|(?:fix|change|clean up|improve|rework|redo|rewrite|update) (?:this|it|that)(?: up)?)\b`)

	// specific is anything in a reply's source that names what to act on:
	// code, a path or file, a line, an identifier, a link, a number, an
	// example or a quotation.
	specific = regexp.MustCompile("`|" + `(?:^|\s)[\w.-]+/[\w./-]+|\b\w+\.(?:go|py|js|ts|tsx|jsx|rb|rs|java|kt|` +
		`swift|c|h|cc|cpp|cs|php|md|json|ya?ml|toml|sh|sql|html|css|lock|txt)\b|\b(?:lines?|L)\s?\d|` +
		`\b[a-z]+[A-Z]\w*|\b\w+_\w+|\b\w+\(\)|https?://|\d|(?i:\be\.g\.|for (?:example|instance)|such as)|` +
		`"[^"\n]{2,}"|^\s{4}\S`)
)

// nonspecific reports the first judgment in a reply that names nothing to act
// on. A judgment with a reason beside it in its sentence is left alone.
func nonspecific(in input) []Finding {
	for _, ln := range in.source {
		if specific.MatchString(ln) {
			return nil
		}
	}

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, span := range sentenceBounds(para.text) {
			text := para.text[span[0]:span[1]]

			at := judgment.FindStringIndex(text)
			if at == nil || reasonGiven.MatchString(text) {
				continue
			}

			m := text[at[0]:at[1]]

			return []Finding{{
				Message: fmt.Sprintf("Name what '%s' refers to and what to change: a line, an identifier in "+
					"backticks, an example or the reason, as in 'This reads the map without the lock: "+
					"`cache.get` races with `cache.put`.'", m),
				Match: m, Line: para.lineAt(span[0] + at[0]),
			}}
		}
	}

	return nil
}

var whyAsked = regexp.MustCompile(`^Why (?:did|would|do|are|were|didn't|don't|wouldn't|aren't) you\b`)

func whyOpener(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, s := range sentenceStarts(para.text) {
			if m := whyAsked.FindString(strings.TrimLeft(para.text[s:], "*_( ")); m != "" {
				out = append(out, Finding{
					Message: fmt.Sprintf("Ask about the code, not the author's choice: in place of '%s', as in "+
						"'Does this need the lock? The map is only read here.'", m),
					Match: m, Line: para.lineAt(s),
				})
			}
		}
	}

	return out
}

var (
	// bareCommand is a sentence of at most five words opening with an
	// imperative. "Fix is in" and "Change in" are nouns.
	bareCommand = regexp.MustCompile(`^(?:Please,? )?((?:[Ff]ix|[Rr]emove|[Cc]hange|[Dd]elete|[Rr]ename|[Mm]ove|` +
		`[Uu]se|[Aa]dd|[Rr]evert|[Dd]rop|[Uu]pdate|[Mm]ake|[Dd]on't|[Dd]o not|[Ss]top|[Rr]ewrite|[Rr]edo|` +
		`[Cc]lean up|[Ss]plit|[Ii]nline|[Ee]xtract|[Rr]eplace)\b(?:\W+\w+){0,4})\W*$`)
	commandNoun = regexp.MustCompile(`^(?:Fix|Change)\s+(?:is|in|was)\b`)

	// purpose is an infinitive giving the command's reason ("to reduce
	// noise"), which [reasonGiven] leaves out: "change this to a map" is no
	// reason.
	purpose = regexp.MustCompile(`\b(?:in order to|so that|to (?:reduce|avoid|keep|match|prevent|ensure|improve|` +
		`comply|support|allow|simplify|fix|stay|remain|make sure))\b`)
)

// bareImperative reports each short command in a reply that gives no reason
// anywhere: a reason in another sentence explains the command too.
func bareImperative(in input) []Finding {
	paras := paragraphs(in.prose, mentionsMasked)

	for _, para := range paras {
		if reasonGiven.MatchString(para.text) || purpose.MatchString(para.text) {
			return nil
		}
	}

	var out []Finding

	for _, para := range paras {
		for _, span := range sentenceBounds(para.text) {
			text := strings.TrimSpace(para.text[span[0]:span[1]])
			// A command naming code in backticks or a number names what to act
			// on.
			m := bareCommand.FindStringSubmatch(text)
			if m != nil && !commandNoun.MatchString(m[1]) && !strings.ContainsAny(text, "#0123456789") {
				out = append(out, Finding{
					Message: fmt.Sprintf("Give the reason with '%s', or ask, as in 'Could this use the shared "+
						"client? Each new one opens its own pool.'", m[1]),
					Match: m[1], Line: para.lineAt(span[0]),
				})
			}
		}
	}

	return out
}

var (
	capsWord = regexp.MustCompile(`\b[A-Z]{2,}(?:'[A-Z]+)?\b`)

	// emphatic are English words, which in capitals are emphasis; any other
	// capital word may be an acronym, and a run of acronyms ("REST API") is
	// a name.
	emphatic = wordSet("NOT", "NEVER", "ALWAYS", "MUST", "DON'T", "DO", "STOP", "WRONG", "PLEASE", "NO", "WHY",
		"ALL", "ANY", "NEED", "SHOULD", "REALLY", "VERY", "THIS", "IS", "WILL", "CAN'T", "WON'T", "ONLY", "WHAT",
		"ARE", "THE", "AND", "BUT", "YOU", "IT", "BE", "AGAIN", "EVERY", "NOTHING", "SHOULDN'T", "DOESN'T")
)

// allCaps reports each run of capital words holding an emphatic one.
func allCaps(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		spans := capsWord.FindAllStringIndex(para.text, -1)

		for i := 0; i < len(spans); {
			j := i + 1
			for j < len(spans) && strings.TrimSpace(para.text[spans[j-1][1]:spans[j][0]]) == "" {
				j++
			}

			run := spans[i:j]
			if shouted(para.text, run) {
				m := para.text[run[0][0]:run[len(run)-1][1]]
				out = append(out, Finding{
					Message: fmt.Sprintf("Write '%s' in lower case: capitals read as shouting. Use *emphasis* "+
						"if a word must stand out.", m),
					Match: m, Line: para.lineAt(run[0][0]),
				})
			}

			i = j
		}
	}

	return out
}

func shouted(text string, run [][]int) bool {
	return slices.ContainsFunc(run, func(at []int) bool { return emphatic[text[at[0]:at[1]]] })
}

var markRun = regexp.MustCompile(`[?!]{2,}`)

func repeatedMarks(in input) []Finding {
	return matchFindings(in, markRun, nil, "Write one mark in place of '%s': a run reads as exasperation.")
}
