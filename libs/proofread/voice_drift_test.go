package proofread

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

// agentDraft is a reply in the shape voice-calibration measured agent drafts
// in: comma-, paren-, colon- and semicolon-heavy, many "and", no "I".
const agentDraft = "The change updates the cache layer so that every key is computed from the sorted inputs, and the " +
	"test suite now covers the path (both the cold and the warm cache). The handler reads all of the inputs, sorts " +
	"them, and hashes the result: this ensures that one key is produced for each distinct set of inputs; the ordering " +
	"no longer affects the outcome. The existing behavior is preserved for the other callers (the CLI, the daemon and " +
	"the console), and the documentation reflects the new contract: every caller receives the same result, regardless " +
	"of the order in which the inputs arrive. Reviewers should note three points: the hash function is unchanged; the " +
	"key format is unchanged; and the migration path (for stored keys) is covered by a new test. Performance is " +
	"unaffected, and memory use stays flat across the benchmark suite (small, medium and large graphs). The change " +
	"also tightens the error handling: a missing input now fails the build, and the message names the input and the " +
	"target that needed it. Finally, the release notes describe the change, and the upgrade guide explains how to " +
	"rebuild an older cache (a single command) without losing the stored results; existing caches remain readable " +
	"until the next major release, and the deprecation warning names the release that removes them."

func TestVoiceDriftFiresOnAnAgentDraft(t *testing.T) {
	if words := Measure(agentDraft, KindReviewReply).Words; words < voiceWindowWords {
		t.Fatalf("agentDraft has %d words, want %d or more", words, voiceWindowWords)
	}

	for _, kind := range []Kind{KindReviewReply, KindReviewInline} {
		got := drifts(agentDraft, kind, WithVoice(authorVoice(t)))
		if len(got) != 1 || got[0].Decision != DecisionAdvise || got[0].Code != "PRF1040" {
			t.Fatalf("voice-drift on %s = %+v, want one advise finding coded PRF1040", kind, got)
		}

		want := fmt.Sprintf("This %s reads further from your own review comments than 95%% of your writing does (",
			voiceDraftNames[kind])
		if !strings.HasPrefix(got[0].Message, want) || strings.Count(got[0].Message, "; ") != 2 ||
			!strings.Contains(got[0].Message, "(yours: none), higher") {
			t.Errorf("message = %q, want three features named against the author's range", got[0].Message)
		}
	}
}

func TestVoiceDriftPassesTheAuthorsOwnDraft(t *testing.T) {
	own := strings.Join(authorReplies(80)[60:], "\n\n")
	if words := Measure(own, KindReviewReply).Words; words < voiceWindowWords {
		t.Fatalf("the author's draft has %d words, want %d or more", words, voiceWindowWords)
	}

	if got := drifts(own, KindReviewReply, WithVoice(authorVoice(t))); got != nil {
		t.Errorf("the author's own draft drifts: %v", got)
	}
}

func TestVoiceDriftAbstains(t *testing.T) {
	v := authorVoice(t)
	words := strings.Fields(agentDraft)
	oneFeature := &Voice{Schema: VoiceSchema, Kinds: map[Kind]KindVoice{KindReviewReply: {Windows: &Windows{
		Count: 3, Values: map[Feature][]float64{"punctuation.paren": {0, 0, 0}}, Cut: 0.1,
	}}}}

	cases := []struct {
		name, text string
		kind       Kind
		voice      *Voice
	}{
		{"on a kind the voice lacks", agentDraft, KindIssue, v},
		{"under 15 words", "Summary: the key (sorted) is stable; and the tests, and docs, pass.", KindReviewReply, v},
		{"under 200 words", strings.Join(words[:150], " "), KindReviewReply, v},
		{"with fewer than 2 features with data", agentDraft, KindReviewReply, oneFeature},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := drifts(tc.text, tc.kind, WithVoice(tc.voice)); got != nil {
				t.Errorf("voice-drift = %v, want none", got)
			}
		})
	}
}

// authorCommits are n commit messages of about 25 words with subjects of 22
// to 38 characters.
func authorCommits(n int) []string {
	out := make([]string, n)

	for i := range out {
		subject := "fix the cache key" + strings.Repeat(" x", i%9)
		out[i] = subject + "\n\nThe key ignored the order of the inputs, so two runs of one target missed each " +
			"other's entries in the cache."
	}

	return out
}

func TestVoiceDriftHoldsACommitToItsSubjectLength(t *testing.T) {
	v, err := BuildVoice(map[Kind][]string{KindCommitMessage: authorCommits(60)})
	if err != nil {
		t.Fatal(err)
	}

	k := v.Kinds[KindCommitMessage]
	if k.Windows != nil || k.SubjectP95 != 33 {
		t.Fatalf("commit-message = %+v, want no windows and a subject p95 of 33", k)
	}

	long := "make the cache key independent of input order across targets"

	got := drifts(long, KindCommitMessage, WithVoice(v))
	if len(got) != 1 || got[0].Message != "The subject runs 60 characters, longer than 95% of your own commit subjects "+
		"(yours: 33 or fewer). Read it against how you write." {
		t.Errorf("voice-drift on a long subject = %+v", got)
	}

	if got := drifts("fix the cache key\n\n"+agentDraft, KindCommitMessage, WithVoice(v)); got != nil {
		t.Errorf("voice-drift on a short subject over an agent body = %v, want none: commits get no shape score", got)
	}

	short, err := BuildVoice(map[Kind][]string{KindCommitMessage: authorCommits(10)})
	if err != nil {
		t.Fatal(err)
	}

	if got := drifts(long, KindCommitMessage, WithVoice(short)); got != nil {
		t.Errorf("voice-drift with no subject reference = %v, want none", got)
	}
}

// syntheticWindows are n windows' feature values drawn under seed.
func syntheticWindows(n int, seed uint64) []map[Feature]float64 {
	rng := rand.New(rand.NewPCG(seed, 0))
	out := make([]map[Feature]float64, n)

	for i := range out {
		out[i] = map[Feature]float64{}
		for _, f := range voiceFeatures {
			out[i][f.name] = round2(rng.Float64() * 10)
		}
	}

	return out
}

func TestTheCutLetsFivePercentOfTheAuthorsWindowsPast(t *testing.T) {
	features := rankedFeatures(KindChangeDescription)

	for _, tc := range []struct{ windows, past int }{{40, 2}, {100, 5}, {19, 0}} {
		measured := syntheticWindows(tc.windows, 7)
		w := measuredWindows(measured, features)
		scores := ownerScores(measured, features)

		past := 0

		for _, s := range scores {
			if s > w.Cut {
				past++
			}
		}

		if len(scores) != tc.windows || past != tc.past {
			t.Errorf("%d windows: %d scored and %d past the cut %v, want %d past", tc.windows, len(scores), past, w.Cut, tc.past)
		}

		if tc.past == 0 && w.Cut != slices.Max(scores) {
			t.Errorf("%d windows: cut %v, want the highest score %v", tc.windows, w.Cut, slices.Max(scores))
		}
	}
}

func TestRankPCountsOnlyTheAgentDirection(t *testing.T) {
	sorted := []float64{1, 2, 3, 4}

	cases := []struct {
		value  float64
		higher bool
		want   float64
	}{
		{3, true, 3.0 / 5},
		{9, true, 1.0 / 5},
		{0, true, 1},
		{2, false, 3.0 / 5},
		{0, false, 1.0 / 5},
	}

	for _, tc := range cases {
		if got := rankP(sorted, tc.value, tc.higher); got != tc.want {
			t.Errorf("rankP(%v, higher %v) = %v, want %v", tc.value, tc.higher, got, tc.want)
		}
	}
}

func TestDriftMessageNamesTheThreeSmallestP(t *testing.T) {
	feature := func(name Feature) voiceFeature {
		i := slices.IndexFunc(voiceFeatures, func(f voiceFeature) bool { return f.name == name })

		return voiceFeatures[i]
	}

	parts := []voicePart{
		{feature("punctuation.comma"), 5, 0.5},
		{feature("punctuation.paren"), 2.94, 0.1},
		{feature("punctuation.semicolon"), 2.9, 0.1},
		{feature("function_words.and"), 29.04, 0.2},
	}
	reference := map[Feature][]float64{
		"punctuation.comma":     {1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
		"punctuation.paren":     {0, 0, 0, 0, 0, 0, 0, 0.5, 0.5, 0.5, 0.5},
		"punctuation.semicolon": {0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		"function_words.and":    {1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 20},
	}

	want := `This description reads further from your own change descriptions than 95% of your writing does ` +
		`(346 words): parentheses 2.9 per 100 words (yours: 0 to 0.5), higher; semicolons 2.9 per 100 words ` +
		`(yours: none), higher; "and" 29 per 1000 words (yours: 2 to 15), higher. Read it against how you write.`
	if got := driftMessage(KindChangeDescription, 346, parts, reference); got != want {
		t.Errorf("driftMessage =\n%s\nwant\n%s", got, want)
	}
}

// markedTexts are n ten-word texts, each opening with a marker no other holds.
func markedTexts(n int) []string {
	out := make([]string, n)

	for i := range out {
		out[i] = fmt.Sprintf("mark%02d one two three four five six seven eight nine.", i)
	}

	return out
}

func TestPoolWindowsChunksTheSameForTheSameSeed(t *testing.T) {
	texts := markedTexts(12)

	a := poolWindows(KindReviewReply, texts, 30, 13)
	b := poolWindows(KindReviewReply, texts, 30, 13)

	if len(a) != 4 || !slices.Equal(a, b) {
		t.Errorf("one seed gave %q then %q, want the same 4 windows", a, b)
	}

	if c := poolWindows(KindReviewReply, texts, 30, 14); slices.Equal(a, c) {
		t.Errorf("seeds 13 and 14 gave the same windows %q", a)
	}

	if !slices.Equal(texts, markedTexts(12)) {
		t.Error("poolWindows reordered its input")
	}
}

func TestPoolWindowsUsesEachTextOnceAndDropsTheRemainder(t *testing.T) {
	windows := poolWindows(KindReviewReply, markedTexts(11), 30, 13)

	if len(windows) != 3 {
		t.Fatalf("11 texts of 10 words in windows of 30+ made %d windows, want 3", len(windows))
	}

	used := 0

	for i, text := range markedTexts(11) {
		marker := fmt.Sprintf("mark%02d", i)

		n := 0
		for _, w := range windows {
			n += strings.Count(w, marker)
		}

		if n > 1 {
			t.Errorf("%s is in %d windows, want at most 1", text, n)
		}

		used += n
	}

	if used != 9 {
		t.Errorf("windows hold %d texts, want 9: the last 2 are a window under 30 words", used)
	}

	for _, w := range windows {
		if got := Measure(w, KindReviewReply).Words; got < 30 {
			t.Errorf("window %q has %d words, want 30 or more", w, got)
		}

		if !strings.Contains(w, "\n\n") {
			t.Errorf("window %q joins its texts without a blank line", w)
		}
	}
}

func TestPoolWindowsKeepsEveryCommitSubject(t *testing.T) {
	texts := make([]string, 12)
	for i := range texts {
		texts[i] = fmt.Sprintf("fix the cache key for target %02d", i)
	}

	windows := poolWindows(KindCommitMessage, texts, 20, 13)
	if len(windows) != 4 {
		t.Fatalf("pooled 12 seven-word subjects into %d windows of 20+ words, want 4", len(windows))
	}

	for _, w := range windows {
		if got := strings.Count(w, "fix the cache key"); got != 3 {
			t.Errorf("window %q holds %d subjects, want 3", w, got)
		}
	}
}

func TestMeasureWindowsMeasuresEachWindow(t *testing.T) {
	shapes := MeasureWindows(markedTexts(12), KindReviewReply, 30, 13)
	if len(shapes) != 4 {
		t.Fatalf("MeasureWindows = %d shapes, want 4", len(shapes))
	}

	for _, s := range shapes {
		if s.Words < 30 {
			t.Errorf("a window measured %d words, want 30 or more", s.Words)
		}
	}
}
