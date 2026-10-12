package proofread

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// authorReplies are n short replies in an invented author's voice: first
// person, contracted, no markup, 9 to 25 words.
func authorReplies(n int) []string {
	extra := []string{"", " It's close.", " We'll see.", " I'd keep it.", " That's fine by me, honestly."}
	out := make([]string, n)

	for i := range out {
		out[i] = fmt.Sprintf("I think this works for case %d. Let's merge it after the build.%s I can't see a problem.",
			i, extra[i%len(extra)])
	}

	return out
}

// draftedReply is a reply in the shape the voice study measured agent text in:
// long, bold-labelled, colon-heavy and uncontracted.
const draftedReply = "**Summary:** The change updates the cache layer so that every key is computed with the " +
	"sorted inputs, and the test suite now covers the path. **Details:** The handler (in the server " +
	"package) reads all of the inputs, sorts them, and hashes the result: this ensures that one key is " +
	"produced for each distinct set of inputs, with the ordering no longer affecting the outcome. The " +
	"existing behavior is preserved for all of the other callers, and the documentation reflects the " +
	"new contract as well."

func authorVoice(t *testing.T) *Voice {
	t.Helper()

	v, err := BuildVoice(map[Kind][]string{KindReviewReply: authorReplies(40)})
	if err != nil {
		t.Fatal(err)
	}

	return v
}

func drifts(text string, kind Kind, opts ...Option) []Finding {
	var out []Finding

	for _, f := range JudgeText(text, kind, opts...) {
		if f.Rule == RuleVoiceDrift {
			out = append(out, f)
		}
	}

	return out
}

func TestBuildVoiceKeepsAggregatesAlone(t *testing.T) {
	texts := authorReplies(40)
	texts[3] = "Zanzibar ships the quokka build."

	v, err := BuildVoice(map[Kind][]string{KindReviewReply: texts})
	if err != nil {
		t.Fatal(err)
	}

	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	for _, word := range []string{"zanzibar", "quokka", "merge", "think"} {
		if strings.Contains(strings.ToLower(string(data)), word) {
			t.Errorf("the voice file holds the word %q:\n%s", word, data)
		}
	}

	k := v.Kinds[KindReviewReply]
	if v.Schema != VoiceSchema || k.Texts != 40 || k.FirstPerson == 0 {
		t.Errorf("BuildVoice = %+v, want schema %s, 40 texts and a first-person rate", v, VoiceSchema)
	}

	words, ok := k.Ranges[FeatureWords]
	if !ok || words.Texts != 40 || words.P10 >= words.P90 {
		t.Errorf("words range = %+v, want one over 40 texts with p10 under p90", words)
	}
}

func TestBuildVoiceSetsNoRangesUnderTheMinimum(t *testing.T) {
	v, err := BuildVoice(map[Kind][]string{KindReviewReply: authorReplies(MinVoiceTexts - 1)})
	if err != nil {
		t.Fatal(err)
	}

	if k := v.Kinds[KindReviewReply]; k.Ranges != nil || k.Texts != MinVoiceTexts-1 {
		t.Errorf("kind = %+v, want its counts and no ranges", k)
	}

	if got := drifts(draftedReply, KindReviewReply, WithVoice(v)); got != nil {
		t.Errorf("voice-drift under the minimum = %v, want none", got)
	}
}

func TestBuildVoiceRefusesWhatItCannotMeasure(t *testing.T) {
	cases := map[string]struct {
		texts map[Kind][]string
		want  string
	}{
		"a kind with no voice": {map[Kind][]string{KindGuide: {"Run it."}},
			"a voice measures change-description, review-reply, commit-message and issue, not guide"},
		"no prose word": {map[Kind][]string{KindReviewReply: {"", "```\ncode\n```"}}, "no text has a prose word to measure"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := BuildVoice(tc.texts); err == nil || err.Error() != tc.want {
				t.Errorf("BuildVoice error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestVoiceDriftNeedsTwoFeaturesOutOfRange(t *testing.T) {
	v := authorVoice(t)

	if got := drifts(authorReplies(3)[2], KindReviewReply, WithVoice(v)); got != nil {
		t.Errorf("the author's own reply drifts: %v", got)
	}

	if got := drifts("I think this **works** for case 9. Let's merge it after the build. We'll see. I can't see a problem.",
		KindReviewReply, WithVoice(v)); got != nil {
		t.Errorf("one feature out of range drifts: %v", got)
	}

	got := drifts(draftedReply, KindReviewReply, WithVoice(v))
	if len(got) != 1 || got[0].Decision != DecisionAdvise || got[0].Code != "PRF1040" {
		t.Fatalf("voice-drift = %+v, want one advise finding coded PRF1040", got)
	}

	for _, want := range []string{"Reads unlike your own review replies: ", "words 84 above your ", "headings and bold 2 above your 0-0"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("message %q does not name %q", got[0].Message, want)
		}
	}
}

func TestVoiceDriftNamesAFeatureBelowItsRange(t *testing.T) {
	v := &Voice{Schema: VoiceSchema, Kinds: map[Kind]KindVoice{KindReviewReply: {Texts: 40, Ranges: map[Feature]Range{
		FeatureWords:      {Texts: 40, P10: 30, P50: 40, P90: 50},
		FeatureFormatting: {Texts: 40, P10: 1, P50: 1, P90: 2},
	}}}}

	got := drifts("It works.", KindReviewReply, WithVoice(v))
	if len(got) != 1 || !strings.Contains(got[0].Message, "words 2 below your 30-50, headings and bold 0 below your 1-2") {
		t.Errorf("voice-drift = %+v, want both features named below their range", got)
	}
}

func TestVoiceDriftIsOffWithoutAVoice(t *testing.T) {
	if got := drifts(draftedReply, KindReviewReply); got != nil {
		t.Errorf("voice-drift with no voice = %v, want none", got)
	}

	if got := drifts(draftedReply, KindReviewReply, WithOnly(RuleVoiceDrift)); got != nil {
		t.Errorf("voice-drift named by -only with no voice = %v, want none", got)
	}
}

func TestAVoiceRelaxesTenseOnChangeDescriptionsAlone(t *testing.T) {
	writesI := &Voice{Schema: VoiceSchema, Kinds: map[Kind]KindVoice{
		KindChangeDescription: {Texts: MinVoiceTexts, Words: 600, FirstPerson: 21, Future: 4},
	}}
	tooFew := &Voice{Schema: VoiceSchema, Kinds: map[Kind]KindVoice{
		KindChangeDescription: {Texts: MinVoiceTexts - 1, Words: 600, FirstPerson: 21, Future: 4},
	}}
	noFuture := &Voice{Schema: VoiceSchema, Kinds: map[Kind]KindVoice{
		KindChangeDescription: {Texts: MinVoiceTexts, Words: 600, FirstPerson: 21, Future: 1},
	}}
	deny := WithDecisions(map[Rule]Decision{RuleTense: DecisionDeny})

	cases := []struct {
		name  string
		kind  Kind
		voice *Voice
		want  Decision
	}{
		{"no voice", KindChangeDescription, nil, DecisionDeny},
		{"a voice that writes I and will", KindChangeDescription, writesI, DecisionAdvise},
		{"the same voice on an issue", KindIssue, writesI, DecisionDeny},
		{"too few descriptions", KindChangeDescription, tooFew, DecisionDeny},
		{"no future tense", KindChangeDescription, noFuture, DecisionDeny},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			found := JudgeText("keep the cache key stable\n\nI will sort the inputs before hashing.", tc.kind,
				deny, WithOnly(RuleTense), WithVoice(tc.voice))
			if len(found) == 0 {
				t.Fatal("tense found nothing")
			}

			for _, f := range found {
				if f.Decision != tc.want {
					t.Errorf("tense on %q = %s, want %s", f.Match, f.Decision, tc.want)
				}
			}
		})
	}
}

func TestBuildVoiceMeasuresTheRatesThatRelaxTense(t *testing.T) {
	texts := make([]string, MinVoiceTexts)
	for i := range texts {
		texts[i] = fmt.Sprintf("fix the build %d\n\nI will add the missing flag, and I will test it on my machine.", i)
	}

	v, err := BuildVoice(map[Kind][]string{KindChangeDescription: texts})
	if err != nil {
		t.Fatal(err)
	}

	if !v.RelaxesTense() {
		t.Errorf("RelaxesTense = false for %+v, want true", v.Kinds[KindChangeDescription])
	}

	if (*Voice)(nil).RelaxesTense() {
		t.Error("a nil voice relaxes tense")
	}
}

func TestReadVoiceRoundTripsAndRefusesBadFiles(t *testing.T) {
	data, err := json.Marshal(authorVoice(t))
	if err != nil {
		t.Fatal(err)
	}

	v, err := ReadVoice(strings.NewReader(string(data)))
	if err != nil || v.Kinds[KindReviewReply].Texts != 40 {
		t.Fatalf("ReadVoice = %+v, %v, want the voice back", v, err)
	}

	cases := map[string]struct{ body, want string }{
		"another schema": {`{"schema":"proofread-voice/9","kinds":{}}`, `schema "proofread-voice/9" is not proofread-voice/1`},
		"an unknown kind": {`{"schema":"proofread-voice/1","kinds":{"guide":{}}}`,
			"a voice measures change-description, review-reply, commit-message and issue, not guide"},
		"an unknown feature": {`{"schema":"proofread-voice/1","kinds":{"issue":{"ranges":{"typos":{}}}}}`,
			`issue: unknown feature "typos"`},
		"an inverted range": {`{"schema":"proofread-voice/1","kinds":{"issue":{"ranges":{"words":{"p10":9,"p90":1}}}}}`,
			"issue: words has p10 9 over p90 1"},
		"an unknown field": {`{"schema":"proofread-voice/1","text":"hi"}`, `json: unknown field "text"`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadVoice(strings.NewReader(tc.body)); err == nil || err.Error() != tc.want {
				t.Errorf("ReadVoice error = %v, want %q", err, tc.want)
			}
		})
	}
}
