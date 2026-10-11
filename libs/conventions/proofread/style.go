package proofread

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RuleVoiceDrift reports a text whose style measures outside its author's own
// range on two or more features of a [Voice].
const RuleVoiceDrift Rule = "voice-drift"

// VoiceSchema names the shape of a voice file, so a file written by another
// version of proofread is refused rather than misread.
const VoiceSchema = "proofread-voice/1"

// MinVoiceTexts is the fewest texts of one kind a [Voice] sets ranges from.
// Under it the kind keeps its counts, and voice-drift stays silent on it.
const MinVoiceTexts = 30

// minFeatureTexts is the fewest texts that must measure a feature for its range
// to be kept: a feature with a guard, such as one that needs 30 words, may be
// measured on few of a kind's texts.
const minFeatureTexts = 10

// Feature names one measurement of a text's style. Every feature is a number
// computed from the text alone: no feature records a word the text chose,
// its spelling or its casing.
type Feature string

const (
	// FeatureWords counts the prose words.
	FeatureWords Feature = "words"
	// FeatureSentenceWords is the mean words per sentence, over texts of three
	// sentences or more.
	FeatureSentenceWords Feature = "sentence_words"
	// FeatureParagraphWords counts the words of the longest paragraph or list
	// item, over texts of two paragraphs or more: in one paragraph it is
	// [FeatureWords] again.
	FeatureParagraphWords Feature = "paragraph_words"
	// FeaturePunctuation is colons, opening parentheses and dashes per 100
	// words, over texts of 30 words or more. Semicolons are left out: a commit
	// subject joins its clauses with one by house rule.
	FeaturePunctuation Feature = "punctuation"
	// FeatureCodeSpans is backticked spans per 100 words, over texts of 30
	// words or more.
	FeatureCodeSpans Feature = "code_spans"
	// FeatureContractions is contractions per 100 words, over texts of 60
	// words or more.
	FeatureContractions Feature = "contractions"
	// FeatureFormatting counts headings and bold spans.
	FeatureFormatting Feature = "formatting"
	// FeatureMarkerWords is its, every, one, with, all, and, the per 100
	// words, over texts of 30 words or more: the function words agent text
	// leans on.
	FeatureMarkerWords Feature = "marker_words"
	// FeatureSubjectChars counts a commit subject's characters.
	FeatureSubjectChars Feature = "subject_chars"
)

// features is every feature in the order a finding names them.
var features = []Feature{
	FeatureWords, FeatureSubjectChars, FeatureSentenceWords, FeatureParagraphWords, FeaturePunctuation,
	FeatureCodeSpans, FeatureContractions, FeatureFormatting, FeatureMarkerWords,
}

var featureLabels = map[Feature]string{
	FeatureWords:          "words",
	FeatureSubjectChars:   "subject characters",
	FeatureSentenceWords:  "words per sentence",
	FeatureParagraphWords: "words in the longest paragraph",
	FeaturePunctuation:    "colons, parentheses and dashes per 100 words",
	FeatureCodeSpans:      "code spans per 100 words",
	FeatureContractions:   "contractions per 100 words",
	FeatureFormatting:     "headings and bold",
	FeatureMarkerWords:    "its/every/one/with/all/and/the per 100 words",
}

// labelAuthor marks a case file section of an author's own texts. It holds no
// case: its texts build the [Voice] every case in its file is judged with.
const labelAuthor Label = "author"

// voiceKinds are the kinds a person writes in their own voice.
var voiceKinds = []Kind{KindChangeDescription, KindReviewReply, KindCommitMessage, KindIssue}

var voicePlurals = map[Kind]string{
	KindChangeDescription: "change descriptions",
	KindReviewReply:       "review replies",
	KindCommitMessage:     "commit messages",
	KindIssue:             "issues",
}

// Voice is one author's measured style per kind of text. It holds aggregates
// alone: counts, rates and percentiles, never a text, a word the author chose
// or a path.
type Voice struct {
	Schema string             `json:"schema"`
	Kinds  map[Kind]KindVoice `json:"kinds"`
}

// KindVoice is a [Voice] over the texts of one kind.
type KindVoice struct {
	Texts int `json:"texts"`
	Words int `json:"words"`
	// FirstPerson and Future are the rates per 1000 words, pooled over the
	// texts, of the first-person singular and the future tense the tense rule
	// reports.
	FirstPerson float64 `json:"first_person_per_1000"`
	Future      float64 `json:"future_per_1000"`
	// Ranges holds each feature measured on enough texts, and none when the
	// kind has under [MinVoiceTexts] texts.
	Ranges map[Feature]Range `json:"ranges,omitempty"`
}

// Range is a feature's spread over the texts that measured it.
type Range struct {
	Texts int     `json:"texts"`
	P10   float64 `json:"p10"`
	P50   float64 `json:"p50"`
	P90   float64 `json:"p90"`
}

// The rates per 1000 words at which a voice relaxes the tense rule: an author
// who writes "I" and "will" this often in descriptions is held to tense as
// advice. The study behind voice-drift measured the first person at 20 to 21
// per 1000 words in one author's hand-typed descriptions against 0.2 and 1.9
// in two sets of agent descriptions, and the floor sits halfway. It measured
// no future-tense rate, so that floor is a guess at "uses will at all".
const (
	tenseFirstPersonFloor = 10.0
	tenseFutureFloor      = 2.0
)

// BuildVoice measures texts, keyed by kind, into a [Voice]. A kind outside
// change-description, review-reply, commit-message and issue is an error, and
// so are texts with no prose word among them. A text with no prose word is not
// counted.
func BuildVoice(texts map[Kind][]string) (*Voice, error) {
	v := &Voice{Schema: VoiceSchema, Kinds: map[Kind]KindVoice{}}

	for kind, list := range texts {
		if !slices.Contains(voiceKinds, kind) {
			return nil, fmt.Errorf("a voice measures %s, not %s", kindList(voiceKinds), kind)
		}

		k, measured := KindVoice{}, map[Feature][]float64{}

		var firstPerson, future int

		for _, text := range list {
			s := measureStyle(text, kind)
			if s.words == 0 {
				continue
			}

			k.Texts++
			k.Words += s.words
			firstPerson += s.firstPerson
			future += s.future

			for f, value := range s.features {
				measured[f] = append(measured[f], value)
			}
		}

		if k.Texts == 0 {
			continue
		}

		k.FirstPerson = round2(1000 * float64(firstPerson) / float64(k.Words))
		k.Future = round2(1000 * float64(future) / float64(k.Words))

		if k.Texts >= MinVoiceTexts {
			k.Ranges = ranges(measured)
		}

		v.Kinds[kind] = k
	}

	if len(v.Kinds) == 0 {
		return nil, errors.New("no text has a prose word to measure")
	}

	return v, nil
}

func ranges(measured map[Feature][]float64) map[Feature]Range {
	out := map[Feature]Range{}

	for f, values := range measured {
		if len(values) < minFeatureTexts {
			continue
		}

		slices.Sort(values)
		out[f] = Range{
			Texts: len(values),
			P10:   round2(percentile(values, 0.1)),
			P50:   round2(percentile(values, 0.5)),
			P90:   round2(percentile(values, 0.9)),
		}
	}

	return out
}

// percentile interpolates between the closest ranks of sorted, which holds at
// least one value.
func percentile(sorted []float64, p float64) float64 {
	rank := p * float64(len(sorted)-1)
	lo := int(math.Floor(rank))

	if lo+1 >= len(sorted) {
		return sorted[lo]
	}

	return sorted[lo] + (rank-float64(lo))*(sorted[lo+1]-sorted[lo])
}

// ReadVoice decodes a voice file. A schema other than [VoiceSchema], a field,
// kind or feature proofread does not know, or a range whose p10 exceeds its
// p90 is an error.
func ReadVoice(r io.Reader) (*Voice, error) {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()

	var v Voice
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}

	if v.Schema != VoiceSchema {
		return nil, fmt.Errorf("schema %q is not %s", v.Schema, VoiceSchema)
	}

	for kind, k := range v.Kinds {
		if !slices.Contains(voiceKinds, kind) {
			return nil, fmt.Errorf("a voice measures %s, not %s", kindList(voiceKinds), kind)
		}

		for f, r := range k.Ranges {
			if !slices.Contains(features, f) {
				return nil, fmt.Errorf("%s: unknown feature %q", kind, f)
			}

			if r.P10 > r.P90 {
				return nil, fmt.Errorf("%s: %s has p10 %v over p90 %v", kind, f, r.P10, r.P90)
			}
		}
	}

	return &v, nil
}

// RelaxesTense reports whether v holds the tense rule to advice on change
// descriptions: its change descriptions number at least [MinVoiceTexts] and
// use the first person and the future tense at the rates an author who writes
// that way does.
func (v *Voice) RelaxesTense() bool {
	if v == nil {
		return false
	}

	k, ok := v.Kinds[KindChangeDescription]

	return ok && k.Texts >= MinVoiceTexts && k.FirstPerson >= tenseFirstPersonFloor && k.Future >= tenseFutureFloor
}

// WithVoice judges with v: the voice-drift rule runs, which is off with no
// voice, and a deny of the tense rule on a change description becomes advice
// when v [Voice.RelaxesTense]. A nil v gives no voice.
func WithVoice(v *Voice) Option { return func(o *options) { o.voice = v } }

// relaxes reports whether v turns rule's deny into advice on kind.
func (v *Voice) relaxes(rule Rule, kind Kind) bool {
	return rule == RuleTense && kind == KindChangeDescription && v.RelaxesTense()
}

var voiceChecks = []check{
	{rule: RuleVoiceDrift, on: voiceKinds, advise: voiceKinds, judge: voiceDrift},
}

var voiceTexts = map[Rule]ruleText{
	RuleVoiceDrift: {
		code:      "PRF1040",
		dimension: DimensionStance,
		catches:   "a text whose style measures outside its author's own range on two or more features of a voice file",
		why: "Runs only with a voice file, which proofread voice build measures from the author's own texts. " +
			"A feature drifts when the text measures under its p10 or over its p90 for the kind, so about one " +
			"text in five drifts on any one feature by construction, and a finding needs two. A study of one " +
			"author, 2026-10-10, set p90 thresholds on 254 hand-typed descriptions, 286 replies and 1,055 " +
			"commits: two or more features over range fired on 9 percent of that author's descriptions against " +
			"66 percent of agent descriptions, 7 against 46 percent of replies, and 5 against 60 percent of " +
			"commits. Those rates are in-sample and count p90 alone, and no firing has been labeled, so the " +
			"rule advises.",
	},
}

// voiceDrift reports, once per text, every feature that measures outside the
// author's range, when two or more do.
func voiceDrift(in input) []Finding {
	if in.opts.voice == nil {
		return nil
	}

	k, ok := in.opts.voice.Kinds[in.kind]
	if !ok || len(k.Ranges) == 0 {
		return nil
	}

	s := measureStyle(strings.Join(in.source, "\n"), in.kind)

	var drifts []string

	for _, f := range features {
		r, ranged := k.Ranges[f]
		value, measured := s.features[f]

		switch {
		case !ranged || !measured:
		case value > r.P90:
			drifts = append(drifts, fmt.Sprintf("%s %s above your %s-%s", featureLabels[f], number(value), number(r.P10), number(r.P90)))
		case value < r.P10:
			drifts = append(drifts, fmt.Sprintf("%s %s below your %s-%s", featureLabels[f], number(value), number(r.P10), number(r.P90)))
		}
	}

	if len(drifts) < 2 {
		return nil
	}

	return []Finding{{
		Message: fmt.Sprintf("Reads unlike your own %s: %s. Read it against how you write.",
			voicePlurals[in.kind], strings.Join(drifts, ", ")),
	}}
}

func number(f float64) string { return fmt.Sprint(round2(f)) }

// styleSample is what one text measures: its words, its counts of the first
// person and the future tense, and each feature whose guard it meets.
type styleSample struct {
	words, firstPerson, future int
	features                   map[Feature]float64
}

var (
	boldSpan   = regexp.MustCompile(`\*\*[^*\s][^*]*\*\*|__[^_\s][^_]*__`)
	spacedDash = regexp.MustCompile(`\s--?\s|\x{2014}|\x{2013}`)
	// contraction counts n't, 're, 've, 'll, 'm and 'd, and 's only after a
	// pronoun, where it is no possessive.
	contraction = regexp.MustCompile(`(?i)\b(?:\w+n['\x{2019}]t|\w+['\x{2019}](?:re|ve|ll|m|d)|` +
		`(?:it|that|there|here|what|who|he|she|let)['\x{2019}]s)\b`)
	markerWords = wordSet("its", "every", "one", "with", "all", "and", "the")
)

// measureStyle measures text read as kind, from the prose [Measure] reads and
// split into sentences as it splits them.
func measureStyle(text string, kind Kind) styleSample {
	m := Measure(text, kind)
	s := styleSample{words: m.Words, features: map[Feature]float64{}}

	if m.Words == 0 {
		return s
	}

	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	lines := markdownProse(raw, kind != KindReviewReply && !titled(kind))
	wrapCodeSpans(lines)

	var paragraphCount, longest, punctuation, spans, contractions, formatting, markers int

	for _, para := range paragraphs(readProse(lines, kind != KindCommitMessage), keep) {
		formatting += len(boldSpan.FindAllString(para.text, -1))

		if para.head.heading {
			formatting++

			continue
		}

		spans += len(anySpan.FindAllString(para.text, -1))
		prose := anySpan.ReplaceAllString(para.text, " code ")

		n := 0

		for _, token := range strings.Fields(prose) {
			if !strings.ContainsFunc(token, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) {
				continue
			}

			n++

			if markerWords[strings.ToLower(strings.TrimFunc(token, func(r rune) bool { return !unicode.IsLetter(r) }))] {
				markers++
			}
		}

		if n > 0 {
			paragraphCount++
		}

		longest = max(longest, n)
		punctuation += strings.Count(prose, ":") + strings.Count(prose, "(") + len(spacedDash.FindAllString(prose, -1))
		contractions += len(contraction.FindAllString(prose, -1))
		s.firstPerson += len(firstPerson.FindAllString(prose, -1))
		s.future += len(future.FindAllString(prose, -1))
	}

	per100 := func(n int) float64 { return round2(100 * float64(n) / float64(m.Words)) }

	s.features[FeatureWords] = float64(m.Words)
	s.features[FeatureFormatting] = float64(formatting)

	if paragraphCount >= 2 {
		s.features[FeatureParagraphWords] = float64(longest)
	}

	if kind == KindCommitMessage {
		s.features[FeatureSubjectChars] = float64(utf8.RuneCountInString(strings.TrimSpace(raw[0])))
	}

	if m.Sentences >= 3 {
		s.features[FeatureSentenceWords] = m.SentenceWordsMean
	}

	if m.Words >= 30 {
		s.features[FeaturePunctuation] = per100(punctuation)
		s.features[FeatureCodeSpans] = per100(spans)
		s.features[FeatureMarkerWords] = per100(markers)
	}

	if m.Words >= 60 {
		s.features[FeatureContractions] = per100(contractions)
	}

	return s
}

func kindList(kinds []Kind) string {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
