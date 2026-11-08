package proofread

import (
	"slices"
	"testing"
)

const laterBody = "fix(cache): keep the key stable\nThe key drifted between runs.\n\n- The key sorts its inputs.\n\n" +
	"## Later\n\n- `magus diff` will read the new key.\n\n## Considerations\n\n- It will cost a copy.\n"

func TestFutureSectionsLetLaterSpeakOfLaterWork(t *testing.T) {
	got := matches(JudgeText(laterBody, KindChangeDescription, houseOn, WithFutureSections("later")), RuleTense)
	if !slices.Equal(got, []string{"12:will"}) {
		t.Errorf("tense findings = %q, want only the Considerations line", got)
	}
}

func TestWithoutFutureSectionsEveryFutureIsReported(t *testing.T) {
	got := matches(JudgeText(laterBody, KindChangeDescription, houseOn), RuleTense)
	if !slices.Equal(got, []string{"8:will", "12:will"}) {
		t.Errorf("tense findings = %q, want both lines", got)
	}
}

func TestAFutureSectionEndsAtTheNextHeadingOfItsLevel(t *testing.T) {
	got := futureLines([]string{"## Later", "- a", "### Detail", "- b", "## Next", "- c"}, []string{"Later"})
	if !got[2] || !got[3] || !got[4] || got[5] || got[6] {
		t.Errorf("futureLines = %v, want lines 2 to 4", got)
	}
}
