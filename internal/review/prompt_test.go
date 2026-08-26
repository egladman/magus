package review

import (
	"testing"

	"github.com/egladman/magus/internal/prompt"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

func promptFor(t *testing.T, rev types.Diff, overlap []types.BranchChange) string {
	t.Helper()
	return Prompt(PromptInput{
		Changeset: rev,
		Origin:    types.ReviewOrigin{Branch: "feat/x"},
		Overlap:   overlap,
		Variant:   prompt.Short,
	})
}

// TestPromptAsksForFindingsNotProse pins the instruction the whole feature exists for. A prompt
// that lets a model draft the review turns a colleague's review into generated text, which is the
// thing this was built to prevent - and it would be a silent regression, since the output would
// still look like a perfectly good prompt.
func TestPromptAsksForFindingsNotProse(t *testing.T) {
	out := promptFor(t, types.Diff{Base: "main"}, nil)

	assert.Contains(t, out, "I will write the actual review comments myself")
	assert.Contains(t, out, "Do not draft review prose")
}

// TestPromptNamesSkillsRatherThanRestatingThem is the anti-duplication guard. magus already ships
// the durable half of a review briefing as installed, stamped, doctor-checked skills in two
// lengths. Restating any of it here would be a second definition free to drift from the installed
// one, and it would spend the reader's context on what their tools already loaded.
func TestPromptNamesSkillsRatherThanRestatingThem(t *testing.T) {
	out := promptFor(t, types.Diff{Base: "main"}, nil)

	assert.Contains(t, out, SkillQuery.String())
	assert.Contains(t, out, SkillArchitecture.String())
	// For an empty changeset the whole prompt is fixed scaffolding. A skill body inlined here
	// would be several thousand bytes on its own, so this bound is what a regression trips.
	assert.Less(t, len(out), 1500,
		"the prompt is a briefing, not a payload: skills are named, never restated")
}

// TestPromptNamesNoAgentHost. magus must not encode agent-host specifics, and a prompt is exactly
// where one would sneak in: naming a particular host's conventions file reads as helpful and is
// wrong for every reader using a different host. The repo-wide convention test enforces this over
// Go source; this states it about the rendered output, which is what actually reaches a reader.
func TestPromptNamesNoAgentHost(t *testing.T) {
	out := promptFor(t, types.Diff{Base: "main"}, nil)

	for _, host := range []string{"CLAUDE.md", "AGENTS.md", "Claude", "Cursor", "Copilot"} {
		assert.NotContains(t, out, host)
	}
}

// TestPromptReportsOnlyTheOverlappingPaths is the fix for what the first real run showed: a
// branch's full path list is its entire diff, and printing it made the prompt forty times the size
// of the answer it carried. Only the intersection is a fact about the change in front of the
// reader.
func TestPromptReportsOnlyTheOverlappingPaths(t *testing.T) {
	rev := types.Diff{Base: "main", Files: []types.DiffFile{{Path: "a.go"}}}
	overlap := []types.BranchChange{
		{Ref: "origin/other", Paths: []string{"a.go", "unrelated.go", "also-unrelated.go"}},
		{Ref: "origin/elsewhere", Paths: []string{"nothing-of-mine.go"}},
	}

	out := promptFor(t, rev, overlap)

	assert.Contains(t, out, "origin/other")
	assert.NotContains(t, out, "unrelated.go", "a path outside this changeset is not the reader's problem")
	assert.NotContains(t, out, "origin/elsewhere", "a branch sharing nothing is not a collision")
}

// TestPromptOmitsSectionsWithNothingInThem. An empty section is a heading over silence, and
// silence under "Other branches changing these same files" reads as "nobody else is touching
// this" - a claim nothing checked. The builder enforces it; this states it about the document.
func TestPromptOmitsSectionsWithNothingInThem(t *testing.T) {
	rev := types.Diff{Base: "main", Files: []types.DiffFile{{Path: "a.go"}}}

	out := promptFor(t, rev, []types.BranchChange{{Ref: "origin/other", Paths: []string{"b.go"}}})

	assert.NotContains(t, out, "Other branches changing")
	assert.NotContains(t, out, "could not measure", "a changeset with no caveats has no caveats section")
}

// TestPromptCarriesWhatCouldNotBeMeasured: magus's own caveats are the difference between
// "nothing depends on this" and "nobody looked". Dropping them invites the model to read silence
// as a clean bill of health, which is the failure mode every annotation in the report guards.
func TestPromptCarriesWhatCouldNotBeMeasured(t *testing.T) {
	rev := types.Diff{Base: "main", Notes: []string{"no symbol index loaded"}}

	out := promptFor(t, rev, nil)

	assert.Contains(t, out, "no symbol index loaded")
	assert.Contains(t, out, "Do not read any of these as evidence that there is nothing there")
}

// TestPromptSaysHowMuchItLeftOut. The changeset is ranked, so a cap takes the tail rather than an
// arbitrary slice - but a silently truncated list reads as a complete one, and a reader who
// believes they have seen every file is worse off than one who knows they have not.
func TestPromptSaysHowMuchItLeftOut(t *testing.T) {
	rev := types.Diff{Base: "main"}
	for range PromptFileLimit + 7 {
		rev.Files = append(rev.Files, types.DiffFile{Path: "f", Role: "source"})
	}

	out := promptFor(t, rev, nil)

	assert.Contains(t, out, "and 7 more")
}

// TestPromptOmitsUnmeasuredCoverage keeps nil coverage from rendering as 0%. "Nobody ran the
// tests" and "this code is untested" are different claims and only one of them accuses.
func TestPromptOmitsUnmeasuredCoverage(t *testing.T) {
	rev := types.Diff{Base: "main", Files: []types.DiffFile{{Path: "a.go", Role: "source"}}}

	out := promptFor(t, rev, nil)

	assert.NotContains(t, out, "0% covered")
}

// TestLongPromptAddsRationaleAndNothingElse. The two forms are the same document at two lengths:
// the long one explains its instructions, it does not carry different ones. A short form that
// dropped an instruction would be a different prompt rather than a shorter one.
func TestLongPromptAddsRationaleAndNothingElse(t *testing.T) {
	rev := types.Diff{Base: "main", Files: []types.DiffFile{{Path: "a.go", Role: "source"}}}
	in := PromptInput{Changeset: rev, Origin: types.ReviewOrigin{Branch: "feat/x"}}

	in.Variant = prompt.Short
	short := Prompt(in)
	in.Variant = prompt.Long
	long := Prompt(in)

	assert.Greater(t, len(long), len(short))
	assert.NotContains(t, short, "a text search is a guess")
	assert.Contains(t, long, "a text search is a guess")
	for _, instruction := range []string{
		"Do not draft review prose",
		"Read in this order",
		SkillQuery.String(),
		"look for the test that PINS",
	} {
		assert.Contains(t, short, instruction, "the short form dropped an instruction, not a rationale")
		assert.Contains(t, long, instruction)
	}
}
