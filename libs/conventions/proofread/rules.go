package proofread

import (
	"fmt"
	"regexp"
	"strings"
)

// Budgets measured 2026-10-06 over 46664 comment blocks: a block's p50 is 25
// words, p90 72, p99 168; a sentence's p50 is 17 words, p90 34, p99 50.
const (
	maxBlockWords    = 250
	maxSentenceWords = 60
)

// fillerWords match case-sensitively. Throat-clearing opens a sentence
// (`Note that`, `This function`), so those match only capitalized:
// `a note that` names the notes feature, and `this function's job` is a
// contract. The adverbs are filler wherever they sit.
const fillerWords = `Note that|Please note|It should be noted|It is worth noting|` +
	`It's worth noting|It is important to|It's important to|This function|This method|` +
	`[Ss]imply|[Bb]asically|[Ee]ssentially|[Nn]eedless to say`

// writtenFillerWords widen fillerWords for Markdown and pull requests with
// adverbs, selling words and stock sentence openers, which are filler wherever
// they sit, so they match in either case like the adverbs. Doc comments keep
// the narrower list until a sweep clears the wider one from them.
//
// The selling words are the one list this repository holds a commit message to
// as well. "genuinely", "literally", "honestly" and "inherently" are left out:
// each also states a contrast ("paths stage literally").
const writtenFillerWords = `[Jj]ust|[Rr]eally|[Vv]ery|[Aa]ctually|[Ll]everag(?:e|es|ed|ing)|[Uu]tiliz(?:e|es|ed|ing)|` +
	`(?i:robust(?:ly)?|comprehensive(?:ly)?|seamless(?:ly)?|powerful(?:ly)?|elegant(?:ly)?|cutting-edge|` +
	`state-of-the-art|world-class|effortless(?:ly)?|` +
	`truly|deeply|fundamentally|inevitably|interestingly|importantly|crucially|` +
	`at (?:its|their) core|at the end of the day|when it comes to|in a world where|in today'?s|` +
	`the reality is|in reality|the real (?:question|issue|problem) is|what (?:really|truly) matters|` +
	`the deeper (?:issue|problem|question)|the heart of the matter|` +
	`(?:important|critical|crucial|essential|vital) to (?:note|remember|recognize|keep in mind)|` +
	`worth noting|may vary)`

var (
	fillerPattern        = regexp.MustCompile(`\b(?:` + fillerWords + `)\b`)
	writtenFillerPattern = regexp.MustCompile(`\b(?:` + fillerWords + `|` + writtenFillerWords + `)\b`)
)

// mereLead is the word before a "just" that means merely: a copula, or the
// contracted one in "it's" and "isn't".
var mereLead = wordSet("is", "are", "was", "were", "be", "s", "t")

// sameLead is the word before a "very" that means the same one.
var sameLead = wordSet("the", "this", "that")

// fillerExempt reports a widened word used in a sense that carries meaning.
// Lowercase "just" means recency ("you just installed"), only
// ("extracts just the binary") or contrast ("not just cores") everywhere but
// after a copula, where it means merely ("it is just files"), unless it
// compares ("just as true") or dates a participle ("were just squashed").
// "Just" opening a sentence is an imperative's minimizer. "very" after the,
// this or that means the same one.
func fillerExempt(text string, at []int) bool {
	switch text[at[0]:at[1]] {
	case "just":
		next := strings.TrimRight(strings.Fields(text[at[1]:] + " .")[0], ".,;:!?)")

		return !mereLead[prevWord(text, at[0])] || next == "as" || strings.HasSuffix(next, "ed")
	case "very", "Very":
		return sameLead[prevWord(text, at[0])]
	}

	return false
}

// termsPattern holds the spellings docs/glossary.md replaces. "Magus" is left
// out: in a doc it usually names the Go type, which no rule can tell from the
// tool.
var termsPattern = regexp.MustCompile(`(?i)\bsub-agents?\b`)

// nameSuffixPattern matches a name whose last word is Of or For, in camel or
// snake case, after at least one other word. A name ending in Of or For
// describes its argument rather than what it returns.
var nameSuffixPattern = regexp.MustCompile(`\b(?:\w*[a-z0-9](?:Of|For)|\w+_(?:of|for))\b`)

// commentBlock budgets each paragraph rather than the whole doc, the scope
// Vale judged the rule at.
func commentBlock(in input) []Finding {
	var words []int

	for _, ln := range in.prose {
		if ln.paragraph || len(words) == 0 {
			words = append(words, 0)
		}

		words[len(words)-1] += len(strings.Fields(ln.body()))
	}

	var out []Finding

	for _, n := range words {
		if n > maxBlockWords {
			out = append(out, Finding{
				Message: "Keep a comment paragraph under 250 words: say why, and move the rest to docs.",
			})
		}
	}

	return out
}

func commentSentence(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, keep) {
		n := 0
		for _, token := range strings.Fields(para.text) {
			n++

			if !endsSentence(token) {
				continue
			}

			if n > maxSentenceWords {
				out = append(out, Finding{Message: "Keep a comment sentence under 60 words: split it."})
			}

			n = 0
		}

		if n > maxSentenceWords {
			out = append(out, Finding{Message: "Keep a comment sentence under 60 words: split it."})
		}
	}

	return out
}

// endsSentence reports whether token closes a sentence, looking past closing
// quotes, brackets and emphasis.
func endsSentence(token string) bool {
	token = strings.TrimRight(token, `"')]*_`+"`")

	return strings.HasSuffix(token, ".") || strings.HasSuffix(token, "!") || strings.HasSuffix(token, "?")
}

func filler(in input) []Finding {
	pattern := fillerPattern
	if in.kind != KindDocComment {
		pattern = writtenFillerPattern
	}

	var out []Finding

	for _, para := range paragraphs(in.prose, mentions(in.kind)) {
		for _, at := range fillerSpans(para.text, pattern) {
			m := para.text[at[0]:at[1]]
			out = append(out, Finding{
				Message: fmt.Sprintf("Drop '%s': state the fact.", m), Match: m, Line: para.lineAt(at[0]),
			})
		}
	}

	return out
}

// fillerSpans returns where pattern finds filler in text, less the words
// [fillerExempt] reads as carrying meaning.
func fillerSpans(text string, pattern *regexp.Regexp) [][]int {
	var out [][]int

	for _, at := range pattern.FindAllStringIndex(text, -1) {
		if !fillerExempt(text, at) {
			out = append(out, at)
		}
	}

	return out
}

func terms(in input) []Finding {
	var out []Finding

	for _, para := range paragraphs(in.prose, mentions(in.kind)) {
		for _, at := range termsPattern.FindAllStringIndex(para.text, -1) {
			m := para.text[at[0]:at[1]]

			want := "subagent"
			if strings.HasSuffix(strings.ToLower(m), "s") {
				want += "s"
			}

			out = append(out, Finding{
				Message: fmt.Sprintf("Write '%s', not '%s'.", want, m), Match: m, Line: para.lineAt(at[0]),
			})
		}
	}

	return out
}

func nameSuffix(in input) []Finding {
	if !in.symbol.Callable {
		return nil
	}

	m := nameSuffixPattern.FindString(in.symbol.Name)
	if m == "" {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Rename '%s': no function or method name ends in the word Of or For.", m),
		Match:   m,
	}}
}

func keep(s string) string { return s }
