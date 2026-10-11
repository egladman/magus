package prose

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Line budgets for a CSS comment, by where it sits. They carry the Go comment
// budget over (hack/policy/comments.buzz: 3 lines inside a body, 4 above an
// unexported declaration, 8 above an exported one): a header earns what a
// package comment earns, a comment above a rule what a doc on an unexported
// declaration earns, and a comment inside a body or after a declaration what
// an inline one earns.
const (
	budgetFile        = 8
	budgetRule        = 4
	budgetSection     = 3
	budgetDeclaration = 3
	budgetInline      = 3
	budgetTrailing    = 2
)

// A rule block earns a base plus a share of its size, the way a longer function
// earns a longer comment: baseBlock lines, and one more for each declsPerLine
// statements directly inside it. The comments charged to a block are the one
// above its header and every one inside it that sits on one of its statements.
const (
	baseBlock    = 6
	declsPerLine = 5
)

func placeBudget(p cssPlace) int {
	switch p {
	case placeFile:
		return budgetFile
	case placeRule:
		return budgetRule
	case placeSection:
		return budgetSection
	case placeDeclaration:
		return budgetDeclaration
	case placeTrailing:
		return budgetTrailing
	}

	return budgetInline
}

func placeName(p cssPlace) string {
	switch p {
	case placeFile:
		return "heading a file"
	case placeRule:
		return "above a rule"
	case placeSection:
		return "between rules"
	case placeDeclaration:
		return "above a declaration"
	case placeTrailing:
		return "after a declaration"
	}

	return "inside a block"
}

func blockBudget(decls int) int { return baseBlock + decls/declsPerLine }

func blockMessage(header string, n, budget, decls int) string {
	return fmt.Sprintf("Block '%s' carries %d comment lines against a budget of %d for its %d statements: "+
		"keep the why the CSS cannot show, and move the rest to docs.", clip(header, 60), n, budget, decls)
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}

	return s[:n] + "..."
}

// commentBudget reports a CSS comment over the lines its place earns.
func commentBudget(in input) []Finding {
	n := in.css.lines
	budget := placeBudget(in.css.place)

	if n <= budget {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Comment %s runs %d lines against a budget of %d: keep the why the CSS "+
			"cannot show, and move the rest to docs.", placeName(in.css.place), n, budget),
	}}
}

// cssStubWords pad a CSS property or selector into a comment, stemmed the way
// [words] stems: "resets" is reset, and "applies" stems to applie.
var cssStubWords = wordSet("reset", "remove", "clear", "apply", "applie", "use", "add", "override",
	"force", "zero", "none", "no", "default", "hide", "show", "rule", "declaration", "property",
	"selector", "style")

// restates reports a one-line CSS comment that only names the rule or the
// declaration it sits on. It is the doc stub rule with the code as the symbol.
func restates(in input) []Finding {
	text, ok := stub(in.symbol, cssStubWords)
	if !ok {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Comment only repeats the code it sits on (%s); say why it is written this "+
			"way, or delete it.", clip(in.symbol.Name, 60)),
		Match: text,
	}}
}

// cssHistoryPatterns add to historyPatterns for a CSS comment. A stylesheet's
// comments narrate the change more than a doc does: what a rule replaced, what
// it was before, which work item built it. Bare "now" and "instead of" stay out
// for the reason historyPatterns leaves them out.
var cssHistoryPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bno longer\b`),
	regexp.MustCompile(`(?i)\bnow (?:uses|reads|is|are|has|have|lives|sits|carries|renders|owns|gets|keeps|` +
		`takes|ships|handles|goes|comes|applies|wins)\b`),
	regexp.MustCompile(`(?i)\b(?:was|were) (?:once|originally|previously|replaced|removed|moved|dropped|` +
		`renamed|changed|fixed|split|merged|migrated|rewritten|ported)\b`),
	regexp.MustCompile(`(?i)\b(?:formerly|originally)\b`),
	regexp.MustCompile(`(?i)\bthe (?:old|former|previous|legacy|prior)\b`),
	regexp.MustCompile(`\b[A-Za-z]+-era\b`),
	regexp.MustCompile(`(?i)\bshipped (?:briefly|earlier|before)\b`),
	regexp.MustCompile(`(?i)\bfix(?:es|ed) (?:a|an|the|this|that|it)\b`),
	regexp.MustCompile(`\bW[0-9]+\b`),
}

// Banned words, from hack/lint/banned-words.buzz and the owner's rule against
// the first two in code. Each names nothing, and the fix says what the thing
// is. A word in backticks or in double quotes is a mention and passes. Two are
// spelled in halves so that lint does not read this file as a use of them.
var (
	bannedPlain = regexp.MustCompile(`(?i)\b(?:(stor(?:y|ies))|(chapters?)|(` + "la" + `nes?)|(` + "cor" + `pus|` +
		"cor" + `pora)|(hand-?offs?))\b`)

	// The frames below are the ones banned-words.buzz refuses for the third word: a
	// determiner or a listed modifier before the noun, matched for number so a
	// verb passes.
	nounOne = regexp.MustCompile(`(?i)\b(?:a|an|this|every|each|one|another|neither|either|per)\s+surface\b`)

	nounMany = regexp.MustCompile(`(?i)\b(?:both|two|three|four|five|these|those|several|all)\s+surfaces\b`)

	nounAny = regexp.MustCompile(`(?i)\b(?:the|its|their|our|other|same|own|whole|full|any|no|some|my|your)\s+surfaces?\b`)

	nounModifierOne = regexp.MustCompile(`(?i)\b(?:agent|mcp|cli|api|command|path|guard|console|diff|review|` +
		`interactive|public|buzz|http|hook|magusfile|status|file|spawn|config|template|machine|server|` +
		`client|authoring|loopback|graph|activity|dashboard|plan|share|std|ide|top-level|everyday|plugin|` +
		`collision|install|management|editor|terminal|catalog|discovery|network|mutating|typed|generated|` +
		`stepping|comparison|preview|playground|command-line|module|host|magus|script|read|write|tool|` +
		`test|run)\s+surface\b`)

	nounModifierMany = regexp.MustCompile(`(?i)\b(?:public|interactive|top-level|everyday|mutating|typed|` +
		`generated|command-line|loopback)\s+surfaces\b`)

	nounCompound = regexp.MustCompile(`(?i)\b(?:([a-z]+)-surfaces?|surfaces?-[a-z]+|surface\s+area)\b`)
)

var bannedFix = [...]string{
	"",
	"Say what it is: the feature, the flow, the page.",
	"Say what it is: a section, a part, a step.",
	"Say \"write paths\", or what it means here.",
	"Say \"the cases\" or \"the inputs\".",
	"Say what was passed, to whom, and why.",
}

const nounFix = "Say what the thing is: the CLI, the page, the API. A verb this refuses reads better reworded " +
	"(\"appears on\", \"shows\")."

// bannedWord reports a word this repository does not use in a comment.
func bannedWord(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentions(in.kind)) {
		for _, at := range bannedPlain.FindAllStringSubmatchIndex(para.text, -1) {
			group := 0

			for g := 1; g < len(at)/2; g++ {
				if at[2*g] >= 0 {
					group = g
				}
			}

			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop '%s': %s", m, bannedFix[group]), Match: m, Line: para.lineAt(at[0]),
			})
		}

		for _, at := range nounSpans(para.text) {
			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop '%s': %s", m, nounFix), Match: m, Line: para.lineAt(at[0]),
			})
		}
	}

	return out
}

// nounSpans returns where text uses the word as a noun: after a determiner
// or a listed modifier, in a hyphen compound other than re-, or as the word
// area. The verb ("this surfaces it") passes.
func nounSpans(text string) [][]int {
	if !strings.Contains(strings.ToLower(text), "surface") {
		return nil
	}

	var out [][]int

	for _, re := range []*regexp.Regexp{nounOne, nounMany, nounAny, nounModifierOne, nounModifierMany} {
		out = append(out, re.FindAllStringIndex(text, -1)...)
	}

	for _, at := range nounCompound.FindAllStringSubmatchIndex(text, -1) {
		if at[2] >= 0 {
			lead := strings.ToLower(text[at[2]:at[3]])
			if lead == "re" || lead == "un" {
				continue
			}
		}

		out = append(out, at[:2])
	}

	return withoutOverlaps(out)
}

// withoutOverlaps keeps the first of any spans that overlap, so one use of a
// word is one finding however many frames match it.
func withoutOverlaps(spans [][]int) [][]int {
	slices.SortFunc(spans, func(a, b []int) int {
		if a[0] != b[0] {
			return a[0] - b[0]
		}

		return b[1] - a[1]
	})

	var out [][]int

	for _, s := range spans {
		if len(out) > 0 && s[0] < out[len(out)-1][1] {
			continue
		}

		out = append(out, s)
	}

	return out
}
