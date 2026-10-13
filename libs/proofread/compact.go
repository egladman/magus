package proofread

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// RuleCompactFiller reports the articles, linking verbs and filler words
	// compact agent text can drop, with the tokens dropping them saves.
	RuleCompactFiller Rule = "compact-filler"
	// RuleCompactMarkup reports a bold or italic marker, or a Markdown table,
	// in compact agent text.
	RuleCompactMarkup Rule = "compact-markup"
	// RuleCompactCostlyAbbrev reports an abbreviation that costs more tokens
	// than the word it replaces: k8s, w/, b/c, e.g., i.e., a word with its
	// vowels dropped.
	RuleCompactCostlyAbbrev Rule = "compact-costly-abbrev"
	// RuleCompactSymbolNegation reports "!" or a Unicode symbol standing in
	// for "not" or a logic operator.
	RuleCompactSymbolNegation Rule = "compact-symbol-negation"
	// RuleCompactLine reports a line of compact agent text holding more than
	// one fact.
	RuleCompactLine Rule = "compact-line"
	// RuleCompactDuplicate reports a line of compact agent text that restates
	// an earlier line.
	RuleCompactDuplicate Rule = "compact-duplicate"
)

var compactChecks = []check{
	{rule: RuleCompactFiller, on: compact, advise: compact, judge: compactFiller},
	{rule: RuleCompactMarkup, on: compact, advise: compact, judge: compactMarkup},
	{rule: RuleCompactCostlyAbbrev, on: compact, advise: compact, judge: compactCostlyAbbrev},
	{rule: RuleCompactSymbolNegation, on: compact, advise: compact, judge: compactSymbolNegation},
	{rule: RuleCompactLine, on: compact, advise: compact, judge: compactLine},
	{rule: RuleCompactDuplicate, on: compact, advise: compact, judge: compactDuplicate},
}

const compactEvidence = " The measurements are in the 2026-10-13 machine-text research " +
	"(research-machine-text.md in the proofread roadmap)."

var compactTexts = map[Rule]ruleText{
	RuleCompactFiller: {
		code:      "PRF7010",
		dimension: DimensionEconomy,
		catches: "articles, linking verbs, \"you\", \"please\", \"note that\" and \"instead\" outside code, " +
			"counted with the tokens dropping them saves",
		why: "Each of these words is one token in tiktoken o200k and cl100k (Claude's tokenizer is not " +
			"public), so the saving is counted at one token a word. They are what the deletion-only " +
			"compressors drop first: LLMLingua-2 (Pan et al., Findings ACL 2024) holds BBH at 70.02 against " +
			"70.07 at 3x by deleting words alone. A 2026-10-13 eval over three magus tasks found the skills " +
			"rewritten to this floor 14.9% shorter than the short form, and they passed 9 of 9 runs against " +
			"the full form's 8 of 9. Stopword removal past that floor hurts code tasks (Yang et al., arXiv " +
			"2410.22793), so the rule names only these words and never a negation, an obligation, a scope, " +
			"a condition or an order." + compactEvidence,
	},
	RuleCompactMarkup: {
		code:      "PRF7011",
		dimension: DimensionEconomy,
		catches:   "a bold or italic marker, or a Markdown table, in compact agent text",
		why: "Measured in tiktoken o200k: bold plus backticks takes a line from 5 tokens to 8, and the same " +
			"two facts cost 24 tokens as a Markdown table, 13 as a list and 11 as key: value lines. An " +
			"agent acts on the words, not their emphasis." + compactEvidence,
	},
	RuleCompactCostlyAbbrev: {
		code:      "PRF7012",
		dimension: DimensionEconomy,
		catches:   "an abbreviation that costs more tokens than the word it replaces: k8s, w/, b/c, e.g., i.e., or dropped vowels",
		why: "Measured in tiktoken o200k: \"k8s\" is 3 tokens and \"Kubernetes\" 1; \"w/\", \"b/c\", " +
			"\"e.g.\" and \"i.e.\" each cost 1 more than the word; \"cnfgrtn\" is 4, \"mssg\" 3 and " +
			"\"cmmt\" 2, against 1 each for the whole word. Telegraph English (Arbuzov et al., arXiv " +
			"2605.04426) ranks the most abbreviated versions last. An abbreviation earns its place only " +
			"where it replaces a word of 2 or more tokens." + compactEvidence,
	},
	RuleCompactSymbolNegation: {
		code:      "PRF7013",
		dimension: DimensionEvidence,
		catches:   "\"!\" or a Unicode symbol standing in for \"not\" or a logic operator",
		why: "Negated framing was the main failure Elkins and Chun measured (arXiv 2601.09724): some models " +
			"endorsed an action 80-97% of the time when asked whether it should not be done. MetaGlyph (van " +
			"Gassen, arXiv 2601.07354) saved 62-81% of tokens with symbols, but Claude Haiku 4.5 read its " +
			"set-membership symbol right 26% of the time, and the not-equal and therefore signs are 2 tokens " +
			"each in o200k. " +
			"Negation stays a word." + compactEvidence,
	},
	RuleCompactLine: {
		code:      "PRF7014",
		dimension: DimensionStructure,
		catches: fmt.Sprintf("a compact line over %d words that joins clauses with a semicolon or a comma and "+
			"a conjunction", maxCompactLineWords),
		why: "The floor is one fact per line. IFScale (Jaroslawicz et al., arXiv 2507.11538) found the best " +
			"model follows 68% of 500 instructions, and an instruction packed beside another on one line " +
			"is the easier one to miss. A line counts as more than one fact when it runs long and joins " +
			"clauses; the length is the 90th percentile of the 1041 prose lines of the skills rewritten " +
			"to the floor on 2026-10-13." + compactEvidence,
	},
	RuleCompactDuplicate: {
		code:      "PRF7015",
		dimension: DimensionEconomy,
		catches: "a compact line that restates an earlier one: half or more of its content words and every " +
			"negation, condition, scope and order word the same",
		why: "Anthropic's skill guidance asks for the smallest set of high-signal tokens and one term per " +
			"idea, and a restated line spends its tokens twice. The rewrite to the floor on 2026-10-13 " +
			"merged lines like these by hand. A line restates another only when its negation, obligation, " +
			"scope, condition and order words match too, since those are what a merge must keep. Over the " +
			"1041 lines that rewrite left, it fires twice, both restatements the merges missed." +
			compactEvidence,
	},
}

// compactFillers are the words the compact floor drops: articles, linking
// verbs, "you" and the filler words. Each is one token, so the count of them
// is the saving.
var compactFillers = wordSet(
	"a", "an", "the", "is", "are", "was", "were", "be", "been", "being", "am", "you", "please", "instead",
)

// compactWord is a word as the compact rules read one: an identifier written
// outside code keeps its underscores and hyphens, so is_valid is not "is".
var compactWord = regexp.MustCompile(`[A-Za-z][A-Za-z0-9_'-]*`)

var noteThat = regexp.MustCompile(`(?i)\bnote that\b`)

// compactFiller reports the filler of the whole text at once: one finding a
// text, since a finding per word would bury every other rule.
func compactFiller(in input) []Finding {
	counts := map[string]int{}
	first, total := 0, 0

	for _, ln := range in.prose {
		body := mentionsMasked(ln.body())
		notes := len(noteThat.FindAllStringIndex(body, -1))
		counts["note that"] += notes
		found := 2 * notes

		for _, w := range compactWord.FindAllString(noteThat.ReplaceAllString(body, ""), -1) {
			if w = strings.ToLower(w); compactFillers[w] {
				counts[w]++
				found++
			}
		}

		if found > 0 && first == 0 {
			first = ln.line
		}

		total += found
	}

	if total == 0 {
		return nil
	}

	words := make([]string, 0, len(counts))
	for w, n := range counts {
		if n > 0 {
			words = append(words, w)
		}
	}

	slices.SortFunc(words, func(a, b string) int {
		if d := counts[b] - counts[a]; d != 0 {
			return d
		}

		return strings.Compare(a, b)
	})

	listed := make([]string, len(words))
	for i, w := range words {
		listed[i] = fmt.Sprintf("'%s' %d", w, counts[w])
	}

	return []Finding{{
		Message: fmt.Sprintf("Drop %s (%s): about %s saved at 1 token a word. Keep every negation, condition, "+
			"scope and order word.", counted(total, "filler word"), strings.Join(listed, ", "), counted(total, "token")),
		Line: first,
	}}
}

var (
	compactBold   = regexp.MustCompile(`(?:\*\*|__)[^*_\s][^*_]*?(?:\*\*|__)`)
	compactItalic = regexp.MustCompile(`(?:^|[^\w*])(\*[^*\s][^*]*?\*|_[^_\s][^_]*?_)(?:[^\w*]|$)`)
)

func compactMarkup(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		body := blankBackticks(ln.body())

		for _, m := range compactBold.FindAllString(body, -1) {
			out = append(out, markupFinding(m, 2, ln.line))
		}

		for _, m := range compactItalic.FindAllStringSubmatch(compactBold.ReplaceAllString(body, ""), -1) {
			out = append(out, markupFinding(m[1], 1, ln.line))
		}
	}

	for i, ln := range unfenced(in.source) {
		if i > 0 && tableDivider.MatchString(ln) && strings.Contains(in.source[i-1], "|") {
			out = append(out, Finding{
				Message: "Rewrite the table as key: value lines: two facts cost 24 tokens as a table and 11 as lines.",
				Line:    i,
			})
		}
	}

	return out
}

func markupFinding(m string, marker, line int) Finding {
	plain := m[marker : len(m)-marker]

	return Finding{
		Message:      fmt.Sprintf("Drop the emphasis in '%s': the markers cost tokens and an agent acts on the words.", m),
		Match:        m,
		Line:         line,
		Replacements: []string{plain},
	}
}

// costlyAbbrevs are the abbreviations tiktoken o200k counts at more tokens
// than the word they replace, each with that word.
var costlyAbbrevs = []struct{ abbrev, word string }{
	{"k8s", "Kubernetes"},
	{"w/o", "without"},
	{"w/", "with"},
	{"b/c", "because"},
	{"e.g.", "such as"},
	{"i.e.", "that is"},
	{"cnfgrtn", "configuration"},
	{"mssg", "message"},
	{"cmmt", "commit"},
	{"affctd", "affected"},
	{"tst", "test"},
}

// costlyAbbrev matches each of costlyAbbrevs as a word of its own: "w/" in a
// path is not one.
var costlyAbbrev = func() *regexp.Regexp {
	alts := make([]string, len(costlyAbbrevs))
	for i, a := range costlyAbbrevs {
		alts[i] = regexp.QuoteMeta(a.abbrev)
	}

	return regexp.MustCompile(`(?i)(?:^|[\s(])(` + strings.Join(alts, "|") + `)(?:[\s.),;:]|$)`)
}()

func compactCostlyAbbrev(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		body := mentionsMasked(ln.body())

		for _, m := range costlyAbbrev.FindAllStringSubmatch(body, -1) {
			word := ""

			for _, a := range costlyAbbrevs {
				if strings.EqualFold(a.abbrev, m[1]) {
					word = a.word

					break
				}
			}

			out = append(out, Finding{
				Message: fmt.Sprintf("Write '%s', not '%s': the abbreviation costs more tokens than the word.",
					word, m[1]),
				Match: m[1], Line: ln.line, Replacements: []string{word},
			})
		}
	}

	return out
}

// symbolWords are the Unicode logic symbols, each with the words to write.
var symbolWords = map[string]string{
	"\u00ac": "not", "\u2260": "not equal", "\u2227": "and", "\u2228": "or", "\u2200": "every", "\u2203": "some",
	"\u2208": "in", "\u2209": "not in", "\u2234": "so", "\u2235": "because", "\u21d2": "then",
	"\u21d4": "if and only if", "\u2192": "->", "\u2282": "in", "\u2286": "in",
}

var (
	bangNot     = regexp.MustCompile(`(?:^|[\s(\[,;:])(![A-Za-z][\w-]*)`)
	logicSymbol = regexp.MustCompile(`[\x{00ac}\x{2260}\x{2227}\x{2228}\x{2200}\x{2203}\x{2208}\x{2209}\x{2234}\x{2235}\x{21d2}\x{21d4}\x{2192}\x{2282}\x{2286}]`)
)

func compactSymbolNegation(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		body := mentionsMasked(ln.body())

		for _, m := range bangNot.FindAllStringSubmatch(body, -1) {
			out = append(out, Finding{
				Message: fmt.Sprintf("Write 'not %s', not '%s': negation stays a word.", m[1][1:], m[1]),
				Match:   m[1], Line: ln.line, Replacements: []string{"not " + m[1][1:]},
			})
		}

		for _, m := range logicSymbol.FindAllString(body, -1) {
			out = append(out, Finding{
				Message: fmt.Sprintf("Write '%s', not '%s': a logic symbol is read wrong more often than its word.",
					symbolWords[m], m),
				Match: m, Line: ln.line, Replacements: []string{symbolWords[m]},
			})
		}
	}

	return out
}

// maxCompactLineWords is the 90th percentile of the 1041 prose lines of the
// 16 skills rewritten to the compact floor on 2026-10-13 (the c2 eval
// variant), a code span counted as one word: p50 11, p75 16, p90 21, p95 24.
const maxCompactLineWords = 21

// clauseJoin is a join between two clauses on one line.
var clauseJoin = regexp.MustCompile(`;|,\s+(?:and|or|but|so|then)\b`)

func compactLine(in input) []Finding {
	var out []Finding

	for _, ln := range in.prose {
		if ln.heading {
			continue
		}

		body := blankBackticks(ln.body())
		if n := len(strings.Fields(body)); n > maxCompactLineWords && clauseJoin.MatchString(body) {
			out = append(out, Finding{
				Message: fmt.Sprintf("Split this line: %d words joining clauses. One fact per line.", n),
				Line:    ln.line,
			})
		}
	}

	return out
}

// keepWords are the words a compact rewrite never drops: negation,
// obligation, scope, condition and order.
var keepWords = wordSet(
	"not", "never", "no", "without", "must", "only", "every", "all", "any", "each", "except", "if", "unless",
	"when", "until", "before", "after", "then", "first", "last",
)

// Two lines restate each other when their content words overlap by
// duplicateOverlap (Jaccard) and each holds at least duplicateMinWords: below
// that, two short lines naming one command read alike and say different things.
const (
	duplicateOverlap  = 0.5
	duplicateMinWords = 4
)

var (
	functionWords  = wordSet(shapeFunctionWords...)
	duplicateStops = wordSet("via", "per", "use", "uses", "using")
)

// lineWords are a line's content words, its code spans each one word, and the
// keep words it holds.
type lineWords struct {
	text    string
	content map[string]bool
	keep    string
}

func readLineWords(ln proseLine) lineWords {
	body := ln.body()
	out := lineWords{text: strings.TrimSpace(body), content: map[string]bool{}}

	for _, span := range codeSpan.FindAllString(body, -1) {
		out.content[span] = true
	}

	var keep []string

	for _, w := range compactWord.FindAllString(codeSpan.ReplaceAllString(body, " "), -1) {
		switch w = strings.ToLower(w); {
		case keepWords[w]:
			keep = append(keep, w)
		case len(w) > 1 && !functionWords[w] && !duplicateStops[w]:
			out.content[w] = true
		}
	}

	slices.Sort(keep)
	out.keep = strings.Join(slices.Compact(keep), " ")

	return out
}

func compactDuplicate(in input) []Finding {
	var (
		out  []Finding
		seen []lineWords
	)

	for _, ln := range in.prose {
		if ln.heading {
			continue
		}

		cur := readLineWords(ln)
		if len(cur.content) < duplicateMinWords {
			continue
		}

		for _, prev := range seen {
			if prev.keep == cur.keep && overlap(prev.content, cur.content) >= duplicateOverlap {
				out = append(out, Finding{
					Message: fmt.Sprintf("Merge this line into the earlier '%s', which says the same: keep "+
						"every negation, condition, scope and order word.", lineOpening(prev.text)),
					Line: ln.line,
				})

				break
			}
		}

		seen = append(seen, cur)
	}

	return out
}

// overlap is the Jaccard index of a and b.
func overlap(a, b map[string]bool) float64 {
	n := shared(a, b)

	return float64(n) / float64(len(a)+len(b)-n)
}

// shared counts the words of a that b also holds.
func shared(a, b map[string]bool) int {
	n := 0

	for w := range a {
		if b[w] {
			n++
		}
	}

	return n
}

// lineOpening is the first words of a line, enough to find it by.
func lineOpening(line string) string {
	const words = 8

	if f := strings.Fields(line); len(f) > words {
		return strings.Join(f[:words], " ") + " ..."
	}

	return line
}

// counted is n of noun, plural past one.
func counted(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}

	return fmt.Sprintf("%d %ss", n, noun)
}
