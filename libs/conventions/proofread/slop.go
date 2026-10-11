package proofread

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// RuleSignpost reports an announcement standing where the point should be:
	// "Here's the thing", "Let's dive in", "Make no mistake".
	RuleSignpost Rule = "signpost"
	// RuleChatbot reports text a chat assistant addressed to its user: an
	// offer, flattery, or a disclaimer about its own knowledge.
	RuleChatbot Rule = "chatbot"
	// RuleLeak reports residue of a tool or a template: a citation marker, or a
	// placeholder left unfilled.
	RuleLeak Rule = "leak"
	// RuleBuzzword reports a word or phrase chosen to sound significant rather
	// than to say what is so: "delve", "tapestry", "plays a vital role".
	RuleBuzzword Rule = "buzzword"
	// RuleBuzzwordWeak reports the words behind [RuleBuzzword] that also have
	// an ordinary sense: "crucial", "landscape", "key factor".
	RuleBuzzwordWeak Rule = "buzzword-weak"
	// RuleContrast reports a claim made by denying its opposite first: "not
	// just X, it is Y".
	RuleContrast Rule = "contrast"
	// RuleVague reports weight or consensus asserted with nothing named: "the
	// stakes are high", "experts argue", "the future looks bright".
	RuleVague Rule = "vague"
	// RuleCloser reports a sentence that opens by announcing it restates the
	// text above: "In conclusion,".
	RuleCloser Rule = "closer"
	// RuleIngTail reports a participle clause added to claim significance:
	// ", highlighting the importance of".
	RuleIngTail Rule = "ing-tail"
	// RuleStaccato reports three or more consecutive sentences of six words or
	// fewer in one paragraph.
	RuleStaccato Rule = "staccato"
	// RuleDash reports an em dash, an en dash or a spaced double hyphen in
	// prose.
	RuleDash Rule = "dash"
	// RuleASCII reports a curly quote, an ellipsis character or an emoji in
	// prose.
	RuleASCII Rule = "ascii"
	// RuleHeadingCase reports a heading whose every word after the first is
	// capitalized.
	RuleHeadingCase Rule = "heading-case"
)

var (
	// withReply is the written kinds and a review reply: the tells that apply
	// to any text a person reads as another person's own words.
	withReply = slices.Concat(written, []Kind{KindReviewReply})
	// everywhere is every kind that holds prose, doc comments included.
	everywhere = slices.Concat(all, []Kind{KindReviewReply})
	// notPullRequest is where a tell a change description refuses outright
	// only advises: a page may state a contrast on purpose.
	notPullRequest = []Kind{KindReference, KindGuide, KindAgentInstructions, KindReviewReply}
	// prosePages are the kinds written as running prose. A guide's steps and a
	// skill's runbook are short imperatives by rule (step-verb, terse-sentence),
	// so a run of short sentences there is the form, not a drumbeat.
	prosePages = []Kind{KindReference, KindChangeDescription}
	// pageKinds are the kinds that hold headings.
	pageKinds = []Kind{KindReference, KindGuide, KindAgentInstructions}
)

// slopChecks run after [toneChecks].
var slopChecks = []check{
	{rule: RuleSignpost, on: withReply, judge: tellJudge("Drop '%s': state the point itself.", signposts...)},
	{rule: RuleChatbot, on: withReply, judge: tellJudge("Drop '%s': it answers a chat the reader never saw.", chatbots...)},
	{rule: RuleLeak, on: everywhere, judge: tellJudge("Remove '%s': it is left over from a tool or a template.", leaks...)},
	{rule: RuleBuzzword, on: withReply, judge: tellJudge("Replace '%s' with what is actually so.", buzzwords...)},
	{
		rule: RuleBuzzwordWeak, on: withReply, advise: withReply,
		judge: tellJudge("Replace '%s' with what is actually so.", weakBuzzwords...),
	},
	{
		rule: RuleContrast, on: written, advise: notPullRequest,
		judge: tellJudge("State the positive claim directly: drop the negation in '%s'.", contrasts...),
	},
	{rule: RuleVague, on: withReply, judge: tellJudge("Name the specific thing '%s' stands for, or cut it.", vagues...)},
	{rule: RuleCloser, on: withReply, judge: tellJudge("Cut '%s': the paragraph above already says it.", closers...)},
	{
		rule: RuleIngTail, on: written, advise: written,
		judge: tellJudge("Make '%s' a sentence with a subject, or cut it.", ingTails...),
	},
	{rule: RuleStaccato, on: prosePages, advise: []Kind{KindReference}, judge: staccato},
	{rule: RuleDash, on: withReply, house: true, judge: dash},
	{rule: RuleASCII, on: withReply, house: true, judge: ascii},
	{rule: RuleHeadingCase, on: pageKinds, advise: pageKinds, judge: headingCase},
}

// tell is one pattern of a rule. An opening tell is anchored to the start of
// a sentence; kinds, when set, narrows the kinds the rule's own list judges.
type tell struct {
	re      *regexp.Regexp
	opening bool
	kinds   []Kind
}

func anywhere(pattern string, kinds ...Kind) tell {
	return tell{re: regexp.MustCompile(pattern), kinds: kinds}
}

func opening(pattern string) tell {
	return tell{re: regexp.MustCompile(pattern), opening: true}
}

// find returns where the tell matches text. An opening tell reads each
// sentence the way replyOpener does, past any emphasis or bracket.
func (t tell) find(text string) [][]int {
	if !t.opening {
		return t.re.FindAllStringIndex(text, -1)
	}

	var out [][]int

	for _, start := range sentenceStarts(text) {
		rest := text[start:]
		lead := strings.TrimLeft(rest, "*_(")

		if at := t.re.FindStringIndex(lead); at != nil {
			skipped := len(rest) - len(lead)
			out = append(out, []int{start + skipped + at[0], start + skipped + at[1]})
		}
	}

	return out
}

// tellJudge reports every span any of tells finds, once: where two overlap,
// the one that starts first stands, and the longer of two that start together.
// message takes the matched text.
func tellJudge(message string, tells ...tell) func(in input) []Finding {
	return func(in input) []Finding {
		var out []Finding

		for _, para := range paragraphs(in.prose, mentions(in.kind)) {
			var spans [][]int

			for _, t := range tells {
				if len(t.kinds) == 0 || slices.Contains(t.kinds, in.kind) {
					spans = append(spans, t.find(para.text)...)
				}
			}

			for _, at := range leftmost(spans) {
				m := para.text[at[0]:at[1]]
				out = append(out, Finding{Message: fmt.Sprintf(message, m), Match: m, Line: para.lineAt(at[0])})
			}
		}

		return out
	}
}

// leftmost returns spans in text order, dropping each that overlaps one kept.
func leftmost(spans [][]int) [][]int {
	slices.SortFunc(spans, func(a, b []int) int { return cmp.Or(a[0]-b[0], b[1]-a[1]) })

	var out [][]int

	end := 0

	for _, s := range spans {
		if len(out) > 0 && s[0] < end {
			continue
		}

		out, end = append(out, s), s[1]
	}

	return out
}

var signposts = []tell{
	opening(`^(?:Here(?:'s| is) (?:the thing|what|why|how|this|that|the (?:problem|catch|kicker|rub))\b|` +
		`(?:The )?(?:uncomfortable |honest |hard |plain )?truth is\b|It turns out\b|` +
		`Let me be (?:clear|honest|upfront)\b|I(?:'ll| will) say it again\b|I'm going to be honest\b|` +
		`Can we talk about\b|` +
		`Let's (?:dive|explore|break|unpack|walk|take a look|get started|jump|look at|talk about|be honest)\b|` +
		`Without further ado\b|Make no mistake\b|Let that sink in\b|Full stop\b|Period\.|Think about it\b|` +
		`And that's (?:okay|ok|fine|alright)\b|What if\b|(?:Honestly|Frankly|Candidly)[,?]|Real talk\b|` +
		`The thing is\b|Look,|Plot twist\b|Spoiler\b|You already know this\b|As we'll see\b|` +
		`In this (?:section|post|article|essay),? (?:we|I)\b|I want to (?:explore|discuss|talk about|walk)\b|` +
		`Let me walk you through\b|That's it\. That's the\b)`),
	anywhere(`\bI promise\b|\bcreeps in\b|\bThe rest of this (?:essay|post|article|piece)\b`),
}

var chatbots = []tell{
	anywhere(`(?i)\bI hope (?:this|that) helps\b|\blet me know if\b|\bfeel free to\b|\bhappy to help\b|` +
		`\b(?:would you like|want) me to\b|\bshould I (?:continue|go on|proceed)\b|\bis there anything else\b|` +
		`\byou(?:'re| are) (?:absolutely |completely |totally )?right\b|` +
		`\b(?:great|excellent|fantastic) (?:question|point|catch|observation)\b|` +
		`\bas an? (?:ai|large) language model\b|\bI(?:'m| am) sorry,? (?:but )?I\b|` +
		`\bI (?:cannot|can't) (?:help|assist) with\b|\bas of my (?:last )?(?:training|knowledge)|` +
		`\bup to my last training\b|\bwhile specific details are (?:limited|scarce)\b|` +
		`\bbased on (?:the )?(?:available|provided) (?:information|sources)\b|` +
		`\bin the provided (?:sources|context|search results)\b|\bmaintains? a low profile\b|` +
		`\bkeeps? (?:personal )?details private\b|` +
		`\bnot (?:extensively |widely |publicly )?(?:documented|disclosed) in (?:readily )?available sources\b`),
	opening(`(?i)^(?:Certainly|Of course|Absolutely|Sure thing)[!,.]|` +
		`^Here (?:is|are) (?:a|an|the|some|your) (?:\w+ ){0,2}(?:overview|summary|draft|breakdown|rewrite)\b`),
	// A salutation is no tell on a page (a letter is a legitimate subject), so
	// the letter patterns judge only a pull request.
	anywhere(`\bDear [A-Z][\w ]{2,40}[:,]|\bI am writing to\b|\bhope this (?:message|email|note) finds you\b|`+
		`\bI understand (?:the |your )?concerns?\b`, KindChangeDescription),
}

var leaks = []tell{
	anywhere(`contentReference|oaicite|oai_citation|citeturn|\bturn\d+(?:search|image|news|file|view)\d+\b|` +
		`\[cite: ?\d|\[span_\d|grok_card|grok_render|【[^】\n]*】|\[attached_file|\[web:\d|ppl-ai|:::writing`),
	anywhere(`(?i)\b\d{4}-xx-xx\b|\bTBD\b|\blorem ipsum\b|\[(?:insert|describe|todo|tbd|fill in)\b[^\]\n]{0,40}\]`),
}

var buzzwords = []tell{
	anywhere(`(?i)\b(?:delv(?:e|es|ed|ing)|tapestr(?:y|ies)|testament|pivotal|vibrant|intricacies|intricate|` +
		`garner(?:s|ed|ing)?|bolstered|meticulous(?:ly)?|multifaceted|nestled|breathtaking|groundbreaking|` +
		`renowned|stunning(?:ly)?|game-chang(?:er|ing)|boasts|commendabl[ey])\b`),
	// "underscore" only as a verb: before an object, after a word that makes
	// it one. A leading underscore and "replace underscores with" are the
	// character.
	anywhere(`(?i)\b(?:underscor(?:es|ed|ing)|(?:to|will|would|may|might|can|could|which|this|that|it|further|` +
		`also|only) underscore)\s+(?:the|this|that|these|those|how|why|what|its|their|our|his|her|a|an)\b`),
	anywhere(`(?i)\b(?:deep dive|lean(?:s|ed|ing)? into|circle back|moving forward|doubl(?:e|es|ed|ing) down|` +
		`take a step back|on the same page|evolving landscape|rich tapestry|` +
		`navigat(?:e|es|ed|ing) (?:the )?(?:challenges|complexit(?:y|ies)|uncertaint(?:y|ies)|landscape)|` +
		`plays? an? (?:vital|crucial|pivotal|key|significant|important|critical) role|` +
		`(?:serves?|stands?) as an? (?:testament|reminder|beacon|cornerstone)|setting the stage|indelible mark|` +
		`key turning point|deeply rooted)\b`),
}

// weakBuzzwords are words the error tier would refuse but for a plain sense
// they also carry. "key" is reported only before an abstract noun: before any
// other it is the noun ("cache key"). "role" is left to the error tier, which
// reads "plays a key role" whole. "realm" is reported only in "the realm of":
// alone it names an authentication domain.
var weakBuzzwords = []tell{
	anywhere(`(?i)\b(?:crucial(?:ly)?|enhanc(?:e|es|ed|ing|ement)|foster(?:s|ed|ing)?|landscape|` +
		`(?:align|aligns|aligned|aligning) with|valuable|emphasi[sz]ing|enduring|interplay|notably|` +
		`(?:in|into|within|beyond) the realms? of|` +
		`key (?:aspect|factor|part|feature|benefit|insight|takeaway|point|difference|advantage|component|` +
		`element|driver|consideration|challenge))\b`),
	opening(`(?i)^Additionally\b`),
}

var contrasts = []tell{
	anywhere(`(?i)\bnot only\b[^.!?;]{1,100}\bbut (?:also|even)\b`),
	anywhere(`(?i)\b(?:it|this|that)(?:'s| is) not (?:just|only|merely|simply) (?:about |a |an |the )?` +
		`[^.!?;]{1,60}[;,] (?:it(?:'s| is)|but)\b`),
	anywhere(`\bNot because [^.!?]{3,80}[.,;] (?:but )?[Bb]ecause\b`),
	anywhere(`\b(?:It|This|That|They) (?:isn't|is not|aren't|are not|wasn't|was not|doesn't|does not|didn't|did not) ` +
		`[^.!?]{2,70}[.!?] (?:It|This|That|They)(?:'s| is| are|'re| was| does| do)\b`),
	anywhere(`\b(?:isn't|is not|aren't|are not|wasn't|was not) (?:a |an |the |just |only |merely )?[\w' -]{1,40}[,;] ` +
		`(?:it(?:'s| is)|they(?:'re| are)|that(?:'s| is))\b`),
	anywhere(`\b[Nn]o [a-z' -]{2,25}, no [a-z' -]{2,25}, (?:just|only)\b|` +
		`\bstops? being\b[^.]{1,50}\band starts? being\b|` +
		`(?:^|[.!?] )Not (?:a|an|the) [\w' -]{1,30}\. Not |` +
		`\b[Nn]ot (?:always|perfectly|exactly|quite|entirely|fully)\. (?:Not|But)\b`),
	anywhere(`,\s+no\s+(?:guessing|surprises|guesswork|wasted \w+|fuss|magic|hassle|friction|ceremony|` +
		`boilerplate)\s*[.;]`),
}

var vagues = []tell{
	anywhere(`(?i)\bThe (?:reasons|implications|stakes|consequences|differences|effects) (?:are|is) ` +
		`(?:structural|significant|high|real|profound|enormous|substantial|serious)\b|` +
		`\bthis is the (?:deepest|biggest|real) (?:problem|issue)\b|` +
		`\b(?:Experts|Critics|Observers|Analysts|Industry (?:reports|experts|observers)|Studies|Some critics|` +
		`Many experts) (?:argue|believe|say|suggest|agree|claim|show|indicate)\b|` +
		`\bit is (?:widely|generally|commonly) (?:known|believed|accepted|considered|recognized|agreed)\b|` +
		`\b(?:the future (?:looks|is) (?:bright|promising)|exciting times|bright future|` +
		`a (?:major|significant|big) step (?:forward|in the right direction)|continues? to thrive|` +
		`pav(?:es|ed|ing) the way|only the beginning)\b|` +
		`\bdespite (?:its|these|the|such) [\w -]{0,40}challenges\b|` +
		`\bthe (?:data|market|culture|conversation|decision) (?:tells us|rewards|shifts|emerges|moves toward)\b|` +
		`\b(?:Nobody|No one) designed this\b|\bPeople tend to\b|` +
		`\bis the (?:currency|lifeblood|fabric|cornerstone) of\b|\bbecomes? a trap\b`),
}

var closers = []tell{
	opening(`^(?:In summary|In conclusion|To summarize|To sum up|All in all),`),
}

var ingTails = []tell{
	anywhere(`(?i),\s+(?:highlighting|underscoring|emphasi[sz]ing|reflecting|symboli[sz]ing|contributing to|` +
		`cultivating|fostering|encompassing|showcasing|resonating)\b`),
}

// Measured 2026-10-10 over the 261 hand-written docs pages and 200 merged pull
// requests: at a cap of 4 words the rule found nothing and missed a run of 6,
// 3 and 5 words; at 6 it finds that run and docs/scope.md alone.
const (
	staccatoWords = 6
	staccatoRun   = 3
)

// stepNumber is a number and the full stop closing it. staccato rewrites the
// stop to a byte of the same length, so the number does not end a sentence.
var stepNumber = regexp.MustCompile(`(\d+)\.(\s|$)`)

// staccato reports a paragraph holding a run of short sentences. A list item
// is a list, not rhythm, and a heading is a label.
func staccato(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		if para.head.item || para.head.heading {
			continue
		}

		var run []sentenceSpan

		// A step number ("**2.**") is no sentence of one word.
		for _, s := range sentenceSpans(stepNumber.ReplaceAllString(para.text, "$1#$2")) {
			if s.words > staccatoWords {
				run = run[:0]

				continue
			}

			if run = append(run, s); len(run) == staccatoRun {
				out = append(out, Finding{
					Message: "Join the short sentences or state the claim in one: a run of three reads as a drumbeat.",
					Line:    para.lineAt(run[0].start),
				})

				break
			}
		}
	}

	return out
}

// dash reports an em dash, an en dash, or a double hyphen set between words.
// A double hyphen is reported with the words either side, as aside spells a
// spaced hyphen; a command's `--` goes in a code span. A dash's match takes
// the spaces around it, so its replacement reads as written.
func dash(in input) []Finding {
	const message = "Write a colon, a semicolon, a comma or parentheses instead of '%s'."

	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		report := func(from, to int, replacement string) {
			m := para.text[from:to]
			out = append(out, Finding{
				Message: fmt.Sprintf(message, strings.TrimSpace(m)), Match: m, Line: para.lineAt(from),
				Replacements: []string{replacement},
			})
		}

		for i, r := range para.text {
			if r != '—' && r != '–' {
				continue
			}

			from, to := i, i+utf8.RuneLen(r)
			if from > 0 && para.text[from-1] == ' ' && to < len(para.text) && para.text[to] == ' ' {
				from, to = from-1, to+1
			}

			report(from, to, dashFor(para.text, from, to))
		}

		for i := 0; ; {
			j := strings.Index(para.text[i:], " -- ")
			if j < 0 {
				break
			}

			at := i + j + 1
			if at > 1 && para.text[at-2] != ' ' && at+3 < len(para.text) && para.text[at+3] != ' ' {
				from, to := wordBefore(para.text, at), wordAfter(para.text, at+1)
				report(from, to, para.text[from:at-1]+dashFor(para.text, at-1, at+3)+para.text[at+3:to])
			}

			i = at + 2
		}
	}

	slices.SortStableFunc(out, func(a, b Finding) int { return a.Line - b.Line })

	return out
}

// asciiMarks are the characters the house style writes in plain ASCII: curly
// quotes, the ellipsis, and the emoji blocks. Arrows and box drawing have no
// plain spelling and stay legal.
var asciiMarks = regexp.MustCompile(`[‘’“”…\x{1F300}-\x{1FAFF}\x{2705}\x{274C}\x{2728}\x{2B50}]`)

func ascii(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		for _, at := range asciiMarks.FindAllStringIndex(para.text, -1) {
			m := para.text[at[0]:at[1]]

			f := Finding{
				Message: fmt.Sprintf("Write '%s' in plain ASCII: ' \" ... or words.", m), Match: m, Line: para.lineAt(at[0]),
			}
			if r, ok := asciiFor[m]; ok {
				f.Replacements = []string{r}
			}

			out = append(out, f)
		}
	}

	return out
}

// headingStops are the words a title-cased heading still writes in lower case.
var headingStops = wordSet("a", "an", "the", "of", "and", "or", "in", "on", "to", "for", "with", "at", "by", "from", "as")

// headingCase reports a heading of four words or more whose every word after
// the first, stop words aside, is capitalized and of which three or more are
// left. A proper noun among lower-case words passes: the exact policy, with its
// proper nouns, belongs to the caller.
func headingCase(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		if !ln.heading {
			continue
		}

		fields := strings.Fields(blankBackticks(ln.body()))
		if len(fields) < 4 {
			continue
		}

		capitalized, title := 0, true

		for _, w := range fields[1:] {
			if headingStops[strings.ToLower(w)] {
				continue
			}

			if r, _ := utf8.DecodeRuneInString(w); !unicode.IsUpper(r) {
				title = false

				break
			}

			capitalized++
		}

		if title && capitalized >= 3 {
			m := strings.TrimSpace(ln.body())
			out = append(out, Finding{
				Message: "Write the heading in sentence case: only the first word and proper nouns are capitalized.",
				Match:   m, Line: ln.line, Replacements: []string{sentenceCase(m)},
			})
		}
	}

	return out
}
