package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// diffReach builds the pointer DiffFile.Reach wants. Reach is a pointer precisely so an
// unindexed workspace cannot serve `reach: 0` on every file, so a fixture has to opt in.
func diffReach(n int) *int { return &n }

// TestDiffFileFactsSaysWhatWasMeasured pins the one-claim-per-line contract, and with it
// the distinction the nil pointers exist to preserve: an unmeasured field prints NOTHING
// rather than a zero. A "0 files reference" line reads as "nothing depends on this", which
// is the opposite of "nobody looked".
func TestDiffFileFactsSaysWhatWasMeasured(t *testing.T) {
	t.Run("nothing measured yields no facts", func(t *testing.T) {
		assert.Empty(t, diffFileFacts(types.DiffFile{Path: "a.go", Surface: types.DiffSurfaceUnknown}))
	})

	t.Run("zero reach is not a fact", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{Path: "a.go", Reach: diffReach(0)})
		assert.Empty(t, facts)
	})

	t.Run("singular nouns agree with their counts", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{
			Path:  "a.go",
			Reach: diffReach(1),
			Churn: &types.DiffChurn{Commits: 1},
		})
		assert.Contains(t, facts, "1 file reference its widest changed symbol")
		assert.Contains(t, facts, "changed in 1 commit")
	})

	t.Run("public surface names the exports when it knows them", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{
			Path:    "a.go",
			Surface: types.DiffSurfacePublic,
			Symbols: []types.DiffSymbol{{Label: "Open", ModuleAPI: true}, {Label: "Close", ModuleAPI: true}},
		})
		require.NotEmpty(t, facts)
		assert.Equal(t, "PUBLIC SURFACE: exports Open, Close", facts[0])
	})

	t.Run("public surface falls back to the consuming projects", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{
			Path:    "a.go",
			Surface: types.DiffSurfacePublic,
			Symbols: []types.DiffSymbol{
				{Label: "Open", ExternalProjects: []string{"web", "api"}},
				{Label: "Close", ExternalProjects: []string{"web"}},
			},
		})
		require.NotEmpty(t, facts)
		assert.Equal(t, "PUBLIC SURFACE: used by web, api", facts[0])
	})

	t.Run("public surface with no evidence still says public", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{Path: "a.go", Surface: types.DiffSurfacePublic})
		require.NotEmpty(t, facts)
		assert.Equal(t, "PUBLIC SURFACE", facts[0])
	})

	t.Run("a hot rising file says so, and an unranked one keeps its commit count", func(t *testing.T) {
		hot := diffFileFacts(types.DiffFile{
			Path:    "a.go",
			Project: "core",
			Churn:   &types.DiffChurn{Commits: 40, Authors: 6, Rank: 3, ProjectTrend: 12},
		})
		assert.Contains(t, hot, "changed in 40 commits by 6 people, hotspot #3 AND RISING - worth asking why it keeps changing")
		assert.Contains(t, hot, "in core")

		cold := diffFileFacts(types.DiffFile{
			Path:  "a.go",
			Churn: &types.DiffChurn{Commits: 40, Rank: types.NotableRankCutoff + 1, ProjectTrend: 12},
		})
		assert.Contains(t, cold, "changed in 40 commits")
		for _, f := range cold {
			assert.NotContains(t, f, "hotspot")
			assert.NotContains(t, f, "RISING")
		}
	})

	t.Run("coverage rounds and no history is stated plainly", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{
			Path:      "a.go",
			Coverage:  &types.ImpactCoverage{Ratio: 0.675, Covered: 27, Total: 40},
			NoHistory: true,
		})
		assert.Contains(t, facts, "68% covered")
		assert.Contains(t, facts, "NO HISTORY - nothing has exercised this yet")
	})

	t.Run("zero total coverage is not zero percent", func(t *testing.T) {
		facts := diffFileFacts(types.DiffFile{Path: "a.go", Coverage: &types.ImpactCoverage{}})
		assert.Empty(t, facts)
	})
}

// TestCapSliceReportsTheRemainder guards the property the helper exists for: a list that
// stops without saying so reads as the whole answer.
func TestCapSliceReportsTheRemainder(t *testing.T) {
	assert.Equal(t, []string{"a", "b"}, capSlice([]string{"a", "b"}, 4))
	assert.Equal(t, []string{"a", "b", "and 2 more"}, capSlice([]string{"a", "b", "c", "d"}, 2))

	// The cap must not write through the caller's backing array; the full slice is still
	// needed by -o json, which renders the same DiffFile.
	full := []string{"a", "b", "c", "d"}
	_ = capSlice(full, 2)
	assert.Equal(t, []string{"a", "b", "c", "d"}, full)
}

func TestChangedPathsFromPatchReadsTheHeaders(t *testing.T) {
	patch := strings.Join([]string{
		"diff --git a/cmd/magus/diff.go b/cmd/magus/diff.go",
		"index 111..222 100644",
		"--- a/cmd/magus/diff.go",
		"+++ b/cmd/magus/diff.go",
		"+// a change",
		"diff --git a/docs/graph.json b/docs/graph.json",
		"diff --git a/cmd/magus/diff.go b/cmd/magus/diff.go",
		"diff --git malformed-without-b-prefix",
		"",
	}, "\n")

	assert.Equal(t, []string{"cmd/magus/diff.go", "docs/graph.json"}, changedPathsFromPatch(patch))
	assert.Empty(t, changedPathsFromPatch(""))
}

// TestDiffInputFromArgs pins the refusal that matters most: a git ref typed by someone
// arriving from `git diff <ref>` must not be swallowed into a plausible listing of their
// own uncommitted edits under exit 0.
func TestDiffInputFromArgs(t *testing.T) {
	dir := t.TempDir()
	patch := filepath.Join(dir, "change.patch")
	require.NoError(t, os.WriteFile(patch, []byte("diff --git a/x b/x\n"), 0o644))

	t.Run("no argument reads the working tree", func(t *testing.T) {
		in, err := diffInputFromArgs(nil)
		require.NoError(t, err)
		assert.Equal(t, inputWorkingTree, in.kind)
		assert.Equal(t, "the working tree", in.label)
	})

	t.Run("dash reads stdin", func(t *testing.T) {
		in, err := diffInputFromArgs([]string{"-"})
		require.NoError(t, err)
		assert.Equal(t, inputStdin, in.kind)
	})

	t.Run("a readable file is a patch", func(t *testing.T) {
		in, err := diffInputFromArgs([]string{patch})
		require.NoError(t, err)
		assert.Equal(t, inputFile, in.kind)
		assert.Equal(t, patch, in.path)
		assert.Contains(t, in.label, patch)
	})

	t.Run("a directory is refused", func(t *testing.T) {
		_, err := diffInputFromArgs([]string{dir})
		require.Error(t, err)
		assert.IsType(t, errUsage{}, err)
	})

	t.Run("a ref is refused and names the command that works", func(t *testing.T) {
		_, err := diffInputFromArgs([]string{"HEAD~1"})
		require.Error(t, err)
		assert.IsType(t, errUsage{}, err)
		assert.Contains(t, err.Error(), "git diff HEAD~1")
		assert.Contains(t, err.Error(), "magus diff -")
	})

	t.Run("two positionals are refused", func(t *testing.T) {
		_, err := diffInputFromArgs([]string{"a", "b"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at most one patch argument")
	})
}

// TestPrintDiffTextOrdersTheEvidence covers the whole text rendering: the counts headline,
// the unranked caveat's placement BEFORE the list, the generated fold in both states, and
// the agent trail.
func TestPrintDiffTextOrdersTheEvidence(t *testing.T) {
	rev := types.Diff{
		Base: "working",
		Files: []types.DiffFile{
			{
				Path:    "core/engine.go",
				Project: "core",
				Role:    types.DiffRoleSource,
				Surface: types.DiffSurfacePublic,
				Symbols: []types.DiffSymbol{{Label: "Run", ModuleAPI: true, FileCount: 12}},
				Reach:   diffReach(12),
				Touches: []types.DiffTouch{{
					Host:       "claude-code",
					Read:       []string{"core/engine.go", "core/plan.go"},
					Transcript: "/tmp/session.jsonl",
				}},
			},
			{Path: "web/app.ts", Role: types.DiffRoleSource, Touches: []types.DiffTouch{{}}},
			{Path: "MAGUS.md", Role: types.DiffRoleOutput},
		},
		SeedProjects:     []string{"core"},
		AffectedProjects: []types.ImpactProject{{}, {}},
		Notes:            []string{"no coverage profile was loaded"},
	}

	t.Run("folded", func(t *testing.T) {
		out := captureStdout(t, func() {
			require.NoError(t, printDiffText(rev, false, func(p string) string { return p }, nil))
		})

		assert.Contains(t, out, "2 files to read, 1 generated folded; 1 projects edited, 2 projects rebuild")
		assert.Contains(t, out, "12 files reference its widest changed symbol")
		assert.Contains(t, out, "PUBLIC SURFACE: exports Run")
		assert.Contains(t, out, "written by claude-code, after reading core/engine.go, core/plan.go")
		assert.Contains(t, out, "transcript: /tmp/session.jsonl")
		// A touch with no host still attributes the write rather than printing a blank.
		assert.Contains(t, out, "written by an agent")
		assert.Contains(t, out, "1 generated files folded")
		assert.Contains(t, out, "--generated")
		assert.Contains(t, out, "note: no coverage profile was loaded")
		// The folded file itself is never listed, which is the whole affordance.
		assert.NotContains(t, out, "  MAGUS.md")
	})

	t.Run("shown", func(t *testing.T) {
		out := captureStdout(t, func() {
			require.NoError(t, printDiffText(rev, true, func(p string) string { return "<" + p + ">" }, nil))
		})

		assert.Contains(t, out, "2 files to read, 1 generated shown")
		assert.Contains(t, out, "generated (1) - a target rewrites these")
		assert.Contains(t, out, "<MAGUS.md>")
		assert.Contains(t, out, "magus describe file <path>")
	})

	t.Run("unranked says so before the list", func(t *testing.T) {
		unranked := types.Diff{Files: []types.DiffFile{
			{Path: "b.go", Role: types.DiffRoleSource},
			{Path: "a.go", Role: types.DiffRoleSource},
		}}
		out := captureStdout(t, func() {
			require.NoError(t, printDiffText(unranked, false, func(p string) string { return p }, nil))
		})

		assert.Contains(t, out, "UNRANKED: no symbol index")
		assert.Less(t, strings.Index(out, "UNRANKED"), strings.Index(out, "b.go"),
			"the caveat must arrive before the reader has read the first entry as the most dangerous one")
	})

	t.Run("a single unranked file gets no caveat", func(t *testing.T) {
		out := captureStdout(t, func() {
			one := types.Diff{Files: []types.DiffFile{{Path: "a.go", Role: types.DiffRoleSource}}}
			require.NoError(t, printDiffText(one, false, func(p string) string { return p }, nil))
		})
		assert.NotContains(t, out, "UNRANKED")
		assert.Contains(t, out, "1 files to read")
	})
}

// TestPathLinkerLeavesPipedOutputBare pins the property that keeps a captured log free of
// escape sequences: the hyperlink gate refuses a non-terminal, and a test's stdout is a pipe.
func TestPathLinkerLeavesPipedOutputBare(t *testing.T) {
	link := pathLinker(t.TempDir())
	assert.Equal(t, "cmd/magus/diff.go", link("cmd/magus/diff.go"))
	assert.Equal(t, "/abs/path.go", link("/abs/path.go"))
}

// TestDiffTouchesWithoutATrail covers the common case: no guard hook is wired, so the
// replay is empty and the map is nil rather than an empty map that renders as a column.
func TestDiffTouchesWithoutATrail(t *testing.T) {
	assert.Nil(t, diffTouches(t.TempDir(), t.TempDir(), []string{"a.go"}))
}
