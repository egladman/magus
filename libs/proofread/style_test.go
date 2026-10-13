package proofread

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

var authorSentences = []string{
	"Let's merge it after the build.", "I can't see a problem.", "It's close.", "We'll see.", "I'd keep it.",
	"That's fine by me, honestly.", "I'll fix the typo in the docs.", "It's already idempotent.",
	"Can you share the log?", "I'll split it into two commits.", "I don't think it matters for now.",
	"We can drop this once the server is gone.", "Good catch.", "I think the test is flaky, not the code.",
}

// authorReplies are n short replies in an invented author's voice: first
// person, contracted, no markup, about 20 words each.
func authorReplies(n int) []string {
	out := make([]string, n)

	for i := range out {
		picked := []string{fmt.Sprintf("I think this works for case %d.", i)}
		for j := 1; j <= 3; j++ {
			picked = append(picked, authorSentences[(i*j+j*5)%len(authorSentences)])
		}

		out[i] = strings.Join(picked, " ")
	}

	return out
}

func authorVoice(t *testing.T) *Voice {
	t.Helper()

	v, err := BuildVoice(map[Kind][]string{KindReviewReply: authorReplies(60)})
	if err != nil {
		t.Fatal(err)
	}

	if v.Kinds[KindReviewReply].Windows == nil {
		t.Fatalf("authorVoice has no windows: %+v", v.Kinds[KindReviewReply])
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
	texts := authorReplies(60)
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
	if v.Schema != VoiceSchema || k.Texts != 60 || k.Words < MinVoiceWords || k.FirstPerson == 0 {
		t.Fatalf("BuildVoice = %+v, want schema %s, 60 texts, %d words or more and a first-person rate", v, VoiceSchema,
			MinVoiceWords)
	}

	if k.Windows == nil || k.Windows.Count < 5 || k.SubjectP95 != 0 {
		t.Fatalf("review-reply = %+v, want 5 windows or more and no subject length", k)
	}

	for f, values := range k.Windows.Values {
		if !slices.IsSorted(values) || len(values) > k.Windows.Count {
			t.Errorf("%s = %v, want at most %d sorted values", f, values, k.Windows.Count)
		}
	}

	if _, ok := k.Windows.Values["function_words.i"]; !ok {
		t.Errorf("values = %v, want the rate of I", k.Windows.Values)
	}
}

func TestBuildVoiceSkipsAKindUnderMinVoiceWords(t *testing.T) {
	v, err := BuildVoice(map[Kind][]string{KindReviewReply: authorReplies(10)})
	if err != nil {
		t.Fatal(err)
	}

	if k := v.Kinds[KindReviewReply]; k.Windows != nil || k.Texts != 10 || k.Words >= MinVoiceWords {
		t.Errorf("kind = %+v, want its counts and no windows", k)
	}

	if got := drifts(agentDraft, KindReviewReply, WithVoice(v)); got != nil {
		t.Errorf("voice-drift with no reference = %v, want none", got)
	}
}

func TestBuildVoiceRanksEachKindOnItsOwnFeatures(t *testing.T) {
	long := make([]string, 8)
	for i := range long {
		long[i] = strings.Join(authorReplies(12), " ")
	}

	v, err := BuildVoice(map[Kind][]string{KindIssue: long, KindReviewReply: long, KindChangeDescription: long})
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		kind    Kind
		feature Feature
		want    bool
	}{
		{KindIssue, "punctuation.colon", false},
		{KindIssue, "template_rate", true},
		{KindReviewReply, "template_rate", false},
		{KindReviewReply, "tag_compression", false},
		{KindReviewReply, "punctuation.colon", true},
		{KindChangeDescription, "tag_compression", true},
	}

	for _, tc := range cases {
		w := v.Kinds[tc.kind].Windows
		if w == nil {
			t.Fatalf("%s has no windows", tc.kind)
		}

		if _, got := w.Values[tc.feature]; got != tc.want {
			t.Errorf("%s ranks %s = %v, want %v", tc.kind, tc.feature, got, tc.want)
		}
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

func TestVoiceDriftIsOffWithoutAVoice(t *testing.T) {
	if got := drifts(agentDraft, KindReviewReply); got != nil {
		t.Errorf("voice-drift with no voice = %v, want none", got)
	}

	if got := drifts(agentDraft, KindReviewReply, WithOnly(RuleVoiceDrift)); got != nil {
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
	if err != nil || v.Kinds[KindReviewReply].Texts != 60 || v.Kinds[KindReviewReply].Windows == nil {
		t.Fatalf("ReadVoice = %+v, %v, want the voice back", v, err)
	}

	issue := func(body string) string { return `{"schema":"proofread-voice/2","kinds":{"issue":` + body + `}}` }

	cases := map[string]struct{ body, want string }{
		"an earlier version": {`{"schema":"proofread-voice/1","kinds":{"issue":{"ranges":{}}}}`,
			"schema \"proofread-voice/1\" is from an earlier proofread: rerun `proofread voice build` to rebuild the voice file"},
		"another schema": {`{"schema":"proofread-voice/9","kinds":{}}`, `schema "proofread-voice/9" is not proofread-voice/2`},
		"an unknown kind": {`{"schema":"proofread-voice/2","kinds":{"guide":{}}}`,
			"a voice measures change-description, review-reply, commit-message and issue, not guide"},
		"an unknown feature": {issue(`{"windows":{"count":1,"values":{"typos":[1]},"cut":1}}`),
			`issue: unknown feature "typos"`},
		"a feature off for the kind": {issue(`{"windows":{"count":1,"values":{"punctuation.colon":[1]},"cut":1}}`),
			`issue: unknown feature "punctuation.colon"`},
		"unsorted values": {issue(`{"windows":{"count":2,"values":{"punctuation.comma":[2,1]},"cut":1}}`),
			"issue: punctuation.comma values are not sorted"},
		"more values than windows": {issue(`{"windows":{"count":1,"values":{"punctuation.comma":[1,2]},"cut":1}}`),
			"issue: punctuation.comma has 2 values for 1 windows"},
		"a negative cut": {issue(`{"windows":{"count":1,"values":{},"cut":-1}}`), "issue: cut -1 is negative"},
		"a subject length off commits": {issue(`{"subject_chars_p95":40}`),
			"issue: only commit messages keep a subject length"},
		"windows on commits": {`{"schema":"proofread-voice/2","kinds":{"commit-message":{"windows":{"count":0,"values":{},"cut":0}}}}`,
			"commit-message: commit messages keep no windows"},
		"an unknown field": {`{"schema":"proofread-voice/2","text":"hi"}`, `json: unknown field "text"`},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadVoice(strings.NewReader(tc.body)); err == nil || err.Error() != tc.want {
				t.Errorf("ReadVoice error = %v, want %q", err, tc.want)
			}
		})
	}
}
