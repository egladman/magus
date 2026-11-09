package proofread

import (
	"strings"
	"testing"
)

// triadVoice is a voice whose change descriptions run to p95 triads per 1000
// words.
func triadVoice(p95 float64) *Voice {
	return &Voice{Schema: VoiceSchema, Kinds: map[Kind]VoiceStats{
		KindChangeDescription: {Ranges: map[Feature]Range{"triads_per_1000": {P95: p95, Windows: 9}}},
	}}
}

const twoTriads = "Read the cache\n\nThe loader reads the cache, the graph, and the log.\n\n" +
	"It also checks size, age and owner before it writes anything.\n"

const threeTriads = twoTriads + "\nThe writer locks the file, the index and the journal.\n"

func TestTriadHoldsAnAuthorWhoWritesSeriesToTheirOwnRate(t *testing.T) {
	got := JudgeText(twoTriads, KindChangeDescription, WithOnly(RuleTriad), WithVoice(triadVoice(200)))

	if len(got) != 0 {
		t.Errorf("triad = %v, want none within the author's own rate", got)
	}
}

func TestTriadReportsPastTheAuthorsOwnRateAndSaysSo(t *testing.T) {
	got := JudgeText(threeTriads, KindChangeDescription, WithOnly(RuleTriad), WithVoice(triadVoice(70)))

	if len(got) != 3 || !strings.Contains(got[0].Message, "Your own texts of this length allow 2") {
		t.Errorf("triad = %v, want three findings naming the author's allowance of 2", got)
	}
}

func TestTriadKeepsItsDefaultUnderAVoiceThatWritesFewer(t *testing.T) {
	got := JudgeText(twoTriads, KindChangeDescription, WithOnly(RuleTriad), WithVoice(triadVoice(1)))

	if len(got) != 2 || strings.Contains(got[0].Message, "Your own texts") {
		t.Errorf("triad = %v, want the default two findings with no voice note", got)
	}
}

func TestTriadKeepsItsDefaultWhenTheVoiceHasNoRange(t *testing.T) {
	v := &Voice{Schema: VoiceSchema, Kinds: map[Kind]VoiceStats{KindChangeDescription: {}}}

	if got := JudgeText(twoTriads, KindChangeDescription, WithOnly(RuleTriad), WithVoice(v)); len(got) != 2 {
		t.Errorf("triad = %v, want the default two findings", got)
	}
}
