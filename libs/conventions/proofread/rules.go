package proofread

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Budgets measured 2026-10-06 over 46664 comment blocks: a block's p50 is 25
// words, p90 72, p99 168; a sentence's p50 is 17 words, p90 34, p99 50.
const (
	maxBlockWords    = 250
	maxSentenceWords = 60
)

// fillerWords match case-sensitively. Throat-clearing opens a sentence
// (`Note that`), so it matches only capitalized: `a note that` names the
// notes feature. The adverbs are filler wherever they sit.
const fillerWords = `Note that|Please note|It should be noted|It is worth noting|` +
	`It's worth noting|It is important to|It's important to|` +
	`[Ss]imply|[Bb]asically|[Ee]ssentially|[Nn]eedless to say`

// docFillerWords open a doc comment where the symbol's name belongs. In a
// reply or a description they point at the code under discussion ("This
// function returns a Result, so ..."), and `this function's job` is a
// contract.
const docFillerWords = `This function|This method`

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
	fillerPattern        = regexp.MustCompile(`\b(?:` + fillerWords + `|` + docFillerWords + `)\b`)
	writtenFillerPattern = regexp.MustCompile(`\b(?:` + fillerWords + `|` + writtenFillerWords + `)\b`)
)

// mereLead is the word before a "just" that means merely: a copula, or the
// contracted one in "it's".
var mereLead = wordSet("is", "are", "was", "were", "be", "s")

// mereNot are the negated copulas, after which "just" still means merely.
// After any other "n't" ("don't just dig", "wouldn't just stopping") it
// states a contrast.
var mereNot = wordSet("isn't", "aren't", "wasn't", "weren't")

// sameLead is the word before a "very" that means the same one.
var sameLead = wordSet("the", "this", "that")

// measured are the words after a "very" that grades a size or a position the
// sentence turns on: "a very long path" is the case a socket limit hits, and
// "the very first run" picks one out.
var measured = wordSet("long", "short", "large", "small", "big", "high", "low", "few", "many", "old",
	"first", "last", "end", "beginning", "start", "top", "bottom", "early", "late")

// fillerExempt reports a widened word used in a sense that carries meaning.
// Lowercase "just" means recency ("you just installed"), only
// ("extracts just the binary") or contrast ("not just cores") everywhere but
// after a copula, where it means merely ("it is just files"), unless it
// compares ("just as true"), dates a participle ("were just squashed") or
// limits a literal ("be just `VERSION`"). "Just" opening a sentence is an
// imperative's minimizer, unless a participle makes it recency ("Just
// pushed a fix"). "very" after the, this or that means the same one, and
// before a [measured] word it grades what the sentence turns on.
//
// Lowercase "actually" states a contrast with what seemed or was configured
// ("what the cache actually did", "wait for it to actually exit"); only the
// sentence opener "Actually," is filler. "more robust" and "less robust"
// compare. A word hyphenated into a compound ("all-powerful",
// "just-in-time") is part of another word.
func fillerExempt(text string, at []int) bool {
	if (at[0] > 0 && text[at[0]-1] == '-') || (at[1] < len(text) && text[at[1]] == '-') {
		return true
	}

	word := text[at[0]:at[1]]
	next := strings.TrimRight(strings.Fields(text[at[1]:] + " .")[0], ".,;:!?)")

	switch strings.ToLower(word) {
	case "just":
		if word == "Just" {
			return strings.HasSuffix(next, "ed")
		}

		return !merely(text, at[0]) || next == "as" || strings.HasSuffix(next, "ed") ||
			strings.HasPrefix(next, "#") || strings.IndexFunc(next, unicode.IsDigit) == 0
	case "very":
		return sameLead[prevWord(text, at[0])] || measured[strings.ToLower(next)]
	case "actually":
		return word == "actually"
	case "robust":
		prev := prevWord(text, at[0])

		return prev == "more" || prev == "less"
	}

	return false
}

// merely reports whether the word before at makes a "just" there mean merely:
// a copula, or a negated one.
func merely(text string, at int) bool {
	prev := prevWord(text, at)
	if prev != "t" {
		return mereLead[prev]
	}

	fields := strings.Fields(text[:at])
	last := strings.ReplaceAll(strings.ToLower(fields[len(fields)-1]), "’", "'")

	return mereNot[last]
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
				Replacements: []string{""},
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
				Replacements: []string{matchCase(want, m)},
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

// matchCase capitalizes replacement's first letter when m opens with a
// capital, so a replacement at the start of a sentence still opens it.
func matchCase(replacement, m string) string {
	r, _ := utf8.DecodeRuneInString(m)
	if !unicode.IsUpper(r) || replacement == "" {
		return replacement
	}

	first, n := utf8.DecodeRuneInString(replacement)

	return string(unicode.ToUpper(first)) + replacement[n:]
}

// asciiFor spells each mark [ascii] reports with its ASCII equivalent. An
// emoji has none: what it stood for takes words.
var asciiFor = map[string]string{"‘": "'", "’": "'", "“": `"`, "”": `"`, "…": "..."}

// dashClause are the words that open a clause a comma joins: a dash before
// one of them separates, where before any other word it introduces.
var dashClause = wordSet("then", "and", "but", "so", "or", "which", "who", "not", "because", "while", "though",
	"although", "since", "unless", "until", "where", "when", "yet")

// dashReplacement is what to write in place of the dash at text[from:to], the spaces
// around it included: "-" in a numeric range, ", " for one of a pair of
// dashes in a sentence or before a joined clause, and ": " where the dash
// introduces what follows.
func dashReplacement(text string, from, to int) string {
	before, after := text[:from], text[to:]

	switch {
	case before != "" && after != "" && isDigit(before[len(before)-1]) && isDigit(after[0]):
		return "-"
	case strings.ContainsAny(sentenceAround(before, after), "—–"), dashClause[strings.ToLower(firstWord(strings.TrimSpace(after)))]:
		return ", "
	}

	return ": "
}

// sentenceAround is the rest of the sentence either side of a dash.
func sentenceAround(before, after string) string {
	if at := strings.LastIndexAny(before, ".!?"); at >= 0 {
		before = before[at+1:]
	}

	if at := strings.IndexAny(after, ".!?"); at >= 0 {
		after = after[:at]
	}

	return before + after
}

// headingWord is a word of a heading, or a code span, which stays as written.
var headingWord = regexp.MustCompile("`[^`]*`|[\\p{L}][\\p{L}'-]*")

// sentenceCase lowers the first letter of each word of heading after the
// first. A word with a capital past its first letter ("GitHub", "API") or a
// code span is a name and keeps its case; a proper noun spelled like any
// other word cannot be told apart and is lowered.
func sentenceCase(heading string) string {
	first := true

	return headingWord.ReplaceAllStringFunc(heading, func(w string) string {
		if first || strings.HasPrefix(w, "`") {
			first = false

			return w
		}

		_, n := utf8.DecodeRuneInString(w)
		if strings.IndexFunc(w[n:], unicode.IsUpper) >= 0 {
			return w
		}

		return strings.ToLower(w[:n]) + w[n:]
	})
}
