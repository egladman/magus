package proofread

import (
	"maps"
	"slices"

	"github.com/egladman/magus/libs/diagnostics"
)

// RuleDoc is one rule as its reference page shows it, in the shape a magus
// guard rule is catalogued in.
type RuleDoc struct {
	Name Rule             `json:"name"`
	Code diagnostics.Code `json:"code"`
	// Kinds are the kinds of text the rule judges.
	Kinds []Kind `json:"kinds"`
	// Decisions is the rule's default on each of Kinds: [DecisionOff] on every
	// kind for a house rule.
	Decisions map[Kind]Decision `json:"decisions"`
	// House marks a rule that encodes one repository's conventions rather than
	// writing a teammate reads, so it runs only where a decisions table names it.
	House bool `json:"house"`
	// Dimension is what the rule protects for the reader.
	Dimension Dimension `json:"dimension"`
	// Catches says in one line what the rule fires on.
	Catches string `json:"catches"`
	// Why is the reason for the rule and for its defaults, as measured.
	Why string `json:"why"`
}

// URL is the page that documents the rule, the one a [Finding] links to.
func (d RuleDoc) URL() string { return prf.URL(d.Code) }

// Catalog returns every rule in [Rules] order.
func Catalog() []RuleDoc {
	out := make([]RuleDoc, len(checks))

	for i, c := range checks {
		decisions := make(map[Kind]Decision, len(c.on))
		for _, k := range c.on {
			decisions[k] = c.defaultDecision(k)
		}

		t := ruleTexts[c.rule]
		out[i] = RuleDoc{
			Name: c.rule, Code: t.code, Kinds: slices.Clone(c.on), Decisions: decisions, House: c.house,
			Catches: t.catches, Why: t.why, Dimension: t.dimension,
		}
	}

	return out
}

const ruleBase = "https://eli.gladman.cc/magus/reference/proofread/"

// prf is the domain of the PRF codes: a code's page is its rule's page.
var prf = diagnostics.New(func(c diagnostics.Code) string {
	for r, t := range ruleTexts {
		if t.code == c {
			return ruleBase + string(r) + "/"
		}
	}

	return ""
})

type ruleText struct {
	code         diagnostics.Code
	dimension    Dimension
	catches, why string
}

// Dimension is what a rule protects for the reader, the axis a finding is
// counted on: shaped after the MQM error typology, with Hyland's metadiscourse
// inside stance and evidence. A rule's PRF family says where it came from; its
// dimension says what it costs the reader.
type Dimension string

const (
	// DimensionEvidence is whether a claim carries what supports it.
	DimensionEvidence Dimension = "evidence"
	// DimensionStance is how the writer stands toward the reader and the work:
	// tone, hedging, credit and blame.
	DimensionStance Dimension = "stance"
	// DimensionStructure is the order a reader meets things in: a lead, a
	// heading, a step, a subject line.
	DimensionStructure Dimension = "structure"
	// DimensionEconomy is words that cost reading time and carry nothing.
	DimensionEconomy Dimension = "economy"
	// DimensionConventions is the house's spelling, typography and form.
	DimensionConventions Dimension = "conventions"
)

// ruleTexts numbers the rules by family: PRF1xxx the shape of a text, PRF2xxx
// tone, PRF3xxx claims and hedges, PRF4xxx generated-writing tells, PRF5xxx
// house style, PRF6xxx doc comments, PRF7xxx agent instructions, PRF8xxx
// review replies and PRF9xxx messages a program prints. A code is never
// reused: a retired rule keeps its number out of circulation. Each family
// file keeps its own rules' texts beside their checks.
var ruleTexts = mergeTexts(coreTexts, commitTexts, helpTexts, issueTexts, densityTexts, reviewTexts, suppressTexts,
	agentReplyTexts, toolTexts)

func mergeTexts(tables ...map[Rule]ruleText) map[Rule]ruleText {
	out := map[Rule]ruleText{}
	for _, t := range tables {
		maps.Copy(out, t)
	}

	return out
}

var coreTexts = map[Rule]ruleText{
	RuleLeadContext: {
		code:    "PRF1001",
		catches: "a change description that does not open with a paragraph naming what a reader can now do",
		why: "A reviewer's first question is what the change is for; a list, a heading or a reply opener " +
			"in that place answers a different one. A lead under 12 words carries no reason. A lead whose " +
			"first sentence states a defect and names no outcome only advises: over the 190 leads of the " +
			"last 200 merged pull requests, 60 opened on the defect, and the outcome may be phrased in " +
			"words no list holds.",
	},
	RuleReplyVoice: {
		code:    "PRF1002",
		catches: "text that answers a prompt the reader never saw: a reply opener, a bold-label item, a stock label",
		why: "\"Great question\", \"as discussed\", a `**Cache:**` bullet or a `Summary` heading is the shape " +
			"of an answer to a prompt the reader was not shown, so the reader has to reconstruct it. A " +
			"review reply is one person answering another in a thread they share, so its openers and " +
			"references to the thread are left alone.",
	},
	RuleSecondPerson: {
		code:    "PRF1003",
		catches: "we, us, our or ours in a guide, which speaks to the reader as you",
		why: "A guide is followed step by step by the person reading it; \"we\" blurs who acts. A \"we\" " +
			"the tense rule already reports is left to it, so one word gives one finding.",
	},
	RuleStepVerb: {
		code:    "PRF1004",
		catches: "a numbered step of a guide that does not open with its imperative verb",
		why: "A reader following a procedure scans for the action. A numbered list counts as a procedure " +
			"only when one of its items opens with an imperative, so a recap, a precedence order or a list " +
			"of reasons is left alone.",
	},
	RuleBlame: {
		code:    "PRF2001",
		catches: "a person or past work as the subject of a fault, and contempt for code or a decision",
		why: "Text loses its tone on the way to a reader, who fills the gap with intent the writer never " +
			"had. \"Whoever wrote this forgot to\" reads as an accusation; \"the rename left the old key\" " +
			"states the same fact. It measured no false positive over the last 200 merged pull requests, " +
			"so it denies. The first person is left alone, since owning a fault reads as candor.",
	},
	RuleVerdict: {
		code:    "PRF2002",
		catches: "a judgment word standing in for the behavior it judges (\"was broken\", \"a mess\")",
		why: "A verdict tells the reader what to think in place of what happened. It advises because " +
			"\"broken\" and \"wrong\" also name a broken test or the wrong checkout; it moves to deny only " +
			"after every firing on real text was right.",
	},
	RuleAbsolute: {
		code:    "PRF2003",
		catches: "never, nobody or nothing as a claim about the past (\"has never fired\", \"nobody checked\")",
		why: "An absolute over every run since a change reads as a verdict on whoever made it; saying when " +
			"and how often states the same fact. It advises because this repository's docs use \"never\" " +
			"788 times and every sampled use states a contract (\"never returns nil\").",
	},
	RuleIntent: {
		code:    "PRF2004",
		catches: "a motive given to a tool or a person (\"guessed\", \"pretends\")",
		why: "A tool has a mechanism, and a person given a motive in writing reads it as an accusation. " +
			"It advises: \"lies\" also says where a file lies.",
	},
	RuleCredit: {
		code:    "PRF2005",
		catches: "a change description that removes or replaces something and says nothing of what it was for",
		why: "One clause on what the earlier design did well keeps the change from reading as a verdict on " +
			"its author. Over the last 200 merged pull requests, 16 removed something by a clause of the " +
			"title or a sentence's opening, and none said what it had been for. It advises until it has " +
			"fired on real text and every firing was right.",
	},
	RuleCondescension: {
		code:    "PRF2006",
		catches: "a word that tells the reader how hard a step should feel or what they should already know",
		why: "\"Simply run\" and \"of course\" tell a reader who is stuck that they should not be. Measured " +
			"over the guides before the rule, none of 16 lowercase \"just\" minimized a step, so \"just\" " +
			"counts only before a verb the reader carries out, and an \"easy\" that warns (\"easy to get " +
			"wrong\") is left alone.",
	},
	RuleClaim: {
		code:    "PRF3001",
		catches: "a measurement, a comparison or a completion with no evidence in its sentence or bullet",
		why: "A claim a reader cannot check reads as boasting or as a guess. Evidence is a code span naming " +
			"a test or command, a link, an issue or commit, or an output ref. It advises: over the last 200 " +
			"merged pull requests it fired in 21, and about half of those state a setting (\"a 300s " +
			"bound\") rather than measure. A sentence that states a limit (\"Not measured on Linux\") is " +
			"exempt.",
	},
	RuleHedge: {
		code:    "PRF3002",
		catches: "a softener qualifying a claim (\"might fix\", \"could potentially\")",
		why: "A hedge lets a claim stand with no evidence. A writer who is unsure scopes the claim instead: " +
			"a sentence under a \"Not verified\" heading, or one opening with \"Not measured\" or " +
			"\"Untested\", states a limit and is exempt.",
	},
	RuleFiller: {
		code:    "PRF4001",
		catches: "throat-clearing (\"Note that\") and filler adverbs (\"simply\", \"basically\")",
		why: "The words carry nothing the sentence needs. Written text takes a wider list (\"actually\", " +
			"\"robust\") than doc comments, which keep the narrow one until a sweep clears the wider; " +
			"the senses that carry meaning (\"just\" as merely, \"very\" as the same one) are exempt.",
	},
	RuleWordy: {
		code:    "PRF4002",
		catches: "a phrase with a shorter equivalent (\"in order to\")",
		why: "Each phrase has a shorter spelling that says the same. Over the 261 hand-written docs pages " +
			"it found four sites, which were fixed.",
	},
	RuleSignpost: {
		code:    "PRF4003",
		catches: "an announcement standing where the point should be (\"Here's the thing\", \"Let's dive in\")",
		why: "A signpost delays the point it promises. It had zero hits in 261 docs pages, 665 changelog " +
			"fragments and 200 merged pull requests, so it denies at no cost.",
	},
	RuleChatbot: {
		code:    "PRF4004",
		catches: "text a chat assistant addressed to its user: an offer, flattery, a knowledge disclaimer",
		why: "\"I hope this helps\" and \"great question\" answer a chat the reader never saw. Zero hits " +
			"over the docs pages, changelog fragments and merged pull requests; the letter patterns " +
			"(\"Dear\", \"I am writing to\") judge only a change description, which is never a letter.",
	},
	RuleLeak: {
		code:    "PRF4005",
		catches: "residue of a tool or a template: a citation marker or an unfilled placeholder",
		why: "`oaicite`, `[cite: 1]` and `[insert ...]` are unambiguous: no reader is served by them. " +
			"They show up where text was pasted from a chat, so the rule judges doc comments too.",
	},
	RuleBuzzword: {
		code:    "PRF4006",
		catches: "a word chosen to sound significant rather than to say what is so (\"delve\", \"tapestry\")",
		why: "The list holds words with no plain sense in technical text. Zero hits over the docs pages, " +
			"changelog fragments and merged pull requests.",
	},
	RuleBuzzwordWeak: {
		code:    "PRF4007",
		catches: "a buzzword that also has an ordinary sense (\"crucial\", \"landscape\")",
		why: "These words are tells in a cluster and plain words alone, so the rule advises: one tell " +
			"proves nothing.",
	},
	RuleVague: {
		code:    "PRF4008",
		catches: "weight or consensus asserted with nothing named (\"experts argue\", \"the stakes are high\")",
		why: "The reader cannot check a source that is not named. Zero hits over the docs pages, " +
			"changelog fragments and merged pull requests.",
	},
	RuleCloser: {
		code:    "PRF4009",
		catches: "a sentence that opens by announcing it restates the text above (\"In conclusion,\")",
		why: "A summary of a page the reader just read costs a paragraph and adds nothing. Zero hits over " +
			"the docs pages, changelog fragments and merged pull requests.",
	},
	RuleContrast: {
		code:    "PRF4010",
		catches: "a claim made by denying its opposite first (\"not just X, it is Y\")",
		why: "Negative parallelism argues with a position nobody took. It denies in a change description, " +
			"where 200 merged pull requests used it 0 times, and advises elsewhere, where the docs use it " +
			"deliberately 15 times.",
	},
	RuleIngTail: {
		code:    "PRF4011",
		catches: "a participle clause added to claim significance (\", highlighting the importance of\")",
		why: "The tail asserts significance with no subject to own it. It advises: a participle clause is " +
			"also ordinary grammar.",
	},
	RuleStaccato: {
		code:    "PRF4012",
		catches: "three or more consecutive sentences of six words or fewer in one paragraph",
		why: "A run of fragments reads as a drumbeat. At a cap of 4 words the rule found nothing and " +
			"missed a real run; at 6 it found that run and docs/scope.md alone. It denies in a change " +
			"description and advises on a page; a guide's steps and agent instructions are short by rule, " +
			"so it does not judge them.",
	},
	RuleHeadingCase: {
		code:    "PRF4013",
		catches: "a heading whose every word after the first is capitalized",
		why: "Title Case headings are a generated-writing tell. It advises: a heading of proper nouns " +
			"keeps lower-case words and passes, and the full sentence-case policy with its exceptions " +
			"stays a repository's own.",
	},
	RuleTerms: {
		code:    "PRF5001",
		catches: "a spelling the glossary replaces (\"sub-agent\")",
		why: "House style: the glossary is one repository's, so the rule is off unless a decisions table " +
			"turns it on.",
	},
	RuleDash: {
		code:    "PRF5002",
		catches: "an em dash, an en dash or a spaced double hyphen in prose",
		why: "House style: plain-ASCII typography is one repository's policy, ported from its Buzz lint " +
			"so proofread alone reproduces it. Off unless a decisions table turns it on.",
	},
	RuleASCII: {
		code:    "PRF5003",
		catches: "a curly quote, an ellipsis character or an emoji in prose",
		why: "House style: curly quotes prove nothing about who wrote a text; the rule encodes one " +
			"repository's ASCII-only policy. Arrows and box drawing stay legal, having no plain spelling.",
	},
	RuleTense: {
		code:    "PRF5004",
		catches: "a claim in the future tense, or a first-person account of a change",
		why: "House style: this repository describes what the code does, in the present tense, with no " +
			"author in a description. A team elsewhere writes \"we\" and \"I\" in a pull request, so the " +
			"rule is off unless a decisions table turns it on.",
	},
	RuleAttribution: {
		code:    "PRF5005",
		catches: "credit to a tool or an agent, or an account of how the work was produced",
		why: "House style: whether a description names the tools behind it is a team's call. This " +
			"repository's product is about agents, so its word lists exempt that subject matter on pages; " +
			"another repository would draw the line elsewhere.",
	},
	RuleCommentBlock: {
		code:    "PRF6001",
		catches: "a doc comment over 250 words",
		why: "House style: measured 2026-10-06 over 46664 comment blocks, a block's p50 is 25 words, p90 " +
			"72 and p99 168; the cap sits past p99 and catches a design document living in a comment.",
	},
	RuleCommentSentence: {
		code:    "PRF6002",
		catches: "a doc comment sentence over 60 words",
		why:     "House style: over the same 46664 blocks a sentence's p50 is 17 words, p90 34 and p99 50.",
	},
	RuleNameSuffix: {
		code:    "PRF6003",
		catches: "a function or method name whose last word is Of or For",
		why: "House style: this repository names a function for what it returns or does, so `valueOf` " +
			"and `configFor` are renamed.",
	},
	RuleAside: {
		code:    "PRF6004",
		catches: "a spaced hyphen spelling an em dash in a doc comment",
		why: "House style: an aside set off by \" - \" reads as a dash the ASCII policy forbids. A hyphen " +
			"between digits is arithmetic or a range and passes; one between identifiers stays reported, " +
			"which held one false positive against 4513 findings.",
	},
	RuleHistory: {
		code:    "PRF6005",
		catches: "a doc comment phrase narrating the change rather than the code (\"used to\")",
		why: "House style: a comment describes the code as it stands and leaves its history to version " +
			"control. A doc with a TODO, FIXME, compat or Deprecated marker is exempt.",
	},
	RuleDocStub: {
		code:    "PRF6006",
		catches: "a one-line doc comment that only repeats the symbol's name",
		why: "House style: a stub satisfies a linter and tells the caller nothing. A doc with a marker is " +
			"exempt.",
	},
	RuleTerseSentence: {
		code:    "PRF7001",
		catches: "a sentence of agent instructions over 25 words",
		why: "House style: every word an agent loads costs context in every session. Measured 2026-10-07 " +
			"over the short form of 18 skills, 1264 sentences ran p50 14 words, p90 30; the cap sits below " +
			"p90.",
	},
	RuleTerseParagraph: {
		code:    "PRF7002",
		catches: "a paragraph or list item of agent instructions over 60 words",
		why: "House style: over the same skills 642 paragraphs and items ran p50 27 words, p90 64; caps " +
			"near p95 trimmed only 5.5% of the bytes, so the cap sits below p90.",
	},
	RuleBareRule: {
		code:    "PRF7003",
		catches: "\"rule\" in agent instructions with no mechanism named",
		why: "House style: in this repository's skills a rule is only what magus enforces, and the rest " +
			"is an instruction.",
	},
	RuleTemplate: {
		code:    "PRF7004",
		catches: "an agent-instructions template that does not render, so neither of its forms can be judged",
		why: "House style: the template form is internal/agent's. A body that does not render is " +
			"judged by this rule alone, and passes where it is off.",
	},
	RuleReplyOpener: {
		code:    "PRF8001",
		catches: "a sentence of a review reply that opens by contradicting (\"No,\", \"As I said\")",
		why: "A contradiction first reads as winning an argument whatever follows it. It measured no " +
			"false positive, so it denies.",
	},
	RuleJudgmentAsFact: {
		code:    "PRF8002",
		catches: "a recommendation in a review reply stated as a fact, with no reason given",
		why: "\"This should be a map\" leaves the author to guess why; the reason, or a label saying it " +
			"is the writer's call, invites an answer. It advises: a modal also states requirements.",
	},
	RuleStackedHedge: {
		code:    "PRF8003",
		catches: "two softeners in one sentence of a review reply, or an apology before its point",
		why: "Stacked softeners read as unsure of a point the writer has. It advises: one tell proves " +
			"nothing.",
	},
	RuleLongThread: {
		code:    "PRF8004",
		catches: "a review reply that is its author's fourth or later in a thread",
		why: "A long exchange in text reads as a stalemate to the people watching it, and a call settles " +
			"it faster. It needs the thread length (-thread-length), and stays silent without it.",
	},
	RuleMessageLength: {
		code:    "PRF9001",
		catches: "a message longer than its rune cap, 160 unless the caller names one",
		why: "A message is read in a terminal at the moment something went wrong: about two lines hold " +
			"a verdict, one command and a ref, and the rationale belongs behind the ref.",
	},
	RuleMessageRationale: {
		code:    "PRF9002",
		catches: "a message that joins more than one reason (so, because, a semicolon, \", which\")",
		why: "One reason names the cause; a second is an argument the reader did not ask for at the " +
			"moment of the failure. The ref holds the rest.",
	},
	RuleMessageCommands: {
		code:    "PRF9003",
		catches: "a message naming more than one backticked command",
		why: "A reader runs the first command a message names. A second competes with it, so a message " +
			"names the one to run first.",
	},
	RuleMessageTag: {
		code:    "PRF9004",
		catches: "a message opening with a component tag (\"server: \") or carrying a marker such as \"[AGENT]\"",
		why: "A tag names who spoke, which the reader already knows, and pushes the verdict off the " +
			"start of the line.",
	},
}
