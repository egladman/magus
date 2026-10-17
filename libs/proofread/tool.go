package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	// RuleToolLead reports a description whose first sentence does not say what
	// the tool does or when to call it.
	RuleToolLead Rule = "tool-lead"
	// RuleToolBoundary reports a description that names no case where the tool
	// is the wrong choice and no tool to use instead.
	RuleToolBoundary Rule = "tool-boundary"
	// RuleToolLength reports a description over 1024 runes.
	RuleToolLength Rule = "tool-length"
	// RuleToolSelling reports a word that sells the tool rather than describes it.
	RuleToolSelling Rule = "tool-selling"
)

const (
	toolLeadMinWords = 4
	toolLeadMaxWords = 40
	toolRunes        = 1024
)

var toolKind = []Kind{KindToolDescription}

var toolChecks = []check{
	{rule: RuleToolLead, on: toolKind, advise: toolKind, judge: toolLead},
	{rule: RuleToolBoundary, on: toolKind, advise: toolKind, judge: toolBoundary},
	{rule: RuleToolLength, on: toolKind, advise: toolKind, judge: toolLength},
	{rule: RuleToolSelling, on: toolKind, judge: tellJudge("Drop '%s': say what the tool does, not how good it is.", toolSelling...)},
}

var toolTexts = map[Rule]ruleText{
	RuleToolLead: {
		code:      "PRF9020",
		dimension: DimensionStructure,
		catches: "a description whose first sentence opens on the tool itself, holds under 4 words or runs past " +
			"40 words",
		why: "A 2026 study of 856 MCP tool descriptions found 56% did not state their purpose clearly, and a " +
			"model choosing among many tools has only the description to go on. Over the 22 descriptions " +
			"magus ships (6 MCP tools, 16 skills) the first sentence runs 7 to 45 words and none opens on " +
			"\"This tool\" or on the tool's own name. 2 pass 40 words: magus-context-audit (42) and " +
			"magus-diagram (45), each a list of nouns before the verb's object. The rule only advises, since " +
			"2 firings are too few to deny on.",
	},
	RuleToolBoundary: {
		code:      "PRF9021",
		dimension: DimensionEvidence,
		catches:   "a description that names no case where the tool is the wrong choice and no tool to use instead",
		why: "Among many tools whose descriptions all say what they do, the one that also says when not to use " +
			"it is the one the model can rule out. Over the 22 descriptions magus ships, 19 name a boundary " +
			"(\"Do NOT\", \"instead\", \"rather than\", \"last resort\", \"cannot\", \"use the client tool\") " +
			"and 3 do not: the status tool, magus-run and magus-workspace-rules. The rule only advises, since " +
			"whether a boundary is worth stating depends on what the other tools do.",
	},
	RuleToolLength: {
		code:      "PRF9022",
		dimension: DimensionEconomy,
		catches:   "a tool or skill description over 1024 runes",
		why: "Every description is in the model's context in every session, whether or not the tool is called. " +
			"Over the 22 descriptions magus ships the median is 586 runes (96 words), 20 sit between 98 and " +
			"704, and the 2 MCP tools client (1207) and diff (1144) run past 1024, the length the Agent " +
			"Skills format allows a skill description. The house paragraph cap of 60 words (terse-paragraph) " +
			"would fire on 19 of the 22, so it is not used here. The rule only advises while 2 firings are " +
			"all there are.",
	},
	RuleToolSelling: {
		code:      "PRF9023",
		dimension: DimensionStance,
		catches:   "a word that sells the tool (powerful, seamless, effortless, state-of-the-art) or a buzzword",
		why: "A selling word names no behavior the model can test a request against, so it adds length without " +
			"adding a reason to pick the tool. The buzzword list the other kinds use is reused, and the " +
			"words below are added for descriptions. Over the 22 descriptions magus ships, 0 hold one, so the " +
			"rule denies on the same footing as buzzword: a hit is a word to replace with what is so.",
	},
}

// toolSelling are the tells of RuleToolSelling: the buzzwords other kinds
// refuse, and the words a product page uses to sell what a description should
// only state.
var toolSelling = append(slices.Clone(buzzwords),
	anywhere(`(?i)\b(?:powerful(?:ly)?|seamless(?:ly)?|effortless(?:ly)?|cutting-edge|best-in-class|`+
		`state-of-the-art|world-class|revolutionary|supercharg(?:e|es|ed|ing)|blazing(?:ly)?[ -]fast|`+
		`industry-leading|next-generation|magic(?:al)?(?:ly)?)\b`))

// toolSelfReference matches a first sentence that names the tool, or opens on
// a noun phrase for the kind of thing it is, before saying what it does.
var toolSelfReference = regexp.MustCompile(
	`(?i)^(?:(?:this|the|a|an|our)\s+(?:\S+\s+)?(?:tool|skill|function|command|helper|utility|plugin|server)\b|` +
		`[\w-]+\s+(?:is|are)\s+(?:a|an|the)\b)`)

// toolBoundaryPhrase matches words that bound a tool's use: a refusal, a case
// to avoid, or another tool to use. A code span is blanked first.
var toolBoundaryPhrase = regexp.MustCompile(
	`(?i)\b(?:do(?:es)? not|don't|doesn't|never|cannot|can't|not for|instead|rather than|last resort|` +
		`only (?:when|if|for)|use (?:the |a |an )?\S+ (?:tool|skill|cli|command)|use \S+ (?:for|when)|` +
		`refus(?:e|es|ed)|read-only)\b`)

// judgeTool judges one tool's or skill's description as plain text: no
// Markdown is read, so a backtick still marks a literal. A URL is not prose.
func judgeTool(raw []string, o options) []Finding {
	lines := make([]string, len(raw))
	for i, ln := range raw {
		lines[i] = bareURL.ReplaceAllString(ln, "")
	}

	return run(input{
		kind: KindToolDescription, prose: readProse(lines, false), lines: lines, source: raw,
		text: strings.Join(raw, "\n"), opts: o,
	})
}

func toolLead(in input) []Finding {
	paras := paragraphs(in.prose, mentions(in.kind))
	if len(paras) == 0 {
		return nil
	}

	para := paras[0]

	spans := sentenceSpans(para.text)
	if len(spans) == 0 {
		return nil
	}

	lead := spans[0]

	end := len(para.text)
	if len(spans) > 1 {
		end = spans[1].start
	}

	sentence := strings.TrimSpace(para.text[lead.start:end])
	line := para.lineAt(lead.start)

	switch {
	case toolSelfReference.MatchString(sentence):
		return []Finding{{
			Message: "Open with what the tool does or when to call it: the first sentence names the tool itself.",
			Match:   firstWords(sentence, 4),
			Line:    line,
		}}
	case lead.words < toolLeadMinWords:
		return []Finding{{
			Message: fmt.Sprintf("Open with what the tool does or when to call it: the first sentence has %d words.",
				lead.words),
			Line: line,
		}}
	case lead.words > toolLeadMaxWords:
		return []Finding{{
			Message: fmt.Sprintf("Say what the tool does or when to call it within %d words (the first sentence has %d): "+
				"a model choosing among many may read no further.", toolLeadMaxWords, lead.words),
			Line: line,
		}}
	}

	return nil
}

func firstWords(s string, n int) string {
	words := strings.Fields(s)
	if len(words) > n {
		words = words[:n]
	}

	return strings.Join(words, " ")
}

func toolBoundary(in input) []Finding {
	if strings.TrimSpace(in.text) == "" || toolBoundaryPhrase.MatchString(blankBackticks(in.text)) {
		return nil
	}

	return []Finding{{
		Message: "Say when not to use the tool, or which tool to use instead: a model choosing among many can " +
			"only rule this one out if it is told.",
		Line: 1,
	}}
}

func toolLength(in input) []Finding {
	n := utf8.RuneCountInString(strings.TrimSpace(in.text))
	if n <= toolRunes {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Keep a description to %d runes (this has %d): every session pays for it, so move "+
			"the detail into the tool's parameters or its output.", toolRunes, n),
		Line: 1,
	}}
}
