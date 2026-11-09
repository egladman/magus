package proofread

import "testing"

// evenCommits are twelve commit messages that each open on "Update" and run
// to one length: none shows a spread alone, and together they are even.
func evenCommits() []string {
	var out []string

	for _, area := range []string{"parser", "writer", "loader", "tests", "docs", "cache", "index", "server",
		"client", "config", "logger", "router"} {
		out = append(out, "update the "+area+" for the new header\n\n"+
			"- Updated the "+area+" to read the new header format.\n"+
			"- Updated the "+area+" to write the new header format.\n"+
			"- Updated the "+area+" to check the new header format.\n")
	}

	return out
}

func commitVoice() *Voice {
	return &Voice{Schema: VoiceSchema, Kinds: map[Kind]VoiceStats{
		KindCommitMessage: {Ranges: map[Feature]Range{
			"item_words_sd":        {P5: 2, P95: 9, Windows: 9},
			"item_opener_share":    {P5: 0.2, P95: 0.5, Windows: 9},
			"opener_class_entropy": {P5: 1.2, P95: 2.4, Windows: 9},
		}},
	}}
}

func TestJudgeSetReportsASetMoreEvenThanItsAuthor(t *testing.T) {
	got, judged := JudgeSet(evenCommits(), KindCommitMessage, WithVoice(commitVoice()), uniformOn)

	if !judged || len(got) != 1 || got[0].Rule != RuleUniform || got[0].Line != 0 {
		t.Errorf("JudgeSet = %+v, %v, want one uniform finding with no line", got, judged)
	}
}

func TestJudgeSetLeavesEachShortTextAlone(t *testing.T) {
	for _, text := range evenCommits() {
		got := JudgeText(text, KindCommitMessage, WithOnly(RuleUniform), WithVoice(commitVoice()), uniformOn)
		if len(got) != 0 {
			t.Fatalf("uniform on one commit = %+v, want none: it is judged in its set", got)
		}
	}
}

func TestJudgeSetJudgesNothingUnder200Words(t *testing.T) {
	got, judged := JudgeSet(evenCommits()[:2], KindCommitMessage, WithVoice(commitVoice()), uniformOn)

	if judged || got != nil {
		t.Errorf("JudgeSet = %+v, %v, want nothing judged", got, judged)
	}
}

func TestJudgeSetRunsOnlyTheSetRules(t *testing.T) {
	got, _ := JudgeSet(evenCommits(), KindCommitMessage, WithVoice(commitVoice()), uniformOn, WithOnly(RuleTriad, RuleTense))

	for _, f := range got {
		if f.Rule != RuleTriad {
			t.Errorf("JudgeSet ran %s, want triad alone when -only names triad and tense", f.Rule)
		}
	}
}
