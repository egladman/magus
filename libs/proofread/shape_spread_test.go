package proofread

import (
	"slices"
	"testing"
)

const evenList = `Steps taken:

- Updated the parser to read the new header format.
- Updated the writer to emit the new header format.
- Updated the loader to accept the new header format.
- Updated the tests to cover the new header format.
`

const unevenList = `Steps taken:

- Rewrote the parser.
- The writer kept its old header until the 2.3 release, so readers built before then still open the files it writes.
- Tests.
- Moved the loader into its own package after the cache split.
`

func TestMeasureShapeItemSpreadIsZeroForEqualItems(t *testing.T) {
	got := MeasureShape(evenList, KindChangeDescription)

	if got.ItemWordsStdDev != 0 || got.ItemOpenerShare != 1 {
		t.Errorf("item spread = sd %v, opener share %v, want 0 and 1", got.ItemWordsStdDev, got.ItemOpenerShare)
	}
}

func TestMeasureShapeItemSpreadGrowsWithUnevenItems(t *testing.T) {
	got := MeasureShape(unevenList, KindChangeDescription)

	if got.ItemWordsStdDev < 5 || got.ItemOpenerShare != 0.25 {
		t.Errorf("item spread = sd %v, opener share %v, want over 5 and 0.25", got.ItemWordsStdDev, got.ItemOpenerShare)
	}
}

func TestMeasureShapeParagraphSpreadCountsTheLeadAndItems(t *testing.T) {
	got := MeasureShape(evenList, KindChangeDescription)

	// The two-word lead against four nine-word items.
	if got.ParagraphWordsStdDev != 2.8 {
		t.Errorf("ParagraphWordsStdDev = %v, want 2.8", got.ParagraphWordsStdDev)
	}
}

func TestMeasureShapeSentenceOpenerShareCountsTheTopOpener(t *testing.T) {
	text := "We read it. We wrote it. We ran it. They checked it. It passed."

	if got := MeasureShape(text, KindReference).SentenceOpenerShare; got != 0.6 {
		t.Errorf("SentenceOpenerShare = %v, want 0.6", got)
	}
}

func TestMeasureShapeOpenerClassEntropyIsZeroForOneClass(t *testing.T) {
	text := "Read the file. Write the file. Check the file. Close the file. Delete the file."

	if got := MeasureShape(text, KindReference).OpenerClassEntropy; got != 0 {
		t.Errorf("OpenerClassEntropy = %v, want 0", got)
	}
}

func TestMeasureShapeOpenerClassEntropyGrowsWithMixedOpeners(t *testing.T) {
	text := "Read the file. The file was long. Slowly it loaded. Because it was cached, it was fast. We closed it."

	if got := MeasureShape(text, KindReference).OpenerClassEntropy; got < 1.5 {
		t.Errorf("OpenerClassEntropy = %v, want 1.5 bits or more", got)
	}
}

func TestMeasureShapeSubordinateShareCountsSentencesWithASubordinator(t *testing.T) {
	text := "It failed because the cache was cold. It passed. It ran. It stopped although nothing changed. It ended."

	if got := MeasureShape(text, KindReference).SubordinateShare; got != 0.4 {
		t.Errorf("SubordinateShare = %v, want 0.4", got)
	}
}

func TestMeasureShapeWithholdsItemSpreadUnderThreeItems(t *testing.T) {
	got := MeasureShape("- One item here.\n- Two items here.\n", KindChangeDescription)

	if !slices.Contains(got.Withheld, "item_words_sd") || !slices.Contains(got.Withheld, "item_opener_share") {
		t.Errorf("Withheld = %v, want the item spread withheld", got.Withheld)
	}
}
