package proofread

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

const (
	// RuleParticiples reports a text whose present participial clauses (",
	// highlighting the", "Using the cache, ...") run over a rate per 1000
	// words.
	RuleParticiples Rule = "participles"
	// RuleNominalizations reports a text whose nominalizations (-tion, -ment,
	// -ity, -ance, -ence, -ness, -ism nouns) run over a rate per 1000 words.
	RuleNominalizations Rule = "nominalizations"
)

// densityKinds are the kinds written as running prose long enough for a rate
// over the whole text to mean something.
var densityKinds = []Kind{KindChangeDescription, KindReference, KindGuide, KindIssue}

var densityChecks = []check{
	{rule: RuleParticiples, on: densityKinds, advise: densityKinds, judge: participles},
	{rule: RuleNominalizations, on: densityKinds, advise: densityKinds, judge: nominalizations},
}

var densityTexts = map[Rule]ruleText{
	RuleParticiples: {
		code:    "PRF4020",
		catches: "a text that hangs present participial clauses on its sentences at a generated-writing rate",
		why: "Reinhart et al. (PNAS 2025) measured GPT-4o using present participial clauses at 5.3 times " +
			"the human rate. The rule counts a comma before an -ing word that opens a clause, and an " +
			"-ing word opening a sentence whose comma closes the clause, per 1000 words, over texts of 200 " +
			"words or more. Measured 2026-10-10: 808 agent pull request bodies from the AIDev set ran " +
			"3.9 per 1000 pooled (p50 3.7), and 30 percent run over the cap of 5; this repository's 96 " +
			"hand-written docs pages ran 1.5 (p90 3.1, 2 percent over) and its 61 commit bodies 0.5 (none " +
			"over), both written by people and agents together. Review comments ran 0.6 by people and 2.6 " +
			"by bots, pooled. The docs are a different genre from a pull request, and the rule cannot " +
			"tell a participle from a gerund without a tagger, so it advises.",
	},
	RuleNominalizations: {
		code:    "PRF4021",
		catches: "a text whose verbs are turned into nouns (-tion, -ment, -ity, -ness) at a generated-writing rate",
		why: "Reinhart et al. (PNAS 2025) measured GPT-4o using nominalizations at 2.1 times the human " +
			"rate. A suffix is a guess at a nominalization, so the rule advises over a whole text and " +
			"never points at one word. Over texts of 200 words or more, measured 2026-10-10: 808 agent " +
			"pull request bodies from the AIDev set ran 55.2 per 1000 pooled (p50 49.8), and 79 percent " +
			"run over the cap of 30; this repository's 96 hand-written docs pages ran 12.2 (p90 20.3, 1 " +
			"percent over) and its 61 commit bodies 12.6 (2 percent over), both written by people and " +
			"agents together. Review comments ran 14.5 by people and 35.4 by bots, pooled.",
	},
}

// Rates per 1000 words, and the shortest text they judge: under 200 words one
// participial clause already reads as a rate over the cap. Each rule's why
// records the measurement.
const (
	maxParticiplesPerK     = 5.0
	maxNominalizationsPerK = 30.0
	minDensityWords        = 200
)

// participleTail is a comma, then an -ing word opening a clause.
var participleTail = regexp.MustCompile(`,\s+([a-z]{2,}ing)\b`)

// participleOpener is an -ing word opening a sentence.
var participleOpener = regexp.MustCompile(`^([A-Z][a-z]+ing)\b`)

// notParticiples are -ing words that are nouns, prepositions or other words
// no participial clause opens with.
var notParticiples = wordSet(
	"thing", "things", "nothing", "something", "anything", "everything", "string", "strings", "during",
	"bring", "king", "ring", "sing", "wing", "spring", "swing", "sting", "morning", "evening", "ceiling",
	"sibling", "siblings", "including", "according", "regarding", "concerning", "following", "pending",
	"notwithstanding", "excluding", "padding", "settings", "heading", "warning", "warnings",
)

// participles reports a text whose participial clauses run over
// [maxParticiplesPerK].
func participles(in input) []Finding {
	words, hits := 0, 0

	for _, para := range densityParagraphs(in) {
		words += countWords(para.text)

		for _, m := range participleTail.FindAllStringSubmatchIndex(para.text, -1) {
			if participleClause(para.text, m[2], m[3]) {
				hits++
			}
		}

		for _, s := range sentenceBounds(para.text) {
			sentence := strings.TrimLeft(para.text[s[0]:s[1]], "*_( ")
			if m := participleOpener.FindStringSubmatch(sentence); m != nil && openerClause(sentence, m[1]) {
				hits++
			}
		}
	}

	return densityFinding(words, hits, maxParticiplesPerK, "present participial clauses",
		"Give each -ing clause its own sentence with a subject, or cut the clause.")
}

// participleClause reports whether the -ing word at text[from:to] after a
// comma opens a clause, rather than naming a noun or standing in a series of
// -ing words ("parsing, linking and running").
func participleClause(text string, from, to int) bool {
	word := text[from:to]
	if notParticiples[word] {
		return false
	}

	if strings.HasSuffix(prevWord(text, from), "ing") {
		return false
	}

	rest := strings.TrimLeft(text[to:], " ")
	if rest == "" || strings.ContainsRune(",.;:)!?", rune(rest[0])) {
		return false
	}

	next := strings.Fields(rest)[0]

	return next != "and" && next != "or"
}

// openerClause reports whether sentence, which opens with the -ing word, is a
// participial clause closed by a comma before its main clause, rather than a
// gerund subject ("Running the suite takes a minute.").
func openerClause(sentence, word string) bool {
	if notParticiples[strings.ToLower(word)] {
		return false
	}

	comma := strings.IndexByte(sentence, ',')
	if comma < 0 {
		return false
	}

	return len(strings.Fields(sentence[:comma])) <= 12
}

// nominalSuffix is a word ending in a suffix that turns a verb or an
// adjective into a noun.
var nominalSuffix = regexp.MustCompile(`\b[A-Za-z][a-z]{2,}(?:tions?|ments?|ities|ity|ances?|ences?|ness(?:es)?|isms?)\b`)

// notNominal are words with a nominalizing suffix that name a thing in
// technical text, or are verbs, or carry the suffix by accident.
var notNominal = wordSet(
	"function", "functions", "section", "sections", "question", "questions", "station", "caption",
	"portion", "portions", "edition", "fraction", "junction", "exception", "exceptions", "collection",
	"collections", "partition", "partitions", "application", "applications", "transaction", "transactions",
	"comment", "comments", "document", "documents", "argument", "arguments", "element", "elements",
	"environment", "environments", "statement", "statements", "moment", "moments", "segment", "segments",
	"fragment", "fragments", "experiment", "experiments", "instrument", "department", "government",
	"apartment", "implement", "implements", "increment", "increments", "decrement", "supplement",
	"complement", "augment", "compliment", "garment", "pigment", "sediment", "torment", "lament",
	"entity", "entities", "identity", "identities", "utility", "utilities", "university", "community",
	"quantity", "sentence", "sentences", "sequence", "sequences", "science", "audience", "reference",
	"references", "instance", "instances", "distance", "balance", "glance", "finance", "licence",
	"conference", "evidence", "business", "witness", "harness", "harnesses", "mechanism", "mechanisms",
)

// nominalizations reports a text whose nominalizations run over
// [maxNominalizationsPerK].
func nominalizations(in input) []Finding {
	words, hits := 0, 0

	for _, para := range densityParagraphs(in) {
		words += countWords(para.text)

		for _, w := range nominalSuffix.FindAllString(para.text, -1) {
			if len(w) >= 7 && !notNominal[strings.ToLower(w)] {
				hits++
			}
		}
	}

	return densityFinding(words, hits, maxNominalizationsPerK, "nominalizations",
		"Turn the nouns back into verbs where an actor does something: 'the validation of the key' "+
			"becomes 'it validates the key'.")
}

// densityParagraphs are the paragraphs a rate counts over: a heading is a
// label, and code and quoted words are mentions.
func densityParagraphs(in input) []paragraph {
	var out []paragraph

	for _, para := range paragraphs(in.prose, mentionsMasked) {
		if !para.head.heading {
			out = append(out, para)
		}
	}

	return out
}

// countWords counts the words of text, a masked span counting as none.
func countWords(text string) int {
	n := 0

	for _, f := range strings.Fields(text) {
		if strings.IndexFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) >= 0 {
			n++
		}
	}

	return n
}

// densityFinding reports hits over words as one finding for the whole text
// when the text holds at least [minDensityWords] and the rate is over limit.
func densityFinding(words, hits int, limit float64, what, fix string) []Finding {
	if words < minDensityWords || words == 0 {
		return nil
	}

	rate := float64(hits) * 1000 / float64(words)
	if rate <= limit {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("%d %s in %d words, %.1f per 1000 (the cap is %.0f): %s", hits, what, words, rate,
			limit, fix),
	}}
}
