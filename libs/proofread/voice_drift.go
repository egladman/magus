package proofread

import (
	"cmp"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// RuleVoiceDrift reports a draft that reads further from its author's own
// texts of its kind than 95% of those texts do, and a commit subject longer
// than 95% of the author's.
const RuleVoiceDrift Rule = "voice-drift"

// VoiceSeed is the seed [BuildVoice] shuffles texts under before pooling them
// into windows, so one set of texts always builds the same voice.
const VoiceSeed uint64 = 13

const (
	// voiceWindowWords is the fewest words in a window of the author's texts,
	// and in a draft voice-drift scores on its own.
	voiceWindowWords = 200
	// voiceFalseAlarmPercent is the share of the author's own windows the cut
	// lets exceed it.
	voiceFalseAlarmPercent = 5
	// voiceKeepPercent is the share of the author's windows, and of the
	// author's commit subjects, a draft must read further than to drift.
	voiceKeepPercent = 100 - voiceFalseAlarmPercent
	// minVoiceFeatures is the fewest features with data a score combines.
	minVoiceFeatures = 2
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
	read func(s Shape, name Feature) (float64, bool)
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
	{name: "punctuation.comma", label: "commas", unit: per100Words, digits: 1, higher: true, read: mapEntry},
	{name: "punctuation.paren", label: "parentheses", unit: per100Words, digits: 1, higher: true, read: mapEntry},
	{
		name: "punctuation.colon", label: "colons", unit: per100Words, digits: 1, higher: true,
		off: []Kind{KindIssue}, read: mapEntry,
	},
	{name: "punctuation.semicolon", label: "semicolons", unit: per100Words, digits: 1, higher: true, read: mapEntry},
	{name: "function_words.i", label: `"I"`, unit: per1000Words, digits: 1, read: mapEntry},
	{name: "function_words.and", label: `"and"`, unit: per1000Words, digits: 1, higher: true, read: mapEntry},
	{
		name: "template_rate", label: "repeated part-of-speech 4-grams", unit: " of all", digits: 2, higher: true,
		off: reviewText, read: unlessWithheld(func(s Shape) float64 { return s.TemplateRate }),
	},
	{
		name: "tag_compression", label: "part-of-speech compression", unit: " of raw size", digits: 2,
		off: reviewText, read: unlessWithheld(func(s Shape) float64 { return s.TagCompression }),
	},
	{
		name: "grammar_per_1000.participial_clause", label: "participial clauses", unit: per1000Words, digits: 1,
		higher: true, read: mapEntry,
	},
}

// mapEntry reads the feature "<map>.<key>" from its map in s.
func mapEntry(s Shape, name Feature) (float64, bool) {
	field, key, _ := strings.Cut(string(name), ".")

	var m map[string]float64

	switch field {
	case "punctuation":
		m = s.Punctuation
	case "function_words":
		m = s.FunctionWords
	case "grammar_per_1000":
		m = s.Grammar
	}

	v, ok := m[key]

	return v, ok
}

func unlessWithheld(value func(Shape) float64) func(Shape, Feature) (float64, bool) {
	return func(s Shape, name Feature) (float64, bool) {
		return value(s), !slices.Contains(s.Withheld, string(name))
	}
}

// rankedFeatures is the features voice-drift ranks a text of kind on.
func rankedFeatures(kind Kind) []voiceFeature {
	return slices.DeleteFunc(slices.Clone(voiceFeatures), func(f voiceFeature) bool { return slices.Contains(f.off, kind) })
}

func readFeatures(s Shape, features []voiceFeature) map[Feature]float64 {
	out := map[Feature]float64{}

	for _, f := range features {
		if v, ok := f.read(s, f.name); ok {
			out[f.name] = v
		}
	}

	return out
}

// MeasureWindowShapes shuffles texts under seed, pools them into windows of
// at least minWords words read as kind, and returns each window's [Shape].
// Each text lands in at most one window, and a last window under minWords is
// dropped.
func MeasureWindowShapes(texts []string, kind Kind, minWords int, seed uint64) []Shape {
	windows := poolWindows(texts, kind, minWords, seed)
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
func poolWindows(texts []string, kind Kind, minWords int, seed uint64) []string {
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

// buildWindows pools texts into windows and keeps each window's features
// with data, or returns nil when no window has minVoiceFeatures of them.
func buildWindows(kind Kind, texts []string) Windows {
	features := rankedFeatures(kind)

	var out Windows

	for _, s := range MeasureWindowShapes(texts, kind, voiceWindowWords, VoiceSeed) {
		if w := readFeatures(s, features); len(w) >= minVoiceFeatures {
			out = append(out, w)
		}
	}

	return out
}

// scoreDraft scores draft on the features it has data for, against the
// windows with data for all of them, and returns the cut those windows set.
// The draft and each window are scored alike, each against the rest of the
// pool the two make. Fisher's statistic grows with the features it sums, so
// a cut from windows scored on fewer features, or on a reference without the
// draft, would let more drafts past than it lets windows. It reports false
// when the draft has fewer than minVoiceFeatures features or no window has
// them all.
func scoreDraft(draft map[Feature]float64, windows Windows, features []voiceFeature) (stat, cut float64, parts []voicePart, ok bool) {
	scored := slices.DeleteFunc(slices.Clone(features), func(f voiceFeature) bool {
		_, has := draft[f.name]

		return !has
	})

	pool := slices.DeleteFunc(slices.Clone(windows), func(w map[Feature]float64) bool {
		return slices.ContainsFunc(scored, func(f voiceFeature) bool {
			_, has := w[f.name]

			return !has
		})
	})

	if len(scored) < minVoiceFeatures || len(pool) == 0 {
		return 0, 0, nil, false
	}

	pool = append(pool, draft)
	reference := sortedValues(pool)
	owner := make([]float64, 0, len(pool)-1)

	for _, w := range pool[:len(pool)-1] {
		score, _ := fisher(w, reference, scored)
		owner = append(owner, score)
	}

	stat, parts = fisher(draft, reference, scored)

	return stat, cutAt(owner), parts, true
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

// fisher combines the one-sided rank p-value of each feature, -2 times the
// sum of their logs, and returns the parts it summed. measured has a value
// for every feature, and reference holds that value among its own.
func fisher(measured map[Feature]float64, reference map[Feature][]float64, features []voiceFeature) (float64, []voicePart) {
	var (
		stat  float64
		parts []voicePart
	)

	for _, f := range features {
		value := measured[f.name]
		p := rankP(reference[f.name], value, f.higher)
		stat += -2 * math.Log(p)
		parts = append(parts, voicePart{feature: f, value: value, p: p})
	}

	return stat, parts
}

// rankP is the share of sorted at least as far as value in the direction
// higher names. sorted holds value, so the share is never 0.
func rankP(sorted []float64, value float64, higher bool) float64 {
	var extreme int

	if higher {
		first, _ := slices.BinarySearch(sorted, value)
		extreme = len(sorted) - first
	} else {
		extreme, _ = slices.BinarySearchFunc(sorted, value, func(e, target float64) int {
			if e > target {
				return 1
			}

			return -1
		})
	}

	return float64(extreme) / float64(len(sorted))
}

var voiceChecks = []check{
	{rule: RuleVoiceDrift, on: voiceJudged, graded: true, judge: voiceDrift},
}

var voiceTexts = map[Rule]ruleText{
	RuleVoiceDrift: {
		code:      "PRF1040",
		dimension: DimensionStance,
		catches: fmt.Sprintf("a draft that reads further from its author's own texts of its kind than %d percent of "+
			"them do, or a commit subject longer than %d percent of the author's", voiceKeepPercent, voiceKeepPercent),
		why: fmt.Sprintf("Runs only with a voice file, which proofread voice build measures from the author's own "+
			"texts, 1,000 words or more of a kind, shuffled and pooled into windows of 200 words or more. A draft of "+
			"200 words or more is ranked on commas, parentheses, colons, semicolons, \"I\", \"and\", repeated "+
			"part-of-speech 4-grams, part-of-speech compression and participial clauses, each only in the direction "+
			"agent drafts move, and the ranks combine by Fisher's method. The draft is scored on the features it has "+
			"data for, against the author's windows with data for all of them, and the draft and each of those "+
			"windows are scored alike against the rest; the draft drifts when it scores past all but %d percent of "+
			"the windows. A commit message is held to the %dth percentile of the author's subject lengths instead. "+
			"The features and their directions come from voice-calibration, which measured one author's held-out "+
			"text against agent drafts and against light agent edits of that text: the rule separates drafts, not "+
			"edits. No firing has been labeled, so the rule advises.", voiceFalseAlarmPercent, voiceKeepPercent),
	},
}

// voiceDrift reports a text that drifts from its kind's reference in the
// voice. It abstains on a kind with no reference, under voiceWindowWords
// words, when fewer than minVoiceFeatures features have data, and when no
// window has data for every feature the text does. A commit message is
// judged on its subject alone.
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

	// TODO: pool a draft under 200 words with the drafts of its kind on its
	// branch or in its thread and score the pool; gathering those drafts is
	// the guard's job.
	if s.Words < voiceWindowWords {
		return nil
	}

	features := rankedFeatures(kind)

	stat, cut, parts, ok := scoreDraft(readFeatures(s, features), k.Windows, features)
	if !ok || stat <= cut {
		return nil
	}

	return []Finding{{Message: driftMessage(in.kind, s.Words, parts, sortedValues(k.Windows))}}
}

// subjectDrift reports a commit subject longer than p95 characters, the
// author's voiceKeepPercent percentile. A p95 of 0 is no reference.
func subjectDrift(text string, p95 float64) []Finding {
	chars := subjectChars(text)
	if p95 == 0 || float64(chars) <= p95 {
		return nil
	}

	return []Finding{{Message: fmt.Sprintf("The subject runs %d characters, longer than %d%% of your own commit "+
		"subjects (yours: %s or fewer). Read it against how you write.", chars, voiceKeepPercent, formatValue(p95, 0))}}
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

	return round2(percentile(chars, voiceKeepPercent/100.0))
}

var (
	voiceDraftNames = map[Kind]string{
		KindChangeDescription: "description",
		KindReviewSummary:     "review",
		KindReviewInline:      "comment",
		KindReviewReply:       "reply",
		KindCommitMessage:     "commit message",
		KindIssue:             "issue",
	}
	voicePlurals = map[Kind]string{
		KindChangeDescription: "change descriptions",
		KindReviewReply:       "review comments",
		KindCommitMessage:     "commit messages",
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

	return fmt.Sprintf("This %s reads further from your own %s than %d%% of your writing does (%d words): %s. "+
		"Read it against how you write.", nameOr(voiceDraftNames, kind), nameOr(voicePlurals, heldKind(kind)),
		voiceKeepPercent, words, strings.Join(named, "; "))
}

// nameOr is kind's entry in names, or the kind's own name for a kind added to
// voiceJudged without one.
func nameOr(names map[Kind]string, kind Kind) string {
	if name, ok := names[kind]; ok {
		return name
	}

	return string(kind)
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
