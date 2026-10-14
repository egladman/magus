package proofread

import (
	"bytes"
	"compress/gzip"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/proofread/postag"
)

// tagged is one sentence as the grammar measures read it: its words as
// [postag.Tokenize] splits them, lowercased, and their tags.
type tagged struct {
	words []string
	tags  []postag.Tagged
}

// grammarFeatures are the rates of [Shape.Grammar]. Each approximates a
// feature of Biber, Variation across Speech and Writing (1988), numbered as
// his Appendix II numbers them; Reinhart et al., PNAS 122 (2025) e2422455122,
// compared their rates in GPT-4o and human text.
var grammarFeatures = []struct {
	name    string
	matches func(s tagged, i int) bool
}{
	// Biber's present and past participial clauses (25, 26): GPT-4o wrote the
	// present ones 5.3 times as often as people (Reinhart et al.).
	{"participial_clause", participialClause},
	// Biber's nominalizations (14): 2.1 times as often (Reinhart et al.).
	{"nominalization", nominalization},
	// Biber's agentless passive (17): about half as often (Reinhart et al.).
	{"agentless_passive", agentlessPassive},
	// Biber's phrasal coordination (64), which he counts for "and" alone: 1.9
	// times as often (Reinhart et al.).
	{"phrasal_coordination", phrasalCoordination},
	// Biber's downtoners (46): every model underused them (Rallapalli et al.,
	// arXiv 2604.14111).
	{"downtoner", downtoner},
	// Biber's concessive adverbial subordinators (36): underused by models
	// (Rallapalli et al.).
	{"concessive_though", concessive},
	// Biber's present participial WHIZ deletion relatives (28), "the event
	// causing this decline".
	{"present_participle_postmodifier", participlePostmodifier},
}

// templateLength is the shortest POS n-gram Shaib et al. count as a template.
const templateLength = 4

// grammarMeasures are the [Shape] fields read from part-of-speech tags.
type grammarMeasures struct {
	rates          map[string]float64
	templateRate   float64
	tagCompression float64
}

// measureGrammar measures sentences, the masked sentences [MeasureShape]
// splits, its rates per 1000 of words.
func measureGrammar(sentences [][]string, words int) grammarMeasures {
	counts := make([]int, len(grammarFeatures))

	var sequence []postag.UPOS

	for _, sentence := range sentences {
		s := tagSentence(sentence)

		for i := range s.words {
			for f, feature := range grammarFeatures {
				if feature.matches(s, i) {
					counts[f]++
				}
			}
		}

		for _, t := range s.tags {
			sequence = append(sequence, t.UPOS)
		}
	}

	rates := make(map[string]float64, len(grammarFeatures))
	for f, feature := range grammarFeatures {
		rates[feature.name] = round2(1000 * float64(counts[f]) / float64(words))
	}

	return grammarMeasures{
		rates:          rates,
		templateRate:   round4(templateRate(sequence)),
		tagCompression: round4(tagCompression(sequence)),
	}
}

func tagSentence(sentence []string) tagged {
	var prose []string

	for _, w := range sentence {
		if w != codeWord {
			prose = append(prose, w)
		}
	}

	var words []string

	for _, w := range postag.Tokenize(strings.Join(prose, " ")) {
		// Markdown emphasis is layout, which [Shape.Layout] counts.
		if strings.Trim(w, "*_") != "" {
			words = append(words, w)
		}
	}

	tags := postag.Tag(words)
	for i, w := range words {
		words[i] = strings.ReplaceAll(strings.ToLower(w), "’", "'")
	}

	return tagged{words: words, tags: tags}
}

// templateRate adapts Shaib et al., "Detection and Measurement of Syntactic
// Templates in Generated Text" (EMNLP 2024), from a dataset to one text: the
// share of 4-gram positions whose UPOS 4-gram occurs at least twice in the
// text. They count a template across a dataset's texts, and their rate is the
// share of texts holding one.
func templateRate(sequence []postag.UPOS) float64 {
	positions := len(sequence) - templateLength + 1
	if positions <= 0 {
		return 0
	}

	counts := map[[templateLength]postag.UPOS]int{}
	for i := range positions {
		counts[[templateLength]postag.UPOS(sequence[i:i+templateLength])]++
	}

	repeated := 0

	for i := range positions {
		if counts[[templateLength]postag.UPOS(sequence[i:i+templateLength])] > 1 {
			repeated++
		}
	}

	return float64(repeated) / float64(positions)
}

// tagCompression is Shaib et al.'s CR-POS turned over: the gzip size of the
// space-joined UPOS sequence over its raw size, so a more repetitive sequence
// scores lower.
func tagCompression(sequence []postag.UPOS) float64 {
	if len(sequence) == 0 {
		return 0
	}

	names := make([]string, len(sequence))
	for i, u := range sequence {
		names[i] = u.String()
	}

	raw := []byte(strings.Join(names, " "))

	var buf bytes.Buffer

	// Neither fails: the level is valid and a bytes.Buffer takes any write.
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	_, _ = zw.Write(raw)
	_ = zw.Close()

	return float64(buf.Len()) / float64(len(raw))
}

func isUPOS(t postag.Tagged, tags ...postag.UPOS) bool { return slices.Contains(tags, t.UPOS) }

// finite reports whether t is a finite verb, which ends a sentence-initial
// participial clause without the comma that closes one.
func finite(t postag.Tagged) bool {
	switch t.XPOS {
	case "VBZ", "VBP", "VBD", "MD":
		return true
	}

	return false
}

// participialClause matches the participle that opens a non-finite clause at
// the start of a sentence or after a comma: "Running the tests, she found a
// bug" and "it failed, leaving the cache stale". A sentence-initial one must
// close at a comma before any finite verb, which tells it from a gerund
// subject such as "Running the tests takes time".
func participialClause(s tagged, i int) bool {
	t := s.tags[i]
	if (t.XPOS != "VBG" && t.XPOS != "VBN") || !isUPOS(t, postag.VERB, postag.AUX) {
		return false
	}

	start := 0
	for start < i && s.tags[start].UPOS == postag.PUNCT {
		start++
	}

	if i == start {
		for _, next := range s.tags[i+1:] {
			if next.XPOS == "," {
				return true
			}

			if finite(next) {
				return false
			}
		}

		return false
	}

	return s.words[i-1] == "," && i+1 < len(s.words) && !isUPOS(s.tags[i+1], postag.PUNCT, postag.CCONJ)
}

// nominalizationSuffixes are Biber's -tion, -ment, -ness and -ity, with the
// -sion, -ance and -ence that also derive a noun from a verb or an adjective.
var nominalizationSuffixes = []string{"tion", "sion", "ment", "ness", "ity", "ance", "ence"}

// notNominalizations end in a nominalization suffix but derive from no
// English verb or adjective the suffix attaches to, and are frequent in
// technical prose, where they would raise every author's rate alike.
var notNominalizations = wordSet(
	"business", "comment", "document", "element", "entity", "fragment", "function", "mention", "question",
	"science", "section", "segment", "sentence", "session", "station", "version",
)

// minNominalizationStem keeps out the words whose "suffix" is most of the
// word: "nation", "city", "moment", "chance".
const minNominalizationStem = 3

func nominalization(s tagged, i int) bool {
	if s.tags[i].UPOS != postag.NOUN {
		return false
	}

	w := singular(s.words[i])
	if notNominalizations[w] {
		return false
	}

	for _, suffix := range nominalizationSuffixes {
		if strings.HasSuffix(w, suffix) && len(w)-len(suffix) >= minNominalizationStem {
			return true
		}
	}

	return false
}

func singular(w string) string {
	switch {
	case strings.HasSuffix(w, "ies"):
		return strings.TrimSuffix(w, "ies") + "y"
	case strings.HasSuffix(w, "nesses"):
		return strings.TrimSuffix(w, "es")
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		return strings.TrimSuffix(w, "s")
	}

	return w
}

var (
	beForms  = wordSet("am", "is", "are", "was", "were", "be", "been", "being", "'s", "'re", "'m")
	getForms = wordSet("get", "gets", "got", "gotten", "getting")
)

// agentlessPassive matches a form of be or get followed by a past
// participle, an adverb or "not" allowed between, with no "by" before the
// clause ends: "the cache was rebuilt", not "the cache was rebuilt by CI".
func agentlessPassive(s tagged, i int) bool {
	if !(beForms[s.words[i]] && s.tags[i].UPOS == postag.AUX) && !getForms[s.words[i]] {
		return false
	}

	j := i + 1
	for j < len(s.words) && j-i <= 2 && isUPOS(s.tags[j], postag.ADV, postag.PART) {
		j++
	}

	if j >= len(s.words) || s.tags[j].XPOS != "VBN" || s.tags[j].UPOS != postag.VERB {
		return false
	}

	for k := j + 1; k < len(s.words) && !isUPOS(s.tags[k], postag.PUNCT, postag.SCONJ); k++ {
		if s.words[k] == "by" {
			return false
		}
	}

	return true
}

// phrasalCoordination matches "and" between two nouns or two adjectives, as
// Biber counts it, so the rate compares with Reinhart et al.'s.
func phrasalCoordination(s tagged, i int) bool {
	if s.tags[i].UPOS != postag.CCONJ || !strings.EqualFold(s.words[i], "and") || i == 0 || i+1 >= len(s.words) {
		return false
	}

	before, after := s.tags[i-1], s.tags[i+1]
	nouns := isUPOS(before, postag.NOUN, postag.PROPN) && isUPOS(after, postag.NOUN, postag.PROPN)

	return nouns || (before.UPOS == postag.ADJ && after.UPOS == postag.ADJ)
}

// downtoners is Biber's list (1988, Appendix II, feature 46).
var downtoners = wordSet(
	"almost", "barely", "hardly", "merely", "mildly", "nearly", "only", "partially", "partly", "practically",
	"scarcely", "slightly", "somewhat",
)

// downtoner matches a word of the list read as an adverb, so "the only way"
// is not one.
func downtoner(s tagged, i int) bool { return downtoners[s.words[i]] && s.tags[i].UPOS == postag.ADV }

// concessive matches "though" and "although" that open a clause; a
// sentence-final "though" is an adverb.
func concessive(s tagged, i int) bool {
	return (s.words[i] == "though" || s.words[i] == "although") && s.tags[i].UPOS == postag.SCONJ
}

// participlePostmodifier matches a noun followed at once by a present
// participle: "a job running every night". A progressive puts a form of be
// between them, so "the job's running" is not one once [postag.Tokenize]
// splits the "'s".
func participlePostmodifier(s tagged, i int) bool {
	return s.tags[i].UPOS == postag.NOUN && i+1 < len(s.words) && s.tags[i+1].XPOS == "VBG" &&
		s.tags[i+1].UPOS == postag.VERB
}
