package proofread

import (
	"fmt"
	"regexp"
	"slices"
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
var densityKinds = []Kind{KindChangeDescription, KindReference, KindPrinciple, KindGuide, KindIssue}

var densityChecks = []check{
	{rule: RuleParticiples, on: densityKinds, graded: true, judge: participles},
	{rule: RuleNominalizations, on: densityKinds, graded: true, judge: nominalizations},
}

var densityTexts = map[Rule]ruleText{
	RuleParticiples: {
		code:      "PRF4020",
		dimension: DimensionEconomy,
		catches:   "a text that hangs present participial clauses on its sentences at a generated-writing rate",
		why: "Reinhart et al. (PNAS 2025) measured GPT-4o using present participial clauses at 5.3 times " +
			"the human rate. The rule counts a comma before an -ing word that opens a clause, and an " +
			"-ing word opening a sentence whose comma closes the clause, per 1000 words, over texts of 200 " +
			"words or more. Measured 2026-10-10: 808 agent pull request bodies from the AIDev set ran " +
			"3.9 per 1000 pooled (p50 3.7), and 30 percent run over the cap of 5; this repository's 96 " +
			"hand-written docs pages ran 1.5 (p90 3.1, 2 percent over) and its 61 commit bodies 0.5 (none " +
			"over), both written by people and agents together. Review comments ran 0.6 by people and 2.6 " +
			"by bots, pooled. The docs are a different genre from a pull request, and the rule cannot " +
			"tell a participle from a gerund without a tagger, so it advises. Measured 2026-10-11 over the " +
			"adjudicated labeling round 2: 81.0% precision (34 of 42) before, 85.0% (34 of 40) after. A " +
			"clause naming an issue (\"addressing #2782\") and an open task item a template wrote are no " +
			"longer counted. The rest turn on whether a tail claims a benefit or states a fact (\", " +
			"renaming the types\"), which the rule cannot read. It stays off until a fresh labeling round " +
			"measures it. A finding quotes each clause it counted, so its reader can check them, and a " +
			"platform's setup tips are not the author's text and are not counted. Labeling round 5 " +
			"measured 60.0% (24 of 40, lower bound 0.45); its wrong firings counted a coding agent's quoted " +
			"prompt and pasted summaries, so those are blanked with their words, and nouns, so an -ing word " +
			"opening a bare item of a series (\", logging methods, and\"), the subject of a verb (\", caching " +
			"is\") or a bold label is no longer counted. Labeling round 6 measured 82.5% (33 of 40, lower bound " +
			"0.68). Six of its seven wrong firings counted an adjective before its noun (\"existing code\", " +
			"\"outgoing calls\"), a gerund opening the main clause after an opening element (\"Currently, " +
			"setting X behaves\") or an item of a series of -ing topics, which are no longer counted. Labeling " +
			"round 7 measured 95.0% (38 of 40, lower bound 0.81 at z 2.24); a person spot-checked ten " +
			"agreed-correct labels, so it advises.",
	},
	RuleNominalizations: {
		code:      "PRF4021",
		dimension: DimensionEconomy,
		catches:   "a text whose verbs are turned into nouns (validation, agreement, stability) at a generated-writing rate",
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
// [maxParticiplesPerK], quoting each clause it counted in text order.
func participles(in input) []Finding {
	words := 0

	var clauses []string

	for _, para := range densityParagraphs(in) {
		words += countWords(para.text)

		var at []int

		for _, m := range participleTail.FindAllStringSubmatchIndex(para.text, -1) {
			if participleClause(para.text, m[2], m[3]) && !citesReference(para.text[m[3]:]) {
				at = append(at, m[2])
			}
		}

		for _, s := range sentenceBounds(para.text) {
			sentence := strings.TrimLeft(para.text[s[0]:s[1]], "*_( ")
			if m := participleOpener.FindStringSubmatch(sentence); m != nil && openerClause(sentence, m[1]) {
				at = append(at, s[1]-len(sentence))
			}
		}

		slices.Sort(at)

		for _, i := range at {
			clauses = append(clauses, clauseText(para.text[i:]))
		}
	}

	return densityFinding(words, len(clauses), clauses, maxParticiplesPerK, "present participial clauses",
		"Give each -ing clause its own sentence with a subject, or cut the clause.")
}

// maxClauseWords is how much of a counted clause a finding quotes: enough to
// find it in the text.
const maxClauseWords = 6

// clauseText is the clause text opens, up to the punctuation that closes it,
// at most [maxClauseWords] words.
func clauseText(text string) string {
	if end := strings.IndexAny(text, ",.;:!?()\n"); end >= 0 {
		text = text[:end]
	}

	words := strings.Fields(text)
	if len(words) > maxClauseWords {
		return strings.Join(words[:maxClauseWords], " ") + " ..."
	}

	return strings.Join(words, " ")
}

// issueRef is an issue or pull request a clause names.
var issueRef = regexp.MustCompile(`#\d+\b`)

// citesReference reports whether the clause rest opens names an issue, which
// makes it a checkable fact ("addressing issue #2782"), not a claim.
func citesReference(rest string) bool {
	if end := strings.IndexAny(rest, ",.;:"); end >= 0 {
		rest = rest[:end]
	}

	return issueRef.MatchString(rest)
}

// participleClause reports whether the -ing word at text[from:to] after a
// comma opens a clause, rather than naming a noun or standing in a series of
// -ing words ("parsing, linking and running").
func participleClause(text string, from, to int) bool {
	word := text[from:to]
	if notParticiples[word] {
		return false
	}

	rest := strings.TrimLeft(text[to:], " ")
	if rest == "" || strings.ContainsRune(",.;:)!?", rune(rest[0])) {
		return false
	}

	next := strings.Fields(rest)[0]
	if next == "and" || next == "or" {
		return false
	}

	comma := strings.LastIndexByte(text[:from], ',')

	return !gerundSubject(rest) && !attributive(word, rest) && !opensMainClause(text, comma, to) &&
		!listedNoun(text, from, to) && !parallelItem(text, comma, to)
}

// attributiveOnly are -ing words of verbs that take no object, so a noun
// right after one is the noun it modifies: "existing code", "outgoing calls".
var attributiveOnly = wordSet("existing", "preexisting", "coexisting", "outgoing", "incoming", "upcoming",
	"ongoing", "underlying", "corresponding", "resulting", "occurring", "recurring", "working", "competing",
	"conflicting", "differing", "lingering", "dangling")

// notNouns are words after an -ing word that open no noun phrase:
// prepositions and adverbs objectWords leaves out ("working around").
var notNouns = wordSet("and", "or", "but", "around", "through", "across", "without", "against", "via", "under",
	"after", "before", "between", "within", "toward", "towards", "alongside", "behind", "like", "together",
	"again", "alone", "fine", "well", "still", "now", "too", "here", "there", "where", "when", "while")

// attributive reports whether word, an -ing word of a verb that takes no
// object, is followed by a bare noun it modifies rather than a phrase of its
// own: "existing code unchanged", but "working around the race".
func attributive(word, rest string) bool {
	if !attributiveOnly[strings.ToLower(word)] {
		return false
	}

	next := phraseWords(rest, 1)
	if len(next) == 0 {
		return false
	}

	return !objectWords[next[0]] && !notNouns[next[0]] && !strings.HasSuffix(next[0], "ly")
}

// introWords open an element that leads a sentence into its main clause: a
// subordinate clause ("when the dialog opens") or a prepositional phrase ("on
// Windows").
var introWords = wordSet("when", "whenever", "if", "unless", "once", "after", "before", "since", "because",
	"while", "although", "though", "until", "as", "where", "in", "on", "at", "for", "with", "without",
	"during", "from", "by", "under", "within", "across", "through", "throughout", "per", "unlike", "upon",
	"despite", "between", "among", "via")

// clauseBreak ends a sentence or a clause. A period inside a word
// ("UnitTests.Parallel", "v1.2") ends nothing.
var clauseBreak = regexp.MustCompile(`[.!?](?:\s|$)|[:;\n]`)

// sentenceRest is text up to the end of its sentence.
func sentenceRest(text string) string {
	if at := clauseBreak.FindStringIndex(text); at != nil {
		return text[:at[0]]
	}

	return text
}

// clauseSubjects open a main clause after a comma: "using the cache, we".
var clauseSubjects = wordSet("we", "i", "you", "it", "they", "he", "she", "this", "these", "the", "a", "an")

// opensMainClause reports whether the -ing word after the comma at
// text[comma], whose phrase continues at text[to:], opens its sentence's main
// clause as its subject ("Currently, setting X behaves", "When it is hidden,
// closing it becomes"), not a tail. Every comma-separated element before it is
// one word or opens with an [introWords] word. A phrase whose own comma is
// followed by a subject ("However, using X, we add") is a participial clause.
func opensMainClause(text string, comma, to int) bool {
	if comma < 0 {
		return false
	}

	start := 0
	if ends := clauseBreak.FindAllStringIndex(text[:comma], -1); len(ends) > 0 {
		start = ends[len(ends)-1][1]
	}

	for _, element := range strings.Split(text[start:comma], ",") {
		words := strings.Fields(strings.ToLower(strings.Trim(element, " \t\r*_-([")))
		if len(words) == 0 || len(words) > 1 && !introWords[words[0]] {
			return false
		}
	}

	if _, after, ok := strings.Cut(sentenceRest(text[to:]), ","); ok {
		if next := phraseWords(after, 1); len(next) == 1 && clauseSubjects[next[0]] {
			return false
		}
	}

	return true
}

// parallelItem reports whether the -ing phrase after the comma at text[comma]
// is an item of a series of -ing items its sentence closes with "and" or "or":
// "cover moving pages, moving to the root, error handling, and missing keys".
// The item before the comma holds an -ing word past its first word, this item
// has a complement of its own, and every item between it and the closing
// "and" is a short bare noun phrase.
func parallelItem(text string, comma, to int) bool {
	if comma < 0 {
		return false
	}

	before := text[strings.LastIndexAny(text[:comma], ",.;:!?(\n")+1 : comma]

	words := strings.Fields(strings.ToLower(before))
	if !slices.ContainsFunc(words[min(1, len(words)):], func(w string) bool {
		w = strings.TrimRight(w, "`*_)")

		return len(w) > 5 && strings.HasSuffix(w, "ing") && !notParticiples[w]
	}) {
		return false
	}

	after := sentenceRest(text[to:])

	// A bare item can take the series as its object: "emphasizing precision,
	// performance, and testing".
	items := strings.Split(after, ",")
	if !slices.ContainsFunc(strings.Fields(strings.ToLower(items[0])), func(w string) bool { return objectWords[w] }) {
		return false
	}

	for _, item := range items[1:] {
		words := strings.Fields(strings.ToLower(item))
		if len(words) > 0 && (words[0] == "and" || words[0] == "or") {
			return true
		}

		if len(words) == 0 || len(words) > maxListedWords || slices.ContainsFunc(words, func(w string) bool {
			return objectWords[w]
		}) {
			return false
		}
	}

	return false
}

// subjectVerbs are the finite verbs that make an -ing phrase before them its
// subject ("caching is", "clicking Submit should"): a participial clause
// takes an object or a complement, never a verb of its own.
var subjectVerbs = wordSet("is", "was", "isn't", "wasn't", "has", "hasn't", "does", "doesn't", "should",
	"shouldn't", "will", "won't", "would", "wouldn't", "can", "can't", "cannot", "could", "couldn't", "must",
	"may", "might", "shall")

// objectWords open an object or a complement, so an -ing word they follow
// takes one: "using the lock", "making it faster", "leading to a retry".
var objectWords = wordSet("the", "a", "an", "this", "that", "these", "those", "it", "its", "them", "their",
	"they", "us", "our", "you", "your", "him", "his", "her", "me", "my", "each", "every", "all", "any",
	"some", "no", "to", "for", "with", "by", "in", "on", "at", "from", "of", "into", "as", "up", "out",
	"back", "down", "off", "over", "more", "less", "both", "either", "such", "what", "how", "whether", "if")

// gerundSubject reports whether rest, the words after an -ing word, makes
// it the subject of a verb: a finite verb right after it, or a modal after
// the one word it names ("clicking `Submit` should"). Only a modal can follow
// that word, since "ensuring tokens are" takes a clause as its object.
func gerundSubject(rest string) bool {
	words := phraseWords(rest, 3)
	if len(words) > 0 && subjectVerbs[words[0]] {
		return true
	}

	if len(words) < 2 || objectWords[words[0]] {
		return false
	}

	switch words[1] {
	case "is", "was", "has", "does", "isn't", "wasn't", "hasn't", "doesn't":
		return false
	}

	return subjectVerbs[words[1]]
}

// maxListedWords bounds a noun phrase standing as an item of a series:
// "constants, logging methods, and", "(ranges, crossing zero)".
const maxListedWords = 3

// listedNoun reports whether the -ing word at text[from:to] opens a short
// noun phrase standing as an item of a series rather than a clause. No object
// or complement follows it. The item closes at a parenthesis, at the comma
// before a series' closing "and", or at a comma or the paragraph's end after
// an item as short and as bare. A clause closed by a period ("causing
// retries.") stays one.
func listedNoun(text string, from, to int) bool {
	item := text[to:]

	// A paragraph is one list item, so its end closes the item.
	closer, after := byte(0), ""
	if end := strings.IndexAny(item, ",.;:!?()"); end >= 0 {
		closer, after = item[end], item[end+1:]
	}

	if strings.IndexByte(".;:!?", closer) >= 0 {
		return false
	}

	words := phraseWords(item, maxListedWords)
	if 1+len(words) > maxListedWords {
		return false
	}

	for _, w := range words {
		if objectWords[w] || w == "and" || w == "or" {
			return false
		}
	}

	// A parenthesis, or the "and" closing a series, makes it an item.
	if next := phraseWords(after, 1); closer == '(' || closer == ')' ||
		closer == ',' && len(next) == 1 && (next[0] == "and" || next[0] == "or") {
		return true
	}

	// The item before is a bare noun phrase too: "It fails, causing retries"
	// and "Fixed the race, ensuring order" are clauses.
	before := text[:from]
	before = before[:strings.LastIndexByte(before, ',')]
	before = before[strings.LastIndexAny(before, ",.;:!?(")+1:]

	if countWords(before) > maxListedWords+1 {
		return false
	}

	for _, w := range strings.Fields(strings.ToLower(before)) {
		if objectWords[w] {
			return false
		}
	}

	return true
}

// phraseWords returns up to n lowercase words opening s, stopping at the
// punctuation that closes the phrase.
func phraseWords(s string, n int) []string {
	if end := strings.IndexAny(s, ",.;:!?()"); end >= 0 {
		s = s[:end]
	}

	words := strings.Fields(strings.ToLower(s))
	if len(words) > n {
		words = words[:n]
	}

	return words
}

// openerClause reports whether sentence, which opens with the -ing word, is a
// participial clause closed by a comma before its main clause, rather than a
// gerund subject ("Running the suite takes a minute."), an adjective
// ("Existing code works") or a label ("**Adding shortcuts**: tests ..."). A
// comma inside a parenthesis the phrase opened closes nothing.
func openerClause(sentence, word string) bool {
	if notParticiples[strings.ToLower(word)] || attributive(word, sentence[len(word):]) {
		return false
	}

	comma := strings.IndexByte(sentence, ',')
	if comma < 0 {
		return false
	}

	head := sentence[:comma]
	if strings.ContainsRune(head, ':') || strings.Count(head, "(") > strings.Count(head, ")") {
		return false
	}

	return len(strings.Fields(head)) <= 12
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

	return densityFinding(words, hits, nil, maxNominalizationsPerK, "nominalizations",
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
// counted, when given, are the hits as the text reads them, quoted after the
// rate so a reader can check each one.
func densityFinding(words, hits int, counted []string, limit float64, what, fix string) []Finding {
	if words < minDensityWords || words == 0 {
		return nil
	}

	rate := float64(hits) * 1000 / float64(words)
	if rate <= limit {
		return nil
	}

	named := ""

	if len(counted) > 0 {
		quoted := make([]string, len(counted))
		for i, c := range counted {
			quoted[i] = "'" + c + "'"
		}

		named = strings.Join(quoted, ", ") + ". "
	}

	return []Finding{{
		Message: fmt.Sprintf("%d %s in %d words, %.1f per 1000 (the cap is %.0f): %s%s", hits, what, words, rate,
			limit, named, fix),
	}}
}
