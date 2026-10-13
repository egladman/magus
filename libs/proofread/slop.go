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
	// withReply is the written kinds and review text: the tells that apply to
	// any text a person reads as another person's own words.
	withReply = slices.Concat(proseKinds, reviewText)
	// everywhere is every kind that holds prose, doc comments included.
	everywhere = slices.Concat(all, compact, changeText, help, reviewText, agentReply)
	// notPullRequest is where a tell a change description refuses outright
	// only advises: a page may state a contrast on purpose.
	notPullRequest = slices.Concat([]Kind{KindReference, KindGuide, KindAgentBrief, KindAgentCompact}, reviewText, changeText)
	// prosePages are the kinds written as running prose. A guide's steps and a
	// skill's runbook are short imperatives by rule (step-verb, terse-sentence),
	// so a run of short sentences there is the form, not a drumbeat.
	prosePages = []Kind{KindReference, KindChangeDescription, KindIssue, KindReleaseNotes}
	// pageKinds are the kinds that hold headings.
	pageKinds = []Kind{KindReference, KindGuide, KindAgentBrief, KindIssue, KindReleaseNotes}
)

// slopChecks run after [toneChecks].
var slopChecks = []check{
	{
		rule: RuleSignpost, on: agentWords, advise: agentReply,
		judge: tellJudge("Drop '%s': state the point itself.", signposts...),
	},
	{
		rule: RuleChatbot, on: slices.Concat(withReply, compact),
		judge: ownWords(tellJudge("Drop '%s': it answers a chat the reader never saw.", chatbots...)),
	},
	{
		rule: RuleLeak, on: everywhere, advise: agentReply,
		judge: tellJudge("Remove '%s': it is left over from a tool or a template.", leaks...),
	},
	{rule: RuleBuzzword, on: agentWords, judge: tellJudge("Replace '%s' with what is actually so.", buzzwords...)},
	{
		rule: RuleBuzzwordWeak, on: agentWords, advise: agentWords,
		judge: tellJudge("Replace '%s' with what is actually so.", weakBuzzwords...),
	},
	{
		rule: RuleContrast, on: slices.Concat(proseKinds, compact, agentReply), advise: slices.Concat(notPullRequest, agentReply),
		judge: ownWords(tellJudgeExcept("State the positive claim directly: drop the negation in '%s'.",
			contrastExempt, contrasts...)),
	},
	{rule: RuleVague, on: agentWords, judge: tellJudge("Name the specific thing '%s' stands for, or cut it.", vagues...)},
	{rule: RuleCloser, on: agentWords, judge: tellJudge("Cut '%s': the paragraph above already says it.", closers...)},
	{
		rule: RuleIngTail, on: slices.Concat(proseKinds, agentReply), advise: slices.Concat(proseKinds, agentReply),
		judge: ownWords(tellJudgeExcept("Make '%s' a sentence with a subject, or cut it.", ingTailExempt, ingTails...)),
	},
	{rule: RuleStaccato, on: prosePages, advise: []Kind{KindReference}, judge: ownWords(staccato)},
	{rule: RuleDash, on: agentWords, house: true, judge: dash},
	{rule: RuleASCII, on: agentWords, house: true, judge: ascii},
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
	return tellJudgeExcept(message, nil, tells...)
}

// tellJudgeExcept is [tellJudge] less the spans exempt reports, given the
// paragraph's text and the span.
func tellJudgeExcept(message string, exempt func(text string, at []int) bool, tells ...tell) func(in input) []Finding {
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
				if exempt != nil && exempt(para.text, at) {
					continue
				}

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

// signposts leave "Here's how you could refactor it:" and "Here's what I
// changed:" alone: each captions the code or list below it, and only "why it
// matters" or "the thing" is an announcement. "What if" counts only as "What
// if I told you": any other "What if" asks a real question.
var signposts = []tell{
	opening(`^(?:Here(?:'s| is) (?:the thing|why (?:it|this|that) matters|what (?:matters|it means|this means)|` +
		`the (?:problem|catch|kicker|rub))\b|` +
		`(?:The )?(?:uncomfortable |honest |hard |plain )?truth is\b|It turns out\b|` +
		`Let me be (?:clear|honest|upfront)\b|I(?:'ll| will) say it again\b|I'm going to be honest\b|` +
		`Can we talk about\b|` +
		`Let's (?:dive|explore|break|unpack|walk|take a look|get started|jump|look at|talk about|be honest)\b|` +
		`Without further ado\b|Make no mistake\b|Let that sink in\b|Full stop\b|Period\.|Think about it\b|` +
		`And that's (?:okay|ok|fine|alright)\b|What if I told you\b|(?:Honestly|Frankly|Candidly)[,?]|Real talk\b|` +
		`The thing is\b|Look,|Plot twist\b|Spoiler\b|You already know this\b|As we'll see\b|` +
		`In this (?:section|post|article|essay),? (?:we|I)\b|I want to (?:explore|discuss|talk about|walk)\b|` +
		`Let me walk you through\b|That's it\. That's the\b)`),
	anywhere(`\bI promise\b|\bcreeps in\b|\bThe rest of this (?:essay|post|article|piece)\b`),
}

// chatbots judge a review reply by fewer tells than a page or a description:
// a reply is one person answering another in a thread both can read, so an
// agreement ("You're right, the argument is unused"), an invitation, an offer
// or an answer's opener there answers a chat the reader did see. Only the
// tells no person writes stay: in the labeled replies, "You're absolutely
// right", "Great question" and "I'm sorry I" were wrong in all 21 firings.
var chatbots = []tell{
	anywhere(`(?i)\blet me know if\b|\bfeel free to\b|\bhappy to help\b|\b(?:would you like|want) me to\b|`+
		`\bshould I (?:continue|go on|proceed)\b|\byou(?:'re| are) (?:(?:absolutely|completely|totally) )?right\b|`+
		`\b(?:great|excellent|fantastic) (?:point|catch|observation)\b|`+
		`\b(?:great|excellent|fantastic) question\b|\bI(?:'m| am) sorry,? (?:but )?I\b`, proseKinds...),
	{
		re:      regexp.MustCompile(`(?i)^(?:Certainly|Of course|Absolutely|Sure thing)[!,.]`),
		opening: true,
		kinds:   proseKinds,
	},
	anywhere(`(?i)\bI hope (?:this|that) helps\b|\bis there anything else\b|` +
		`\bas an? (?:ai|large) language model\b|` +
		`\bI (?:cannot|can't) (?:help|assist) with\b|\bas of my (?:last )?(?:training|knowledge)|` +
		`\bup to my last training\b|\bwhile specific details are (?:limited|scarce)\b|` +
		`\bbased on (?:the )?(?:available|provided) (?:information|sources)\b|` +
		`\bin the provided (?:sources|context|search results)\b|\bmaintains? a low profile\b|` +
		`\bkeeps? (?:personal )?details private\b|` +
		`\bnot (?:extensively |widely |publicly )?(?:documented|disclosed) in (?:readily )?available sources\b`),
	opening(`(?i)^Here (?:is|are) (?:a|an|the|some|your) (?:\w+ ){0,2}(?:overview|summary|draft|breakdown|rewrite)\b`),
	// A salutation is no tell on a page (a letter is a legitimate subject), so
	// the letter patterns judge only a pull request.
	anywhere(`\bDear [A-Z][\w ]{2,40}[:,]|\bI am writing to\b|\bhope this (?:message|email|note) finds you\b|`+
		`\bI understand (?:the |your )?concerns?\b`, KindChangeDescription),
}

// leaks read a pair of lenticular brackets (U+3010, U+3011) as a citation only
// around a dagger (U+2020), as in a source or a file-and-line marker: without
// one the pair is CJK punctuation around a label. A placeholder word must end
// at a space, a colon or the bracket, so link text naming a file
// (`[describe.go:668]`, `[todo.go]`) is no placeholder.
var leaks = []tell{
	anywhere(`contentReference|oaicite|oai_citation|citeturn|\bturn\d+(?:search|image|news|file|view)\d+\b|` +
		`\[cite: ?\d|\[span_\d|grok_card|grok_render|【[^】\n]*†[^】\n]*】|\[attached_file|\[web:\d|ppl-ai|` +
		`:::writing`),
	anywhere(`(?i)\b\d{4}-xx-xx\b|\bTBD\b|\blorem ipsum\b|\[(?:insert|describe|todo|tbd|fill in)(?:[\s:][^\]\n]{0,40})?\]`),
}

// buzzwords read three words only in their buzzword sense. "vibrant" counts
// before an abstract noun ("a vibrant ecosystem"): before a color or a theme
// it describes the color. "on the same page" counts after a person ("we are
// all on the same page"): after anything else it is a page. "deep dive"
// counts before into, on, of or through, or ending its phrase: before a noun
// it names a kind of document ("the deep dive doc").
var buzzwords = []tell{
	anywhere(`(?i)\b(?:delv(?:e|es|ed|ing)|tapestr(?:y|ies)|testament|pivotal|intricacies|intricate|` +
		`garner(?:s|ed|ing)?|bolstered|meticulous(?:ly)?|multifaceted|nestled|breathtaking|groundbreaking|` +
		`renowned|stunning(?:ly)?|game-chang(?:er|ing)|boasts|commendabl[ey])\b`),
	anywhere(`(?i)\bvibrant (?:community|communities|ecosystems?|culture|hub|scene|city|tapestry|history|` +
		`heritage|life|world|place|landscape|neighborhood|atmosphere|energy)\b`),
	anywhere(`(?i)\b(?:we|we're|us|everyone|everybody|you|you're|they're|team)(?:\s+[\w']+){0,2}\s+` +
		`on the same page\b`),
	anywhere(`(?i)\bdeep dives?(?:\s+(?:into|on|of|through)\b|\s*[:.,;!?)*]|\s*$)`),
	// "underscore" only as a verb: before an object, after a word that makes
	// it one. A leading underscore and "replace underscores with" are the
	// character, and so is a plural before "that" or an article ("double
	// underscores that the loader replaces").
	anywhere(`(?i)\b(?:underscor(?:ed|ing)|(?:to|will|would|may|might|can|could|which|this|that|it|further|` +
		`also|only) underscore)\s+(?:the|this|that|these|those|how|why|what|its|their|our|his|her|a|an)\b|` +
		`\bunderscores\s+(?:the|this|how|why|what|its|their|our|his|her)\b`),
	anywhere(`(?i)\b(?:lean(?:s|ed|ing)? into|circle back|moving forward|doubl(?:e|es|ed|ing) down|` +
		`take a step back|evolving landscape|rich tapestry|` +
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

// contrasts read "not only ... but also" only before an article: before an
// adjective or a phrase it adds a condition ("not only non-empty but also free
// of whitespace", "merge not only within a layer but also across layers").
var contrasts = []tell{
	anywhere(`(?i)\bnot only (?:a|an|the|about)\b[^.!?;]{1,100}\bbut (?:also|even)\b`),
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

var (
	// supposing opens a clause that states a condition, whose "is not X, it
	// is Y" is a plain conditional: "If the alias is not provided, it is
	// derived".
	supposing = regexp.MustCompile(`(?i)\b(?:if|when|unless|assuming|whether)\b`)

	// raisedPosition reports a position someone took, which a later "isn't X,
	// it's Y" in the paragraph answers rather than invents: "You proposed
	// joining ... So it isn't a join, it's an attribute".
	raisedPosition = regexp.MustCompile(`(?i)\b(?:you|they|it) (?:proposed|suggested|said|asked|argued|assumed|` +
		`wrote)\b|\bassum(?:ption|ed)\b|\bflagged\b`)

	// eventList is a "no ..., no ..., just" listing what happened, not
	// denying a claim: "no counts moved, no sections changed, just the
	// fingerprint".
	eventList = regexp.MustCompile(`^[Nn]o [a-z' -]*\b\w+ed,`)
)

// contrastExempt reports a contrast that answers a position stated before it
// in its paragraph, states a condition, sits in parentheses as a plain
// correction, or lists what happened.
func contrastExempt(text string, at []int) bool {
	return supposing.MatchString(sentenceBefore(text, at[0])) || parenthesized(text, at[0]) ||
		raisedPosition.MatchString(text[:sentenceAt(text, at[0])]) || eventList.MatchString(text[at[0]:at[1]])
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

// ingTails leaves "underscoring" to [buzzwords], which reports the word
// however the sentence uses it. "reflecting" is left out: in every labeled
// firing it named what a change mirrors ("reflecting the new location").
var ingTails = []tell{
	anywhere(`(?i),\s+(highlighting|emphasi[sz]ing|symboli[sz]ing|contributing to|` +
		`cultivating|fostering|encompassing|showcasing|resonating)\b`),
}

var (
	// presenting are the tail verbs that present content: before a bare noun
	// phrase they name what is shown ("showcasing custom validators and CORS
	// support"), not its importance ("showcasing its advantages").
	presenting = wordSet("highlighting", "emphasizing", "emphasising", "showcasing", "encompassing")

	// determinedObject are the words that open an object which is a claim
	// about a thing rather than the thing: "the importance of", "its
	// flexibility", "how to".
	determinedObject = wordSet("the", "a", "an", "its", "their", "his", "her", "our", "your", "this", "that",
		"these", "those", "how", "what", "why", "which", "whether", "both", "it", "them")

	// qualityClaim opens a bare object that claims a gain rather than naming
	// content: "showcasing improved guidance".
	qualityClaim = wordSet("improved", "enhanced", "better", "greater", "increased", "higher", "faster", "cleaner",
		"stronger", "superior")

	// gerundItem opens an item of a list of actions: "by providing better
	// links, highlighting the logs, and ordering the steps".
	gerundItem = regexp.MustCompile(`(?i)(?:^\s*|\b(?:by|for|while) )\w+ing\b`)
)

// ingTailExempt reports a tail that is no clause (a noun in a list:
// "numbers, highlighting, drawing"), one that names the content it presents,
// and one that is an item in a list of actions.
func ingTailExempt(text string, at []int) bool {
	rest := strings.TrimLeft(text[at[1]:], " ")
	next := leadingWord(rest)

	if next == "" {
		return true
	}

	verb := strings.ToLower(strings.TrimLeft(text[at[0]:at[1]], ", \t\n"))
	if presenting[verb] && !determinedObject[strings.ToLower(next)] && !qualityClaim[strings.ToLower(next)] {
		return true
	}

	before := sentenceBefore(text, at[0])
	if i := strings.LastIndex(before, ","); i >= 0 {
		before = before[i+1:]
	}

	return gerundItem.MatchString(before)
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

// abbreviation is an initial or an abbreviation and its full stop, which
// staccato rewrites like a [stepNumber]: "Lee, A., Kim, B. C.", "(Ex. fill)".
var abbreviation = regexp.MustCompile(`\b([A-Z]|Ex|ex|vs|etc|cf|Fig)\.`)

// htmlList is markup that lays a paragraph out as a list or a heading.
var htmlList = regexp.MustCompile(`(?i)<(?:li|br|ul|ol|h\d|td|tr)\b`)

// staccato reports a paragraph holding a run of short sentences. A list item
// is a list, not rhythm, and a heading or a title is a label; so is a
// paragraph laid out as a list in HTML. A sentence that is not prose breaks a
// run: one with no word (a path, an emoji, a log line), a label ending in a
// colon, or a link ("Source: [tool.proto:146].").
func staccato(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		if para.head.item || para.head.heading || (titled(in.kind) && para.head.line == 1) ||
			htmlLaidOut(in.source, para) {
			continue
		}

		var run []sentenceSpan

		// A step number ("**2.**") is no sentence of one word.
		text := abbreviation.ReplaceAllString(stepNumber.ReplaceAllString(para.text, "$1#$2"), "$1#")
		spans := sentenceSpans(text)

		for i, s := range spans {
			end := len(text)
			if i+1 < len(spans) {
				end = spans[i+1].start
			}

			if s.words > staccatoWords || !readsAsProse(text[s.start:end]) {
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

// htmlLaidOut reports whether a source line of para holds [htmlList] markup.
func htmlLaidOut(source []string, para paragraph) bool {
	for _, s := range para.starts {
		if s.line > 0 && s.line <= len(source) && htmlList.MatchString(source[s.line-1]) {
			return true
		}
	}

	return false
}

// readsAsProse reports whether sentence reads as one: it holds a word of letters,
// holds no link, and does not end in a colon.
func readsAsProse(sentence string) bool {
	fields := strings.Fields(sentence)
	if len(fields) == 0 || strings.Contains(sentence, "[") ||
		strings.HasSuffix(strings.TrimRight(fields[len(fields)-1], "*_"), ":") {
		return false
	}

	notWord := func(r rune) bool { return !unicode.IsLetter(r) && r != '\'' && r != '-' }

	return slices.ContainsFunc(fields, func(f string) bool {
		f = strings.Trim(f, "*_\"'()[]:;,.!?")

		return f != "" && strings.IndexFunc(f, notWord) < 0
	})
}

// dash reports an em dash, an en dash, or a double hyphen set between words.
// A double hyphen is reported with the words either side, as aside spells a
// spaced hyphen; a command's `--` goes in a code span. A dash's match takes
// the spaces around it, so its replacement reads as written. A date range is
// no aside: its double hyphen stands, and its em or en dash, still not ASCII,
// is replaced by a hyphen.
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

			replacement := dashReplacement(para.text, from, to)
			if dateRange(para.text, from, to) {
				replacement = strings.Replace(para.text[from:to], string(r), "-", 1)
			}

			report(from, to, replacement)
		}

		for i := 0; ; {
			j := strings.Index(para.text[i:], " -- ")
			if j < 0 {
				break
			}

			at := i + j + 1
			if at > 1 && para.text[at-2] != ' ' && at+3 < len(para.text) && para.text[at+3] != ' ' &&
				!dateRange(para.text, at-1, at+3) {
				from, to := wordBefore(para.text, at), wordAfter(para.text, at+1)
				report(from, to, para.text[from:at-1]+dashReplacement(para.text, at-1, at+3)+para.text[at+3:to])
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

		for i := 1; i < len(fields); i++ {
			w := fields[i]
			if headingStops[strings.ToLower(w)] {
				continue
			}

			if r, _ := utf8.DecodeRuneInString(w); !unicode.IsUpper(r) {
				title = false

				break
			}

			if !inNameList(fields, i) {
				capitalized++
			}
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

// inNameList reports whether fields[i] is one name in a list joined by commas or
// by a lower-case "or" or "and": the names in "Coming from TypeScript, Go, Python
// or Swift" are proper nouns, not title case. A capitalized "And" joins nothing,
// so a title-cased heading keeps counting its words.
func inNameList(fields []string, i int) bool {
	joins := func(w string) bool { return w == "or" || w == "and" }

	switch {
	case strings.HasSuffix(fields[i], ","):
		return true
	case i+1 < len(fields) && joins(fields[i+1]):
		return true
	case i > 0 && (strings.HasSuffix(fields[i-1], ",") || joins(fields[i-1])):
		return true
	}

	return false
}
