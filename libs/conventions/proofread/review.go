package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

const (
	// RuleNonspecific reports a top-level review comment that judges code or
	// asks for a change and names nothing to act on: no code, path, line,
	// identifier, example or reason. A failure the reader can find ("tests are
	// failing") is a reason. A reply [WithAnchored] marks sits on its line, and
	// one holding a block quote answers the quoted comment; neither is judged.
	// Nor are words in single quotes.
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
		code:      "PRF8010",
		dimension: DimensionEvidence,
		catches: "a top-level review comment that judges or asks for a change and names no code, path, line, " +
			"example or reason",
		why: "Gunawardena et al. (CSCW 2022) define destructive criticism as feedback that is nonspecific " +
			"and inconsiderate, and over half of their respondents had received it in the past year; Bosu " +
			"et al. (MSR 2015) found a third of review comments were not useful. Measured 2026-10-10 over " +
			"the AIDev review comments: 2.92 percent of 39639 written by people and 0.15 percent of 42076 " +
			"written by bots. It judges top-level comments only: an inline comment, or a reply in its " +
			"thread, sits on the line it judges, which is its referent, and the caller says so with " +
			"-anchored. The anchor gives the place, not the reason, so an anchored bare verdict still " +
			"lacks the suggestion Gunawardena et al. count, and the rule leaves it alone. Measured " +
			"2026-10-11 over the adjudicated labeling round 2: 7.0% precision (3 of 43) before, 27.3% (3 " +
			"of 11) after. Most firings named what they judged in a noun phrase (\"the indentation\", " +
			"\"this logic\") or by the noun the judgment modifies (\"redundant docstrings\"), so only a " +
			"verdict on a bare this, it or that, or on nothing, is reported. The rest sat inline on the " +
			"line they judge. It advises, and stays off until a fresh labeling round measures it. Labeling " +
			"round 5 measured 32.5% (13 of 40, lower bound 0.20); seven of its wrong firings named a failure " +
			"the reader can find (\"tests are failing\", \"CI failed\", a command that times out), which is " +
			"the reason for the change asked, so such a reply is left alone. A retry asked with nothing " +
			"named (\"something went wrong, please try again\") is still reported. Many of its other wrong " +
			"firings judged words the writer quoted. A block quote is the comment the reply answers, its " +
			"referent as an anchored reply's line is, so a reply holding one is left alone, and so is a " +
			"judgment in single quotes. A reply CodeRabbit marks as its own auto-generated reply is not the " +
			"author's.",
	},
	RuleWhyOpener: {
		code:      "PRF8011",
		dimension: DimensionStance,
		catches:   "a review reply sentence that opens \"Why did you\" or \"Why would you\"",
		why: "Danescu-Niculescu-Mizil et al. (ACL 2013) found a direct question opening with \"why\" among " +
			"the strongest cues of an impolite request: it asks the author to defend themselves. Asking " +
			"what the code needs (\"Does this need the lock?\") asks the same. Measured 2026-10-10 over " +
			"the AIDev review comments: 0.16 percent of 39639 written by people and none of 42076 written " +
			"by bots. It advises: the author may want the reason on record.",
	},
	RuleBareImperative: {
		code:      "PRF8012",
		dimension: DimensionStance,
		catches:   "a short command in a review reply that gives no reason anywhere (\"Fix this.\")",
		why: "Danescu-Niculescu-Mizil et al. (ACL 2013) found a bare imperative, and a request opening " +
			"with \"Please\", read as less polite than one that gives its reason or asks. A command of " +
			"five words or fewer counts, unless the reply gives a reason anywhere or the command names " +
			"code or its target (\"change this to appsettings\"). Measured 2026-10-10 over the AIDev review " +
			"comments: 4.56 percent of 39639 written by people and 0.07 percent of 42076 written by bots. " +
			"It advises: between people who share the context, a short command can be read as " +
			"intended. Measured 2026-10-11 over the adjudicated labeling round: 37.2% precision (16 of " +
			"43) before, 100% (16 of 16) after. Most firings followed a sentence stating the problem " +
			"(\"Duplicate documentation. Remove it.\"), so only a reply made of bare commands is judged. " +
			"It stays off until 30 labeled firings show it.",
	},
	RuleAllCaps: {
		code:      "PRF8013",
		dimension: DimensionStance,
		catches:   "words in capitals for emphasis in a review reply (\"DO NOT\", \"NEVER\")",
		why: "Capitals read as shouting. An acronym is left alone: the rule reports a run of capital words " +
			"only when it holds an English word such as NOT, NEVER or ALL. Measured 2026-10-10 over the " +
			"AIDev review comments: 0.41 percent of 39639 written by people and 0.37 percent of 42076 " +
			"written by bots, which capitalize ALL and ANY in technical prose too, so the rule does not " +
			"tell the two apart and only advises. Measured 2026-10-11 over the adjudicated labeling round: " +
			"65.9% precision (27 of 41) before, 90.0% (27 of 30, lower bound 0.74) after, so it ships " +
			"advise. AND or OR joining a condition is an operator, and capitals in single quotes, " +
			"\"ALL CAPS\" and one capital word opening a name (\"IT Admin\") are left alone.",
	},
	RuleRepeatedMarks: {
		code:      "PRF8014",
		dimension: DimensionStance,
		catches:   "a run of question or exclamation marks in a review reply (\"??\", \"!!\", \"?!\")",
		why: "Repeated marks read as exasperation where one mark asks the same question. Measured " +
			"2026-10-10 over the AIDev review comments: 0.17 percent of 39639 written by people and 0.05 " +
			"percent of 42076 written by bots. Code spans are masked, so an operator such as ?? passes. " +
			"Measured 2026-10-11 over the adjudicated labeling round: 46.5% precision (20 of 43) before, " +
			"100% (20 of 20) after: every false positive was an operator or placeholder outside a code " +
			"span, so a run set between spaces or brackets, or touching a word it opens (\"!!hit\", " +
			"\"!!NAME!!\"), is left alone. It stays off until 30 labeled firings show it.",
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

// symptom names a failure a reader can go and find: a check that fails, a
// build that breaks, a command that times out ("tests are failing", "CI
// failed", "'yarn build' is failing"). It is the reason for the change it
// asks, so a reply naming one is specific. "It doesn't work" names nothing
// to look at.
var symptom = regexp.MustCompile(`(?i)\b(?:tests?|specs?|ci|builds?|checks?|jobs?|pipelines?|workflows?|` +
	`lint(?:er|ing)?|type ?checks?|compil(?:er|ation)|deploys?|deployments?)\b(?:\W+\w+){0,5}?\W+` +
	`(?:fail(?:s|ed|ing)?|failling|tim(?:e|es|ed|ing) out|broken|break(?:s|ing)?|crash(?:es|ed|ing)?|` +
	`hang(?:s|ing)?)\b|` +
	`\b(?:failed|failing|broken)\W+(?:\w+\W+)?(?:tests?|specs?|ci|builds?|checks?|jobs?|pipelines?|workflows?)\b|` +
	`'[^'\n]{2,}'\s+(?:is |was |keeps )?(?:fail(?:s|ed|ing)?|failling|tim(?:e|es|ed|ing) out|broken|` +
	`crash(?:es|ed|ing)?)\b|` +
	`\b(?:doesn't|does not|won't|can't|cannot|fails? to) compile\b|\b(?:merge )?conflicts\b`)

var (
	// namedThing is a determiner opening a noun phrase: "the naming", "this
	// logic", "an OR condition". A sentence holding one names what it judges.
	namedThing = regexp.MustCompile(`(?i)\b(?:the|a|an|this|that|these|those|its|their|our|your|my|no|other|` +
		`any|each|every)\s+([\w'-]+)`)

	// notANoun are the words after a determiner that leave it bare: "this
	// is", "that should", "fix this up", "a bit".
	notANoun = wordSet("is", "was", "are", "were", "be", "been", "seems", "seemed", "looks", "looked", "feels",
		"sounds", "reads", "does", "doesn't", "did", "didn't", "isn't", "wasn't", "aren't", "should", "shouldn't",
		"would", "could", "will", "won't", "can", "can't", "might", "may", "must", "has", "had", "have", "up", "out",
		"again", "too", "now", "please", "here", "there", "and", "or", "but", "i", "we", "you", "they", "it", "this",
		"that", "bit", "little", "one")

	// predicateTail are the words that may follow a judgment said of its
	// subject ("it is redundant, honestly", "wrong for now"). Any other word
	// after it is the noun it modifies ("redundant docstrings"), which the
	// reply names.
	predicateTail = wordSet("and", "or", "but", "with", "to", "for", "in", "on", "at", "of", "as", "than", "here",
		"there", "now", "too", "anyway", "honestly", "imo", "tbh", "though", "then", "because", "since", "if", "so",
		"when", "which", "again", "already", "yet", "overall", "please", "remove", "drop", "delete")
)

// namesReferent reports whether text, one sentence, names the thing its
// judgment at at is about: a noun phrase anywhere in it, the noun the
// judgment modifies, or a clause after it ("strange that we keep two IDs").
// Only a verdict on a bare this, it or that, or on nothing, is reported.
func namesReferent(text string, at []int) bool {
	rest := strings.TrimLeft(text[at[1]:], " ")
	if next := leadingWord(rest); next != "" && !predicateTail[strings.ToLower(next)] {
		return true
	}

	unjudged := text[:at[0]] + "," + text[at[1]:]

	for _, m := range namedThing.FindAllStringSubmatch(unjudged, -1) {
		if !notANoun[strings.ToLower(m[1])] && !judgment.MatchString(m[1]) {
			return true
		}
	}

	return false
}

// leadingWord is the word rest opens with, or "" when it opens with
// punctuation or is empty.
func leadingWord(rest string) string {
	end := strings.IndexFunc(rest, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' && r != '-' && r != '_'
	})
	if end < 0 {
		end = len(rest)
	}

	return rest[:end]
}

// judged is the first judgment in text, one sentence, of the author's work:
// not a mention in single quotes.
func judged(text string) []int {
	for _, at := range judgment.FindAllStringIndex(text, -1) {
		if !singleQuoted(text, at[0]) {
			return at
		}
	}

	return nil
}

// nonspecific reports the first judgment in a top-level reply that names
// nothing to act on. A judgment with a reason beside it in its sentence, or
// one that names what it judges, is left alone, and so are the words the
// writer quotes.
func nonspecific(in input) []Finding {
	// A block quote is the comment the reply answers: its referent, as the
	// line is an anchored reply's, and words the writer did not write.
	if in.opts.anchored || slices.ContainsFunc(in.source, inQuote) {
		return nil
	}

	for _, ln := range in.source {
		if specific.MatchString(ln) {
			return nil
		}
	}

	if symptom.MatchString(strings.Join(in.source, "\n")) {
		return nil
	}

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, span := range sentenceBounds(para.text) {
			text := para.text[span[0]:span[1]]

			at := judged(text)
			if at == nil || reasonGiven.MatchString(text) || namesReferent(text, at) {
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

// commandTarget names what a command changes something to: "change this to
// appsettings", "make it a frozen set".
var commandTarget = regexp.MustCompile(`\b(?:to|into|with) \w+|^\W*[Mm]ake it \w+`)

// bareImperative reports each short command in a reply made only of bare
// commands: a reason in another sentence explains the command too, and so
// does a sentence stating the problem ("Duplicate documentation. Remove it.").
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
			if text == "" {
				continue
			}
			// A command naming code in backticks, a number or its target names
			// what to act on.
			m := bareCommand.FindStringSubmatch(text)
			if m == nil || commandNoun.MatchString(m[1]) || strings.ContainsAny(text, "#0123456789") ||
				commandTarget.MatchString(m[1]) {
				return nil
			}

			out = append(out, Finding{
				Message: fmt.Sprintf("Give the reason with '%s', or ask, as in 'Could this use the shared "+
					"client? Each new one opens its own pool.'", m[1]),
				Match: m[1], Line: para.lineAt(span[0]),
			})
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

var (
	// logical are the capital words that also name a boolean operator.
	logical = wordSet("AND", "OR", "NOT")

	// condition is a sentence that states a condition, where a capital AND
	// or OR is the operator: "only run when the label is present AND".
	condition = regexp.MustCompile(`\b(?:when|whenever|if|logic|OR|conjunction|condition|true|false|enabled|` +
		`disabled)\b`)

	// titleWord opens a capitalized word that is not all capitals.
	titleWord = regexp.MustCompile(`^\s+[A-Z][a-z]`)
)

// shouted reports whether run, spans of capital words in text, holds emphasis.
// It does not when the words are quoted ('NEVER CANCEL'), when they mention
// capitals ("ALL CAPS"), when AND or OR joins a condition, or when one word
// opens a name ("IT Admin").
func shouted(text string, run [][]int) bool {
	if singleQuoted(text, run[0][0]) {
		return false
	}

	operators := true

	for _, at := range run {
		w := text[at[0]:at[1]]
		if w == "CAPS" {
			return false
		}

		operators = operators && logical[w] && w != "NOT"
	}

	if operators && condition.MatchString(sentenceHolding(text, run[0][0])) {
		return false
	}

	if len(run) == 1 && titleWord.MatchString(text[run[0][1]:]) {
		return false
	}

	return slices.ContainsFunc(run, func(at []int) bool { return emphatic[text[at[0]:at[1]]] })
}

var markRun = regexp.MustCompile(`[?!]{2,}`)

// repeatedMarks reports a run of marks closing a word or standing alone. A run
// set between spaces or brackets, or opening a word, is an operator
// ("a ?? b", "(??)", "!!hit") or a placeholder ("!!REPLACE!!").
func repeatedMarks(in input) []Finding {
	return matchFindings(in, markRun, func(text string, at []int) bool {
		before, after := byte(' '), byte(' ')
		if at[0] > 0 {
			before = text[at[0]-1]
		}

		if at[1] < len(text) {
			after = text[at[1]]
		}

		opensWord := isWordByte(after) || after == '('
		standsAlone := at[0] == 0 && at[1] == len(text)

		word := at[0]
		for word > 0 && isWordByte(text[word-1]) {
			word--
		}

		closesPlaceholder := word < at[0] && strings.HasSuffix(text[:word], text[at[0]:at[1]])

		return opensWord || closesPlaceholder || (!standsAlone && strings.IndexByte(" ([\\", before) >= 0 &&
			(strings.IndexByte(" )]'\"", after) >= 0 || at[1] == len(text)))
	}, "Write one mark in place of '%s': a run reads as exasperation.")
}
