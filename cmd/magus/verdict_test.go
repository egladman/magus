package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func gap(project string) types.KnowledgeSymbolGap {
	return types.KnowledgeSymbolGap{Project: types.NewProjectRef(project, ""), State: types.SymbolIndexNotBuilt}
}

func renderAnswer(ans types.KnowledgeAnswer, hint string) string {
	var b bytes.Buffer
	printSymbolAnswer(&b, ans, hint)
	return b.String()
}

// A found verdict prints nothing: the rows above it already answered the question, and a
// line saying so on every successful lookup is noise.
func TestPrintSymbolAnswerFoundIsSilent(t *testing.T) {
	assert.Empty(t, renderAnswer(types.KnowledgeAnswer{Verdict: types.VerdictFound}, ""))
}

// An absent verdict asserts the absence positively. It must not suggest building an
// index: nothing is missing, so the remedy would point at nothing.
func TestPrintSymbolAnswerAbsentAssertsAndSuggestsNothing(t *testing.T) {
	got := renderAnswer(types.EmptyAnswer(true, nil), "")
	assert.Contains(t, got, "verdict: absent")
	assert.NotContains(t, got, "graph build")
	assert.NotContains(t, got, "outside coverage")
}

// An unknown verdict has to name what was not searched and the command that would fix
// it. A verdict the reader cannot act on is barely better than the empty result it
// replaced.
func TestPrintSymbolAnswerUnknownNamesGapsAndRemedy(t *testing.T) {
	got := renderAnswer(types.EmptyAnswer(true, []types.KnowledgeSymbolGap{gap("libs/api"), gap("docs")}), "")
	assert.Contains(t, got, "verdict: unknown, not absent")
	assert.Contains(t, got, "outside coverage: libs/api (not-indexed), docs (not-indexed)")
	assert.Contains(t, got, "magus graph build")
}

// The not-loaded reason is a different fix: the index may be perfectly fine and this
// lookup simply did not consult it, so the remedy is another command, not a build.
func TestPrintSymbolAnswerNotLoadedPointsAtTheOtherVerb(t *testing.T) {
	got := renderAnswer(types.EmptyAnswer(false, nil), "magus refs Foo")
	assert.Contains(t, got, "searched domain entities only, not code symbols")
	assert.Contains(t, got, "magus refs Foo")
	assert.NotContains(t, got, "graph build", "nothing is missing, so do not send them to build one")
}

// House rule: user-facing output is plain ASCII. An em-dash or a curly quote here would
// reach every terminal that runs an empty lookup.
func TestPrintSymbolAnswerIsPlainASCII(t *testing.T) {
	for _, ans := range []types.KnowledgeAnswer{
		types.EmptyAnswer(true, nil),
		types.EmptyAnswer(false, nil),
		types.EmptyAnswer(true, []types.KnowledgeSymbolGap{gap("libs/api")}),
	} {
		got := renderAnswer(ans, "magus refs Foo")
		for _, r := range got {
			require.Less(t, r, rune(0x80), "non-ASCII %q in %q", r, got)
		}
		assert.NotContains(t, got, "--", "no double dash standing in for an em-dash")
	}
}

// The exit code IS the feature at the process boundary. Both cases used to be 2, so a
// script could not tell "this does not exist" from "I could not look".
func TestAnswerExitSplitsUnknownFromAbsent(t *testing.T) {
	assert.Equal(t, errSilent{exitCode: 1}, answerExit(types.EmptyAnswer(false, nil)),
		"unknown is exit 1: invoked correctly, the work could not be done")
	assert.Equal(t, errSilent{exitCode: 2}, answerExit(types.EmptyAnswer(true, nil)),
		"absent is exit 2: the request cannot be carried out as stated")
}

// A gap whose index exists but will not decode reads differently from one never built,
// because the fix differs: rebuild versus build.
func TestGapListRendersDetailOverState(t *testing.T) {
	got := gapList([]types.KnowledgeSymbolGap{
		{Project: types.NewProjectRef("libs/api", ""), State: types.SymbolIndexNotBuilt, Detail: "undecodable"},
		gap("docs"),
	})
	assert.Equal(t, "libs/api (undecodable), docs (not-indexed)", got)
}

// Every line of the block is indented under the verdict, so an empty result reads as one
// finding rather than as several unrelated warnings.
func TestPrintSymbolAnswerIndentsUnderTheVerdict(t *testing.T) {
	got := renderAnswer(types.EmptyAnswer(false, []types.KnowledgeSymbolGap{gap("libs/api")}), "magus refs Foo")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	require.Greater(t, len(lines), 1)
	assert.Equal(t, "verdict: unknown, not absent", lines[0])
	for _, l := range lines[1:] {
		assert.True(t, strings.HasPrefix(l, "  "), "continuation line %q must be indented", l)
	}
}
