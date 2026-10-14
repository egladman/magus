package proofread

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
)

// VoiceSchema names the shape of a voice file, so a file written by another
// version of proofread is refused rather than misread.
const VoiceSchema = "proofread-voice/3"

// olderVoiceSchemas are the schemas earlier proofread versions wrote. A file
// in one is refused with the command that rebuilds it.
//
// compat(until: no voice file in proofread-voice/1 or /2 is still read):
// naming the rebuild beats the bare "not proofread-voice/3" refusal.
var olderVoiceSchemas = []string{"proofread-voice/1", "proofread-voice/2"}

// MinVoiceTexts is the fewest change descriptions a [Voice] relaxes the tense
// rule from.
const MinVoiceTexts = 30

// MinVoiceWords is the fewest words of one kind a [Voice] builds a reference
// from. Under it the kind keeps its counts, and voice-drift abstains on it.
const MinVoiceWords = 1000

// Feature names one measurement of a [Shape] by its JSON path: a number by
// its name, a map entry as "<map>.<key>".
type Feature string

// labelAuthor marks a case file section of an author's own texts. It holds no
// case: its texts build the [Voice] every case in its file is judged with.
const labelAuthor Label = "author"

// voiceKinds are the kinds a [Voice] measures.
var voiceKinds = []Kind{KindChangeDescription, KindReviewReply, KindCommitMessage, KindIssue}

// voiceJudged are the kinds held to a voice: voiceKinds and every review text.
var voiceJudged = slices.Concat(voiceKinds, []Kind{KindReviewSummary, KindReviewInline})

// heldKind is the kind whose measures in a [Voice] k is held to. A person writes
// a review's summary, its inline comments and its replies in one voice, which a
// voice keeps under review-reply.
func heldKind(k Kind) Kind {
	if isReview(k) {
		return KindReviewReply
	}

	return k
}

// Voice is one author's measured style per kind of text. It holds aggregates
// alone: counts, rates and window measures, never a text, a word the author
// chose or a path.
type Voice struct {
	Schema string              `json:"schema"`
	Kinds  map[Kind]VoiceStats `json:"kinds"`
}

// VoiceStats is a [Voice] over the texts of one kind.
type VoiceStats struct {
	Texts int `json:"texts"`
	Words int `json:"words"`
	// FirstPerson and Future are the rates per 1000 words, pooled over the
	// texts, of the first-person singular and the future tense the tense rule
	// reports.
	FirstPerson float64 `json:"first_person_per_1000"`
	Future      float64 `json:"future_per_1000"`
	// Windows is the reference voice-drift ranks a draft of the kind against.
	// It is nil on commit messages, under [MinVoiceWords] words, and when no
	// window measured two features.
	Windows Windows `json:"windows,omitempty"`
	// SubjectP95 is the 95th percentile, in characters, of the author's
	// commit subjects. It is set on commit messages of [MinVoiceWords] words
	// or more, and 0 otherwise.
	SubjectP95 float64 `json:"subject_chars_p95,omitempty"`
}

// Windows is what an author's texts of one kind measure when shuffled and
// pooled into windows of 200 words or more: per window, each feature
// voice-drift ranks the kind on that the window has data for. A window with
// fewer than two is not kept. The cut a draft is held to is set when the
// draft is judged, from the windows with data for every feature the draft
// has.
type Windows []map[Feature]float64

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

// BuildVoice measures texts, keyed by kind, into a [Voice]. A kind of
// [MinVoiceWords] words or more gets a reference: the commit subjects' 95th
// percentile for commit messages, and [Windows] for the other kinds. A kind
// outside change-description, review-reply, commit-message and issue is an
// error, and so are texts with no prose word among them. A text with no prose
// word is not counted.
func BuildVoice(texts map[Kind][]string) (*Voice, error) {
	v := &Voice{Schema: VoiceSchema, Kinds: map[Kind]VoiceStats{}}

	for kind, list := range texts {
		if !slices.Contains(voiceKinds, kind) {
			return nil, fmt.Errorf("a voice measures %s, not %s", kindList(voiceKinds), kind)
		}

		var (
			k                   VoiceStats
			kept                []string
			firstPerson, future int
		)

		for _, text := range list {
			words, fp, fut := tenseCounts(text, kind)
			if words == 0 {
				continue
			}

			k.Texts++
			k.Words += words
			firstPerson += fp
			future += fut
			kept = append(kept, text)
		}

		if k.Texts == 0 {
			continue
		}

		k.FirstPerson = round2(1000 * float64(firstPerson) / float64(k.Words))
		k.Future = round2(1000 * float64(future) / float64(k.Words))

		switch {
		case k.Words < MinVoiceWords:
			// Too few words to rank a draft against: the tense counts alone stand.
		case kind == KindCommitMessage:
			k.SubjectP95 = subjectP95(kept)
		default:
			k.Windows = buildWindows(kind, kept)
		}

		v.Kinds[kind] = k
	}

	if len(v.Kinds) == 0 {
		return nil, errors.New("no text has a prose word to measure")
	}

	return v, nil
}

// tenseCounts returns text's prose words and its counts of the first-person
// singular and the future tense, read as kind.
func tenseCounts(text string, kind Kind) (words, firstPersons, futures int) {
	paras := readKind(text, kind)

	words = measure(paras).Words
	if words == 0 {
		return 0, 0, 0
	}

	for _, para := range paras {
		if para.head.heading {
			continue
		}

		prose := maskSpans(para.text)
		firstPersons += len(firstPerson.FindAllString(prose, -1))
		futures += len(future.FindAllString(prose, -1))
	}

	return words, firstPersons, futures
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

// ReadVoice decodes a voice file. A schema an earlier proofread wrote is an
// error that names `proofread voice build`, which rebuilds the file. So is
// any other schema than [VoiceSchema], a field, kind or feature proofread does
// not know, a feature voice-drift does not rank on its kind, a window with
// fewer than two features, a negative subject length, and a reference on a
// kind that cannot hold it.
func ReadVoice(r io.Reader) (*Voice, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read: %w", err)
	}

	var head struct {
		Schema string `json:"schema"`
	}

	if err := json.Unmarshal(data, &head); err != nil {
		return nil, fmt.Errorf("voice file schema: %w", err)
	}

	switch {
	case slices.Contains(olderVoiceSchemas, head.Schema):
		return nil, fmt.Errorf("schema %q is from an earlier proofread: rerun `proofread voice build` to rebuild the voice file",
			head.Schema)
	case head.Schema != VoiceSchema:
		return nil, fmt.Errorf("schema %q is not %s", head.Schema, VoiceSchema)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	var v Voice
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("voice file: %w", err)
	}

	for kind, k := range v.Kinds {
		if err := checkVoiceStats(kind, k); err != nil {
			return nil, err
		}
	}

	return &v, nil
}

func checkVoiceStats(kind Kind, k VoiceStats) error {
	switch {
	case !slices.Contains(voiceKinds, kind):
		return fmt.Errorf("a voice measures %s, not %s", kindList(voiceKinds), kind)
	case k.SubjectP95 < 0:
		return fmt.Errorf("%s: subject length %v is negative", kind, k.SubjectP95)
	case k.SubjectP95 > 0 && kind != KindCommitMessage:
		return fmt.Errorf("%s: only commit messages keep a subject length", kind)
	case k.Windows == nil:
		return nil
	case kind == KindCommitMessage:
		return fmt.Errorf("%s: commit messages keep no windows", kind)
	}

	ranked := rankedFeatures(kind)

	for i, w := range k.Windows {
		if len(w) < minVoiceFeatures {
			return fmt.Errorf("%s: window %d has %d features, want %d or more", kind, i, len(w), minVoiceFeatures)
		}

		for f := range w {
			if !slices.ContainsFunc(ranked, func(r voiceFeature) bool { return r.name == f }) {
				return fmt.Errorf("%s: unknown feature %q", kind, f)
			}
		}
	}

	return nil
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

func kindList(kinds []Kind) string {
	names := make([]string, len(kinds))
	for i, k := range kinds {
		names[i] = string(k)
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
