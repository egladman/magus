package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/types"
)

func gap(project string) types.KnowledgeSymbolGap {
	return types.KnowledgeSymbolGap{Project: types.NewProjectRef(project, ""), State: types.SymbolIndexNotBuilt}
}

func renderVerdict(ans types.KnowledgeAnswer, hint string) string {
	var b bytes.Buffer
	printVerdict(&b, ans, hint)
	return b.String()
}

// A found verdict prints nothing: the rows above it already answered the question, and a
// line saying so on every successful lookup is noise.
func TestPrintVerdictFoundIsSilent(t *testing.T) {
	assert.Empty(t, renderVerdict(types.KnowledgeAnswer{Verdict: types.VerdictFound}, ""))
}

// An absent verdict asserts the absence positively. It must not suggest building an
// index: nothing is missing, so the remedy would point at nothing.
func TestPrintVerdictAbsentAssertsAndSuggestsNothing(t *testing.T) {
	got := renderVerdict(types.ClassifyAnswer(false, "", nil), "")
	assert.Contains(t, got, "verdict: absent")
	assert.NotContains(t, got, "graph build")
	assert.NotContains(t, got, "outside coverage")
}

// An unknown verdict has to name what was not searched and the command that would fix
// it. A verdict the reader cannot act on is barely better than the empty result it
// replaced.
func TestPrintVerdictUnknownNamesGapsAndRemedy(t *testing.T) {
	got := renderVerdict(types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{gap("libs/api"), gap("docs")}), "")
	assert.Contains(t, got, "verdict: unknown, not absent")
	assert.Contains(t, got, "outside coverage: libs/api (not-indexed), docs (not-indexed)")
	assert.Contains(t, got, "magus graph build")
}

// The not-loaded reason is a different fix: the index may be perfectly fine and this
// lookup simply did not consult it, so the remedy is another command, not a build.
func TestPrintVerdictNotLoadedPointsAtTheOtherVerb(t *testing.T) {
	got := renderVerdict(types.ClassifyAnswer(false, types.ReasonSymbolsNotLoaded, nil), "magus refs Foo")
	assert.Contains(t, got, "searched domain entities only, not code symbols")
	assert.Contains(t, got, "magus refs Foo")
	assert.NotContains(t, got, "graph build", "nothing is missing, so do not send them to build one")
}

// House rule: user-facing output is plain ASCII. An em-dash or a curly quote here would
// reach every terminal that runs an empty lookup.
func TestPrintVerdictIsPlainASCII(t *testing.T) {
	for _, ans := range []types.KnowledgeAnswer{
		types.ClassifyAnswer(false, "", nil),
		types.ClassifyAnswer(false, types.ReasonSymbolsNotLoaded, nil),
		types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{gap("libs/api")}),
		knowledge.Answer("Foo", false, knowledge.Coverage{Seeded: true, Probed: true, IndexOnly: true, Stale: []string{"libs/api"}}),
	} {
		got := renderVerdict(ans, "magus refs Foo")
		for _, r := range got {
			require.Less(t, r, rune(0x80), "non-ASCII %q in %q", r, got)
		}
		assert.NotContains(t, got, "--", "no double dash standing in for an em-dash")
	}
}

// The exit code IS the feature at the process boundary. Both cases used to be 2, so a
// script could not tell "this does not exist" from "I could not look".
func TestExitForVerdictSplitsUnknownFromAbsent(t *testing.T) {
	assert.Equal(t, errSilent{exitCode: 1}, exitForVerdict(types.ClassifyAnswer(false, types.ReasonSymbolsNotLoaded, nil).Verdict),
		"unknown is exit 1: invoked correctly, the work could not be done")
	assert.Equal(t, errSilent{exitCode: 2}, exitForVerdict(types.ClassifyAnswer(false, "", nil).Verdict),
		"absent is exit 2: the request cannot be carried out as stated")
}

// A gap whose index exists but will not decode reads differently from one never built,
// because the fix differs: rebuild versus build.
func TestDescribeGapsRendersDetailOverState(t *testing.T) {
	got := types.DescribeGaps([]types.KnowledgeSymbolGap{
		{Project: types.NewProjectRef("libs/api", ""), State: types.SymbolIndexNotBuilt, Detail: "undecodable"},
		gap("docs"),
	})
	assert.Equal(t, "libs/api (undecodable), docs (not-indexed)", got)
}

// Every line of the block is indented under the verdict, so an empty result reads as one
// finding rather than as several unrelated warnings.
func TestPrintVerdictIndentsUnderTheVerdict(t *testing.T) {
	got := renderVerdict(types.ClassifyAnswer(false, types.ReasonSymbolsNotLoaded, []types.KnowledgeSymbolGap{gap("libs/api")}), "magus refs Foo")
	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	require.Greater(t, len(lines), 1)
	assert.Equal(t, "verdict: unknown, not absent", lines[0])
	for _, l := range lines[1:] {
		assert.True(t, strings.HasPrefix(l, "  "), "continuation line %q must be indented", l)
	}
}

// A probe that could not run must not come back as a verified absence: reporting the
// coverage it failed to establish is the one outcome this verdict exists to prevent.
func TestPrintVerdictCoverageUnknownDoesNotAssertAbsence(t *testing.T) {
	got := renderVerdict(types.ClassifyAnswer(false, types.ReasonCoverageUnknown, nil), "")
	assert.Contains(t, got, "verdict: unknown, not absent")
	assert.Contains(t, got, "could not determine which projects it searched")
	assert.NotContains(t, got, "absent (magus searched everything")
}

// The caveat where it is the whole explanation. `magus refs <real name>` on an index built
// before that definition existed printed a verdict byte-identical to `magus refs <typo>`,
// while the stale-index line showed up only under answers that FOUND something - the one
// case where it did not change what to do.
func TestPrintVerdictIndexStaleNamesTheProjectsAndTheRefresh(t *testing.T) {
	got := renderVerdict(knowledge.Answer("adoptionRun", false, knowledge.Coverage{
		Seeded: true, Probed: true, IndexOnly: true, Stale: []string{"libs/api", "."},
	}), "")
	assert.Contains(t, got, "verdict: unknown, not absent")
	assert.Contains(t, got, "libs/api, .")
	assert.Contains(t, got, "magus graph build")
	assert.NotContains(t, got, "absent (magus searched everything")
}

// The CLI and the MCP tools must reach the same verdict about one graph. Both surfaces
// build a knowledge.Coverage of what they observed and hand it to knowledge.Answer; neither
// derives a reason. The mirror of this assertion lives in internal/handler/mcp.
func TestVerdictDerivationIsSharedWithMCP(t *testing.T) {
	const q = "kind=target nothingmatchesthis"
	// The gate that used to be CLI-only: a kind outside the lazy layer rules it out, so the
	// absence is verified whether or not symbols were loaded.
	assert.Equal(t, types.VerdictAbsent, knowledge.Answer(q, false, knowledge.Coverage{}).Verdict)
	// And the case the CLI used to get wrong on its own: kind=file names a kind those shards
	// hold, so an unseeded lookup may not assert anything.
	assert.Equal(t, types.VerdictUnknown,
		knowledge.Answer("kind=file nothingmatchesthis", false, knowledge.Coverage{Probed: true}).Verdict)
}

// A found result still carries a coverage caveat when one applies, and prints nothing
// when it does not.
func TestPrintVerdictFoundWithGapsStillWarns(t *testing.T) {
	assert.Empty(t, renderVerdict(types.ClassifyAnswer(true, "", nil), ""))
	got := renderVerdict(types.ClassifyAnswer(true, "", []types.KnowledgeSymbolGap{gap("libs/api")}), "")
	assert.Contains(t, got, "outside coverage: libs/api (not-indexed)",
		"a populated list from a half-indexed workspace is as misleading as an empty one")
}
