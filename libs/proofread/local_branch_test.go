package proofread

import (
	"slices"
	"testing"
)

func localBranchMatches(t *testing.T, text string, kind Kind, names ...string) []string {
	t.Helper()
	return matches(JudgeText(text, kind, houseOn, WithLocalBranches(names...)), RuleLocalBranch)
}

func assertBranchMatches(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("local-branch findings:\n got %q\nwant %q", got, want)
	}
}

func TestLocalBranchReportsAPushedNothingBranchInADescription(t *testing.T) {
	got := localBranchMatches(t, "Fix the cache.\n\n- Squashed from `feat/great-pasteur-51bbcb` by content.\n",
		KindChangeDescription, "feat/great-pasteur-51bbcb")
	assertBranchMatches(t, got, []string{"3:feat/great-pasteur-51bbcb"})
}

func TestLocalBranchReportsAHyphenatedNameOutsideBackticks(t *testing.T) {
	got := localBranchMatches(t, "Fix the cache.\n\n- It came from latex-spell.\n", KindChangeDescription, "latex-spell")
	assertBranchMatches(t, got, []string{"3:latex-spell"})
}

func TestLocalBranchPassesAOneWordNameInProse(t *testing.T) {
	got := localBranchMatches(t, "Fix the cache.\n\n- The index stores each embed once.\n", KindChangeDescription, "embed")
	assertBranchMatches(t, got, nil)
}

func TestLocalBranchReportsAOneWordNameInBackticks(t *testing.T) {
	got := localBranchMatches(t, "Fix the cache.\n\n- Squashed from `embed`.\n", KindChangeDescription, "embed")
	assertBranchMatches(t, got, []string{"3:embed"})
}

func TestLocalBranchPassesANameInsideALongerOne(t *testing.T) {
	got := localBranchMatches(t, "Fix the cache.\n\n- Squashed from `embed-index`.\n", KindChangeDescription, "embed")
	assertBranchMatches(t, got, nil)
}

func TestLocalBranchPassesANameInAURL(t *testing.T) {
	got := localBranchMatches(t, "Fix the cache.\n\n- See https://example.com/tree/latex-spell for the rest.\n",
		KindChangeDescription, "latex-spell")
	assertBranchMatches(t, got, nil)
}

func TestLocalBranchJudgesNothingWithoutNames(t *testing.T) {
	got := localBranchMatches(t, "Fix the cache.\n\n- Squashed from `feat/great-pasteur-51bbcb`.\n", KindChangeDescription)
	assertBranchMatches(t, got, nil)
}

func TestLocalBranchLeavesAReplyToThePersonAlone(t *testing.T) {
	got := localBranchMatches(t, "The work is on `latex-spell`.\n", KindAgentReply, "latex-spell")
	assertBranchMatches(t, got, nil)
}
