package prompt

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestSectionWithOnlyANoteDoesNotRender is the invariant the package exists for. A heading over
// nothing reads as "we looked and found nothing", and the caller wrote the section
// unconditionally precisely so it would not have to decide.
func TestSectionWithOnlyANoteDoesNotRender(t *testing.T) {
	out := New("Title", Short).
		Section("Collisions").Note("As of the last fetch.").
		String()

	assert.NotContains(t, out, "Collisions")
	assert.NotContains(t, out, "As of the last fetch")
}

// TestSectionRendersItsNoteOnceItHasContent: the note is not dead weight, it is the sentence that
// tells a reader how to weigh what follows - so it appears exactly when there is something to
// weigh.
func TestSectionRendersItsNoteOnceItHasContent(t *testing.T) {
	out := New("Title", Short).
		Section("Collisions").Note("As of the last fetch.").Bullet("origin/other").
		String()

	assert.Contains(t, out, "## Collisions")
	assert.Contains(t, out, "As of the last fetch.")
	assert.Contains(t, out, "- origin/other")
}

// TestFieldOmitsAnEmptyValue. An unmeasured value has no honest rendering: blank reads as a
// measurement that came back empty, and a zero accuses. Leaving the line out says the only true
// thing.
func TestFieldOmitsAnEmptyValue(t *testing.T) {
	out := New("Title", Short).
		Section("What this is").Field("branch", "").Field("base", "main").
		String()

	assert.NotContains(t, out, "branch")
	assert.Contains(t, out, "- base: main")
}

// TestFieldAloneIsEnoughToRenderASection guards the interaction between the two rules above: a
// section whose every field was empty must vanish, and one with a single field must not.
func TestFieldAloneIsEnoughToRenderASection(t *testing.T) {
	assert.NotContains(t,
		New("T", Short).Section("Facts").Note("n").Field("a", "").String(), "Facts")
	assert.Contains(t,
		New("T", Short).Section("Facts").Note("n").Field("a", "b").String(), "Facts")
}

// TestItemsAlwaysStatesWhatItDropped. A truncated list a reader believes is complete is worse
// than no list, because they stop looking.
func TestItemsAlwaysStatesWhatItDropped(t *testing.T) {
	out := New("Title", Short).
		Section("Files").Items([]string{"a", "b", "c", "d"}, 2, "Ask magus for the rest.").
		String()

	assert.Contains(t, out, "- a")
	assert.Contains(t, out, "- b")
	assert.NotContains(t, out, "- c")
	assert.Contains(t, out, "... and 2 more. Ask magus for the rest.")
}

// TestItemsWithoutALimitKeepsEverything, and says nothing about a remainder that does not exist.
func TestItemsWithoutALimitKeepsEverything(t *testing.T) {
	out := New("Title", Short).
		Section("Files").Items([]string{"a", "b"}, 0, "unused").
		String()

	assert.Contains(t, out, "- a")
	assert.Contains(t, out, "- b")
	assert.NotContains(t, out, "more")
}

// TestItemsAtExactlyTheLimitClaimsNoRemainder pins the boundary: an off-by-one here invents a
// "... and 0 more" line, which is the same lie in the other direction.
func TestItemsAtExactlyTheLimitClaimsNoRemainder(t *testing.T) {
	out := New("Title", Short).
		Section("Files").Items([]string{"a", "b"}, 2, "unused").
		String()

	assert.NotContains(t, out, "more")
}

// TestLeadAlwaysRenders: it is the instruction the prompt exists to give, so it does not depend
// on what was found. A prompt whose framing varies with its findings is a different prompt each
// time it runs.
func TestLeadAlwaysRenders(t *testing.T) {
	out := New("Review this", Short).Lead("Find what is worth commenting on.").String()

	assert.Contains(t, out, "# Review this")
	assert.Contains(t, out, "Find what is worth commenting on.")
}

// TestBecauseAppearsOnlyInTheLongForm. The short form is the instruction; the long form is the
// instruction plus why it is the instruction. A reader who already trusts the tool should not pay
// context for the argument that would have convinced them.
func TestBecauseAppearsOnlyInTheLongForm(t *testing.T) {
	build := func(v Variant) string {
		return New("T", v).
			Section("How to look").
			Bullet("ask the graph, do not grep").
			Because("a graph answer is checked against declared sources; a grep hit is a guess").
			String()
	}

	assert.NotContains(t, build(Short), "a guess")
	assert.Contains(t, build(Long), "a guess")
	// The instruction itself survives both, which is what makes them the same document.
	assert.Contains(t, build(Short), "ask the graph")
	assert.Contains(t, build(Long), "ask the graph")
}

// TestBecauseAloneCannotConjureASection: rationale explains facts, so a section that gathered no
// facts has nothing to explain and must stay gone even in the long form.
func TestBecauseAloneCannotConjureASection(t *testing.T) {
	out := New("T", Long).Section("Collisions").Because("branches collide").String()

	assert.NotContains(t, out, "Collisions")
}

// TestOnlySwapsWordingPerVariant is the builder's form of the skills' short-or-long swap: one
// fact, worded for the length, never both at once.
func TestOnlySwapsWordingPerVariant(t *testing.T) {
	build := func(v Variant) string {
		return New("T", v).
			Section("Scope").
			Only(Short, "3 projects rebuild.").
			Only(Long, "3 projects rebuild, which is the reverse closure rather than the edited set.").
			String()
	}

	assert.Contains(t, build(Short), "3 projects rebuild.")
	assert.NotContains(t, build(Short), "reverse closure")
	assert.Contains(t, build(Long), "reverse closure")
}

// TestProseAndListsAreSeparated. Markdown needs a blank line between a paragraph and a list, and
// a caller writing one chain should not have to remember where those go. Without it the list
// renders glued to the sentence above it.
func TestProseAndListsAreSeparated(t *testing.T) {
	out := New("T", Short).
		Section("S").Note("Read these:").Bullet("a").Text("Then this.").Bullet("b").
		String()

	assert.Contains(t, out, "Read these:\n\n- a\n")
	assert.Contains(t, out, "- a\n\nThen this.\n\n- b\n")
}

// TestConsecutiveBulletsAreNotSeparated: the blank line belongs BETWEEN prose and a list, not
// inside the list, where it would render as several one-item lists.
func TestConsecutiveBulletsAreNotSeparated(t *testing.T) {
	out := New("T", Short).Section("S").Bullet("a").Bullet("b").Bullet("c").String()

	assert.Contains(t, out, "- a\n- b\n- c\n")
}

// TestSectionsChainInOrder. The chain is the readable part of the API, and it has to produce the
// order it was written in - a caller reading top to bottom is describing the document.
func TestSectionsChainInOrder(t *testing.T) {
	out := New("T", Short).
		Section("First").Bullet("1").
		Section("Second").Bullet("2").
		Section("Third").Bullet("3").
		String()

	assert.Less(t, strings.Index(out, "## First"), strings.Index(out, "## Second"))
	assert.Less(t, strings.Index(out, "## Second"), strings.Index(out, "## Third"))
}
