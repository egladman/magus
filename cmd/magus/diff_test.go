package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/diff"
	"github.com/egladman/magus/types"
)

// TestDiffTUIRefusalMatrix pins every way --tui can be asked for something it cannot do.
//
// Each of these has a plausible reading that would "work" and produce a lie: a viewport over
// a patch file coordinating a session about a working tree nobody is editing, two loops
// fighting for one terminal, or -o json answered with a picture. A refusal that names the
// conflicting flag is the whole point, so the message is asserted, not just the failure.
func TestDiffTUIRefusalMatrix(t *testing.T) {
	workingTree := diffInput{kind: inputWorkingTree, label: "the working tree"}
	stdin := diffInput{kind: inputStdin, label: "a patch on stdin"}
	patchFile := diffInput{kind: inputFile, path: "x.patch", label: "the patch in x.patch"}

	tests := []struct {
		name        string
		flags       gen.DiffFlags
		src         diffInput
		format      Format
		interactive bool
		wantMsg     string
		wantStderr  string
	}{
		{
			name:   "without --tui nothing here is refused",
			flags:  gen.DiffFlags{Watch: true},
			src:    stdin,
			format: outputJSON,
		},
		{
			name:        "--tui at a terminal over the working tree runs",
			flags:       gen.DiffFlags{Tui: true},
			src:         workingTree,
			format:      outputText,
			interactive: true,
		},
		{
			name:        "--generated composes: it only sets the initial fold",
			flags:       gen.DiffFlags{Tui: true, Generated: true},
			src:         workingTree,
			format:      outputText,
			interactive: true,
		},
		{
			name:        "a patch file has no working tree to coordinate over",
			flags:       gen.DiffFlags{Tui: true},
			src:         patchFile,
			format:      outputText,
			interactive: true,
			wantMsg:     "--tui reads the working tree, so it cannot be combined with the patch in x.patch",
		},
		{
			name:        "stdin, same reason and named the same way",
			flags:       gen.DiffFlags{Tui: true},
			src:         stdin,
			format:      outputText,
			interactive: true,
			wantMsg:     "--tui reads the working tree, so it cannot be combined with a patch on stdin",
		},
		{
			name:        "--watch and --tui both own the terminal",
			flags:       gen.DiffFlags{Tui: true, Watch: true},
			src:         workingTree,
			format:      outputText,
			interactive: true,
			wantMsg:     "--tui and --watch both drive the terminal",
		},
		{
			name:        "a machine-readable format cannot be answered with a viewport",
			flags:       gen.DiffFlags{Tui: true},
			src:         workingTree,
			format:      outputJSON,
			interactive: true,
			wantMsg:     "cannot be combined with -o json",
		},
		{
			name:       "no terminal names the command that works here",
			flags:      gen.DiffFlags{Tui: true},
			src:        workingTree,
			format:     outputText,
			wantStderr: "magus: diff --tui requires an interactive terminal; use `magus diff` instead\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags := tc.flags
			var err error
			stderr := captureStderr(t, func() {
				err = diffTUIRefusal(&flags, tc.src, tc.format, tc.interactive)
			})
			assert.Equal(t, tc.wantStderr, stderr)
			if tc.wantMsg == "" && tc.wantStderr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			// Both refusal shapes exit 2: nothing was attempted, so this is a misuse rather
			// than a failure of the work.
			assert.Equal(t, exitUsage, exitCodeOf(err))
			if tc.wantMsg != "" {
				assert.Contains(t, err.Error(), tc.wantMsg)
			}
		})
	}
}

// TestDiffTUIFilesJoinKeepsTheAnnotationOrder guards the one thing this join must not do:
// re-derive an order. The patch arrives in whatever order the VCS emitted and the
// annotations arrive in reading order, so following the patch would quietly undo
// SortForReading and put a lockfile back at the top.
func TestDiffTUIFilesJoinKeepsTheAnnotationOrder(t *testing.T) {
	reach := 12
	rev := types.Diff{Files: []types.DiffFile{
		{Path: "core.go", Role: types.DiffRoleSource, Surface: types.DiffSurfaceInternal, Reach: &reach, Project: "root"},
		{Path: "gen/out.json", Role: types.DiffRoleOutput, Surface: types.DiffSurfaceUnknown},
	}}
	patch := "diff --git a/gen/out.json b/gen/out.json\n" +
		"@@ -1 +1 @@\n" +
		"-{}\n" +
		"+{\"a\":1}\n" +
		"diff --git a/core.go b/core.go\n" +
		"@@ -3 +3 @@\n" +
		"+func F() {}\n"

	files := diffTUIFiles(rev, diff.ParseHunks(patch))
	require.Len(t, files, 2)
	assert.Equal(t, "core.go", files[0].Path)
	assert.False(t, files[0].Generated)
	assert.Equal(t, []string{"12 files reference its widest changed symbol", "in root"}, files[0].Facts)
	require.Len(t, files[0].Hunks, 1)
	assert.Equal(t, "@@ -3 +3 @@", files[0].Hunks[0].Header)
	assert.NotEmpty(t, files[0].Hunks[0].Digest, "the viewed set is keyed by this")

	assert.Equal(t, "gen/out.json", files[1].Path)
	assert.True(t, files[1].Generated)
	require.Len(t, files[1].Hunks, 1)
}

func TestDiffCountsLineIsWhatTheReaderIsLeftWith(t *testing.T) {
	rev := types.Diff{
		Files: []types.DiffFile{
			{Path: "a.go", Role: types.DiffRoleSource},
			{Path: "b.go", Role: types.DiffRoleSource},
			{Path: "gen/out.json", Role: types.DiffRoleOutput},
		},
		SeedProjects:     []string{"root"},
		AffectedProjects: []types.ImpactProject{{Path: "root"}, {Path: "docs"}},
	}
	assert.Equal(t, "2 files to read, 1 generated folded; 1 projects edited, 2 projects rebuild",
		diffCountsLine(rev))
	assert.Equal(t, "0 files to read", diffCountsLine(types.Diff{}))
}
