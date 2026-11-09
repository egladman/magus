package proofread

import (
	"slices"
	"strings"
	"testing"
)

// uniformOn turns on uniform, which ships off.
var uniformOn = WithDecisions(map[Rule]Decision{RuleUniform: DecisionAdvise})

func TestUniformShipsOff(t *testing.T) {
	got := JudgeText(evenDescription, KindChangeDescription, WithVoice(unevenVoice()))
	if slices.ContainsFunc(got, func(f Finding) bool { return f.Rule == RuleUniform }) {
		t.Errorf("uniform ships on: %v", got)
	}
}

// unevenVoice is an author whose change descriptions vary their items and
// openers.
func unevenVoice() *Voice {
	return &Voice{Schema: VoiceSchema, Kinds: map[Kind]VoiceStats{
		KindChangeDescription: {Ranges: map[Feature]Range{
			"item_words_sd":        {P5: 3, P95: 12, Windows: 9},
			"item_opener_share":    {P5: 0.2, P95: 0.5, Windows: 9},
			"opener_class_entropy": {P5: 1.2, P95: 2.4, Windows: 9},
		}},
	}}
}

const evenItems = `- Updated the parser to read the new header format.
- Updated the writer to emit the new header format.
- Updated the loader to accept the new header format.
- Updated the tests to cover the new header format.
- Updated the docs to describe the new header format.
`

const unevenItems = `- Rewrote the parser.
- The writer kept its old header until the 2.3 release, so readers built before then still open the files it writes.
- Tests for the empty file and the file with no trailing newline.
- Moved the loader into its own package after the cache split.
- Docs.
`

// evenDescription and unevenDescription repeat their items past the 200
// words uniform judges a single text at.
var (
	evenDescription = "fix(parser): read the new header\n\nUpdated the parser to read the new header.\n\n" +
		strings.Repeat(evenItems, 5)
	unevenDescription = "fix(parser): read the new header\n\n" +
		"The parser reads the new header now, which unblocks the 2.3 release.\n\n" + strings.Repeat(unevenItems, 4)
)

func TestUniformLeavesATextUnder200Words(t *testing.T) {
	short := "fix(parser): read the new header\n\nUpdated the parser.\n\n" + evenItems

	if got := JudgeText(short, KindChangeDescription, WithOnly(RuleUniform), uniformOn, WithVoice(unevenVoice())); len(got) != 0 {
		t.Errorf("uniform = %v, want none under 200 words", got)
	}
}

func TestUniformReportsATextMoreEvenThanItsAuthor(t *testing.T) {
	got := JudgeText(evenDescription, KindChangeDescription, WithOnly(RuleUniform), uniformOn, WithVoice(unevenVoice()))

	if len(got) != 1 || !strings.Contains(got[0].Message, "items of one length") ||
		!strings.Contains(got[0].Message, "items on one opener") {
		t.Errorf("uniform = %v, want one finding naming the item length and opener", got)
	}
}

func TestUniformLeavesATextAsUnevenAsItsAuthor(t *testing.T) {
	if got := JudgeText(unevenDescription, KindChangeDescription, WithOnly(RuleUniform), uniformOn, WithVoice(unevenVoice())); len(got) != 0 {
		t.Errorf("uniform = %v, want none", got)
	}
}

func TestUniformLeavesAnAuthorWhoWritesEvenly(t *testing.T) {
	v := &Voice{Schema: VoiceSchema, Kinds: map[Kind]VoiceStats{
		KindChangeDescription: {Ranges: map[Feature]Range{
			"item_words_sd":        {P5: 0, P95: 2, Windows: 9},
			"item_opener_share":    {P5: 0.8, P95: 1, Windows: 9},
			"opener_class_entropy": {P5: 0, P95: 1, Windows: 9},
		}},
	}}

	if got := JudgeText(evenDescription, KindChangeDescription, WithOnly(RuleUniform), uniformOn, WithVoice(v)); len(got) != 0 {
		t.Errorf("uniform = %v, want none for an author who writes this way", got)
	}
}

func TestUniformAbstainsWithoutAVoice(t *testing.T) {
	if got := JudgeText(evenDescription, KindChangeDescription, WithOnly(RuleUniform), uniformOn); len(got) != 0 {
		t.Errorf("uniform = %v, want none with no voice", got)
	}
}

func TestUniformNeedsTwoMeasuresPastTheRange(t *testing.T) {
	v := unevenVoice()
	r := v.Kinds[KindChangeDescription].Ranges
	delete(r, "item_opener_share")
	delete(r, "opener_class_entropy")

	if got := JudgeText(evenDescription, KindChangeDescription, WithOnly(RuleUniform), uniformOn, WithVoice(v)); len(got) != 0 {
		t.Errorf("uniform = %v, want none on one measure alone", got)
	}
}
