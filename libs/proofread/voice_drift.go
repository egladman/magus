package proofread

import (
	"cmp"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RuleVoiceDrift reports a draft that reads further from its author's own
// texts of its kind than 95% of those texts do, and a commit subject longer
// than 95% of the author's.
const RuleVoiceDrift Rule = "voice-drift"

const (
	// voiceWindowWords is the fewest words in a window of the author's texts,
	// and in a draft voice-drift scores on its own.
	voiceWindowWords = 200
	// voiceDraftFloor is the fewest words voice-drift ever judges.
	voiceDraftFloor = 15
	// voiceFalseAlarmPercent is the share of the author's own windows the cut
	// lets exceed it.
	voiceFalseAlarmPercent = 5
	// minVoiceFeatures is the fewest features with data a score combines.
	minVoiceFeatures = 2
	// voiceSeed shuffles the texts before pooling, so one set of texts always
	// builds the same voice.
	voiceSeed uint64 = 13
)

// voiceFeature is one feature voice-drift ranks a draft on.
type voiceFeature struct {
	name  Feature
	label string
	unit  string
	// digits is the decimal places a finding prints.
	digits int
	// higher is the direction agent drafts move: a draft is ranked only
	// against owner windows at least as far that way.
	higher bool
	// off are the kinds the feature is not ranked on.
	off  []Kind
	read func(Shape) (float64, bool)
}

const (
	per100Words  = " per 100 words"
	per1000Words = " per 1000 words"
)

// voiceFeatures are the features voice-drift ranks on, each only in the
// direction agent drafts moved from one author's own text in voice-calibration
// (2026-10-13). Combined, they flagged 96-100% of agent drafts at 0-6% owner
// false alarms; template_rate and tag_compression are off for review text and
// colons for issues because their direction flipped between the author's eras.
var voiceFeatures = []voiceFeature{
	{name: "punctuation.comma", label: "commas", unit: per100Words, digits: 1, higher: true, read: punctuationRate("comma")},
	{name: "punctuation.paren", label: "parentheses", unit: per100Words, digits: 1, higher: true, read: punctuationRate("paren")},
	{
		name: "punctuation.colon", label: "colons", unit: per100Words, digits: 1, higher: true,
		off: []Kind{KindIssue}, read: punctuationRate("colon"),
	},
	{
		name: "punctuation.semicolon", label: "semicolons", unit: per100Words, digits: 1, higher: true,
		read: punctuationRate("semicolon"),
	},
	{name: "function_words.i", label: `"I"`, unit: per1000Words, digits: 1, read: functionWordRate("i")},
	{name: "function_words.and", label: `"and"`, unit: per1000Words, digits: 1, higher: true, read: functionWordRate("and")},
	{
		name: "template_rate", label: "repeated part-of-speech 4-grams", unit: " of all", digits: 2, higher: true,
		off: reviewText, read: unlessWithheld("template_rate", func(s Shape) float64 { return s.TemplateRate }),
	},
	{
		name: "tag_compression", label: "part-of-speech compression", unit: " of raw size", digits: 2,
		off: reviewText, read: unlessWithheld("tag_compression", func(s Shape) float64 { return s.TagCompression }),
	},
	{
		name: "grammar_per_1000.participial_clause", label: "participial clauses", unit: per1000Words, digits: 1,
		higher: true, read: grammarRate("participial_clause"),
	},
}

func punctuationRate(mark string) func(Shape) (float64, bool) {
	return func(s Shape) (float64, bool) {
		v, ok := s.Punctuation[mark]

		return v, ok
	}
}

func functionWordRate(word string) func(Shape) (float64, bool) {
	return func(s Shape) (float64, bool) {
		v, ok := s.FunctionWords[word]

		return v, ok
	}
}

func grammarRate(name string) func(Shape) (float64, bool) {
	return func(s Shape) (float64, bool) {
		v, ok := s.Grammar[name]

		return v, ok
	}
}

func unlessWithheld(name string, value func(Shape) float64) func(Shape) (float64, bool) {
	return func(s Shape) (float64, bool) { return value(s), !slices.Contains(s.Withheld, name) }
}

// rankedFeatures is the features voice-drift ranks a text of kind on.
func rankedFeatures(kind Kind) []voiceFeature {
	return slices.DeleteFunc(slices.Clone(voiceFeatures), func(f voiceFeature) bool { return slices.Contains(f.off, kind) })
}

func readFeatures(s Shape, features []voiceFeature) map[Feature]float64 {
	out := map[Feature]float64{}

	for _, f := range features {
		if v, ok := f.read(s); ok {
			out[f.name] = v
		}
	}

	return out
}

// MeasureWindows shuffles texts under seed, pools them into windows of at
// least minWords words read as kind, and returns each window's [Shape]. Each
// text lands in at most one window, and a last window under minWords is
// dropped.
func MeasureWindows(texts []string, kind Kind, minWords int, seed uint64) []Shape {
	windows := poolWindows(kind, texts, minWords, seed)
	out := make([]Shape, len(windows))

	for i, w := range windows {
		out[i] = MeasureShape(w, kind)
	}

	return out
}

// poolWindows shuffles texts under seed, then joins them in that order, a
// blank line apart, into windows of at least minWords words. The shuffle is
// Eder's random sampling: windows cut from the texts in order would each
// stand for one stretch of writing.
func poolWindows(kind Kind, texts []string, minWords int, seed uint64) []string {
	order := slices.Clone(texts)
	rng := rand.New(rand.NewPCG(seed, 0))
	rng.Shuffle(len(order), func(i, j int) { order[i], order[j] = order[j], order[i] })

	var (
		windows []string
		window  strings.Builder
		words   int
	)

	for _, text := range order {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}

		// A later text's title stays as a paragraph of its own: a paragraph
		// ends its sentence, and most commits are a subject alone.
		if window.Len() > 0 {
			window.WriteString("\n\n")
		}

		window.WriteString(text)

		words += Measure(text, kind).Words
		if words >= minWords {
			windows = append(windows, window.String())
			window.Reset()

			words = 0
		}
	}

	return windows
}

// buildWindows pools texts into windows and sets the cut from them, or
// returns nil when no window, scored against the others, has two features
// with data.
func buildWindows(kind Kind, texts []string) *Windows {
	features := rankedFeatures(kind)

	var measured []map[Feature]float64
	for _, s := range MeasureWindows(texts, kind, voiceWindowWords, voiceSeed) {
		measured = append(measured, readFeatures(s, features))
	}

	return measuredWindows(measured, features)
}

func measuredWindows(measured []map[Feature]float64, features []voiceFeature) *Windows {
	stats := ownerScores(measured, features)
	if len(stats) == 0 {
		return nil
	}

	return &Windows{Count: len(measured), Values: sortedValues(measured), Cut: cutAt(stats)}
}

// ownerScores scores each window against the others, leaving it out of its
// own reference, and keeps the scores of windows with two features with data.
func ownerScores(measured []map[Feature]float64, features []voiceFeature) []float64 {
	var stats []float64

	for i, w := range measured {
		others := sortedValues(slices.Delete(slices.Clone(measured), i, i+1))
		if stat, parts := fisher(w, others, features); len(parts) >= minVoiceFeatures {
			stats = append(stats, stat)
		}
	}

	return stats
}

func sortedValues(measured []map[Feature]float64) map[Feature][]float64 {
	out := map[Feature][]float64{}

	for _, w := range measured {
		for f, v := range w {
			out[f] = append(out[f], v)
		}
	}

	for _, values := range out {
		slices.Sort(values)
	}

	return out
}

// cutAt is the least score that no more than voiceFalseAlarmPercent of stats
// exceed. Under 20 scores that is the highest.
func cutAt(stats []float64) float64 {
	sorted := slices.Sorted(slices.Values(stats))
	allowed := len(sorted) * voiceFalseAlarmPercent / 100

	return sorted[len(sorted)-1-allowed]
}

// voicePart is one feature's contribution to a score.
type voicePart struct {
	feature voiceFeature
	value   float64
	p       float64
}

// fisher combines the one-sided rank p-value of each feature measured with a
// reference, -2 times the sum of their logs, and returns the parts it summed.
func fisher(measured map[Feature]float64, reference map[Feature][]float64, features []voiceFeature) (float64, []voicePart) {
	var (
		stat  float64
		parts []voicePart
	)

	for _, f := range features {
		value, ok := measured[f.name]
		ref := reference[f.name]

		if !ok || len(ref) == 0 {
			continue
		}

		p := rankP(ref, value, f.higher)
		stat += -2 * math.Log(p)
		parts = append(parts, voicePart{feature: f, value: value, p: p})
	}

	return stat, parts
}

// rankP is (1 + the values of sorted at least as far as value in the
// direction higher names) / (len(sorted) + 1).
func rankP(sorted []float64, value float64, higher bool) float64 {
	var extreme int
	if higher {
		extreme = len(sorted) - sort.SearchFloat64s(sorted, value)
	} else {
		extreme = sort.Search(len(sorted), func(i int) bool { return sorted[i] > value })
	}

	return float64(1+extreme) / float64(len(sorted)+1)
}

var voiceChecks = []check{
	{rule: RuleVoiceDrift, on: voiceJudged, advise: voiceJudged, judge: voiceDrift},
}

var voiceTexts = map[Rule]ruleText{
	RuleVoiceDrift: {
		code:      "PRF1040",
		dimension: DimensionStance,
		catches: "a draft that reads further from its author's own texts of its kind than 95 percent of them do, " +
			"or a commit subject longer than 95 percent of the author's",
		why: "Runs only with a voice file, which proofread voice build measures from the author's own texts, " +
			"1,000 words or more of a kind, shuffled and pooled into windows of 200 words or more. A draft of 200 " +
			"words or more is ranked against those windows on commas, parentheses, colons, semicolons, \"I\", " +
			"\"and\", repeated part-of-speech 4-grams, part-of-speech compression and participial clauses, each " +
			"only in the direction agent drafts move, and the ranks combine by Fisher's method. The cut lets 5 " +
			"percent of the author's own windows, each scored against the rest, past it. A commit message is held " +
			"to its subject's length instead. A study of one author, 2026-10-13, flagged 96 to 100 percent of " +
			"agent drafts at 0 to 6 percent false alarms on the author's held-out 2018-24 text, and the subject " +
			"rule 84.8 percent of agent commits at 6.0 percent. It caught 11 to 32 percent of light agent edits " +
			"of the author's own text, so the rule detects agent drafts, not edits. No firing has been labeled, " +
			"so the rule advises.",
	},
}

// voiceDrift reports a text that drifts from its kind's reference in the
// voice. It abstains on a kind with no reference, under voiceDraftFloor
// words, under voiceWindowWords words, and when fewer than minVoiceFeatures
// features have data. A commit message is judged on its subject alone.
func voiceDrift(in input) []Finding {
	if in.opts.voice == nil {
		return nil
	}

	kind := heldKind(in.kind)

	k, ok := in.opts.voice.Kinds[kind]
	if !ok {
		return nil
	}

	text := strings.Join(in.source, "\n")

	if kind == KindCommitMessage {
		return subjectDrift(text, k.SubjectP95)
	}

	if k.Windows == nil {
		return nil
	}

	s := MeasureShape(text, kind)

	switch {
	case s.Words < voiceDraftFloor:
		return nil
	case s.Words < voiceWindowWords:
		// TODO: pool a draft under 200 words with the drafts of its kind on its
		// branch or in its thread and score the pool; gathering those drafts is
		// the guard's job.
		return nil
	}

	features := rankedFeatures(kind)

	stat, parts := fisher(readFeatures(s, features), k.Windows.Values, features)
	if len(parts) < minVoiceFeatures || stat <= k.Windows.Cut {
		return nil
	}

	return []Finding{{Message: driftMessage(in.kind, s.Words, parts, k.Windows.Values)}}
}

// subjectDrift reports a commit subject longer than p95 characters. A p95 of
// 0 is no reference.
func subjectDrift(text string, p95 float64) []Finding {
	chars := subjectChars(text)
	if p95 == 0 || float64(chars) <= p95 {
		return nil
	}

	return []Finding{{Message: fmt.Sprintf("The subject runs %d characters, longer than 95%% of your own commit "+
		"subjects (yours: %s or fewer). Read it against how you write.", chars, formatValue(p95, 0))}}
}

func subjectChars(text string) int {
	subject, _, _ := strings.Cut(strings.TrimSpace(text), "\n")

	return utf8.RuneCountInString(strings.TrimSpace(subject))
}

func subjectP95(texts []string) float64 {
	chars := make([]float64, len(texts))
	for i, text := range texts {
		chars[i] = float64(subjectChars(text))
	}

	slices.Sort(chars)

	return round2(percentile(chars, 0.95))
}

var (
	voiceDraftNames = map[Kind]string{
		KindChangeDescription: "description",
		KindReviewSummary:     "review",
		KindReviewInline:      "comment",
		KindReviewReply:       "reply",
		KindIssue:             "issue",
	}
	voicePlurals = map[Kind]string{
		KindChangeDescription: "change descriptions",
		KindReviewReply:       "review comments",
		KindIssue:             "issues",
	}
)

// driftMessage names the three parts with the smallest p, each with its
// value, the author's 10th to 90th percentile and the direction it drifts.
func driftMessage(kind Kind, words int, parts []voicePart, reference map[Feature][]float64) string {
	parts = slices.Clone(parts)
	slices.SortStableFunc(parts, func(a, b voicePart) int { return cmp.Compare(a.p, b.p) })

	named := make([]string, 0, 3)

	for _, part := range parts[:min(3, len(parts))] {
		f := part.feature
		ref := reference[f.name]
		direction := "lower"

		if f.higher {
			direction = "higher"
		}

		named = append(named, fmt.Sprintf("%s %s%s (yours: %s), %s", f.label, formatValue(part.value, f.digits), f.unit,
			ownerRange(ref, f.digits), direction))
	}

	return fmt.Sprintf("This %s reads further from your own %s than 95%% of your writing does (%d words): %s. "+
		"Read it against how you write.", voiceDraftNames[kind], voicePlurals[heldKind(kind)], words, strings.Join(named, "; "))
}

// ownerRange is the 10th to 90th percentile of sorted, "none" when both are
// 0, and one number when they print alike.
func ownerRange(sorted []float64, digits int) string {
	lo, hi := formatValue(percentile(sorted, 0.1), digits), formatValue(percentile(sorted, 0.9), digits)

	switch {
	case lo == "0" && hi == "0":
		return "none"
	case lo == hi:
		return lo
	}

	return lo + " to " + hi
}

func formatValue(v float64, digits int) string {
	scale := math.Pow(10, float64(digits))

	return strconv.FormatFloat(math.Round(v*scale)/scale, 'f', -1, 64)
}
