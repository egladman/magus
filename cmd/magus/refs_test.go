package main

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefsTextPrintsMatchingLines pins the output shape: path:line:text, the one
// every grep-shaped tool and every agent already parses. classify is nil (the
// cold-worktree case: no workspace, no graph, no symbol index) - refsTextCmd
// must still answer, which is the whole reason it runs textScan directly
// instead of going through refs' usual symbol-graph path.
func TestRefsTextPrintsMatchingLines(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "line one\nconst Ref = \"NEEDLE\"\nline three\n")

	var err error
	out := captureStdout(t, func() {
		err = refsTextCmd(context.Background(), w.Root(), "NEEDLE", false, nil)
	})
	require.NoError(t, err, "a match exits 0, like grep")
	assert.Equal(t, w.Path("a.go")+":2:const Ref = \"NEEDLE\"\n", out)
}

// TestRefsTextNoMatchExitsOneAndPrintsNothing pins grep's exit contract, not refs'
// verdict contract: a pattern genuinely absent from the tree is a completed,
// successful search that found zero matches, so it is exit 1 with an empty
// stdout stream, never exit 2 (which this package reserves for a search that
// could not run at all).
func TestRefsTextNoMatchExitsOneAndPrintsNothing(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "nothing interesting here\n")

	var err error
	out := captureStdout(t, func() {
		err = refsTextCmd(context.Background(), w.Root(), "NEEDLE_NOT_PRESENT", false, nil)
	})
	assert.Equal(t, errSilent{exitCode: 1}, err)
	assert.Empty(t, out)
}

// TestRefsTextUnreadableRootExitsTwo pins the distinction a bare walk cannot make
// on its own: searchableFiles SKIPS a file that vanishes mid-walk (a live
// checkout's ordinary churn), but a root that never existed, or cannot be listed
// at all, is a search that never ran - conflating that with "ran and found
// nothing" (exit 1) is the one thing worse than the grep this replaces.
func TestRefsTextUnreadableRootExitsTwo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	var err error
	errOut := captureStderr(t, func() {
		err = refsTextCmd(context.Background(), missing, "NEEDLE", false, nil)
	})
	assert.Equal(t, errSilent{exitCode: 2}, err)
	assert.Contains(t, errOut, missing)
}

// TestRefsTextAnswersColdWithNoClassifier is the case the whole task exists to
// guarantee: refsTextCmd must never answer `unknown`. A nil classify is exactly
// what a cold worktree with no workspace, no graph, and no symbol index hands
// refsCmd (inspectWorkspace failed), and the search must still find real
// matches rather than erroring out or reporting nothing.
func TestRefsTextAnswersColdWithNoClassifier(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "const Ref = \"NEEDLE\"\n")

	var err error
	out := captureStdout(t, func() {
		err = refsTextCmd(context.Background(), w.Root(), "NEEDLE", false, nil)
	})
	require.NoError(t, err)
	assert.Contains(t, out, "NEEDLE")
}

// TestRefsTextNoGeneratedFiltersAndReportsTheCount pins that --no-generated's
// exclusion is COUNTED and PRINTED (to stderr, so stdout stays a pure match
// stream), the same accounting textPresence prints beside a symbol miss: a
// silent under-report is the one failure that makes this worse than grep.
//
// classify comes from a real opened workspace (magus.Open), not inspectWorkspace:
// that helper memoizes its root globally for the process (see helpers.go), which
// only holds for one CLI invocation, not a test binary exercising many roots.
func TestRefsTextNoGeneratedFiltersAndReportsTheCount(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"src/a.go": "const Ref = \"NEEDLE\"\n",
		"gen/b.go": "const Ref = \"NEEDLE\"\n",
	})
	w.Magusfile(`import "magus";
magus\project({"outputs": ["gen/*"]});
export fun build(ctx: magus\Context, args: [str]) > void {}
`)
	root, err := filepath.EvalSymlinks(w.Root())
	require.NoError(t, err)

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "magus.Open")
	t.Cleanup(func() { _ = m.Close() })

	var cmdErr error
	var out, errOut string
	errOut = captureStderr(t, func() {
		out = captureStdout(t, func() {
			cmdErr = refsTextCmd(ctx, root, "NEEDLE", true, m.ClassifyFiles)
		})
	})
	require.NoError(t, cmdErr, "the hand-written file still matches")
	assert.Contains(t, out, filepath.Join("src", "a.go"))
	assert.NotContains(t, out, filepath.Join("gen", "b.go"), "excluded entirely, not just marked")
	assert.Contains(t, errOut, "1 generated file(s) excluded: declared output")
}

// TestRefsTextExitsTwoOnScanError covers the remaining error path (not a root
// problem): an unparseable request. An empty pattern is textindex.Scan's own
// refusal, and refsTextCmd must surface it as exit 2, not silently match nothing.
func TestRefsTextExitsTwoOnScanError(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "anything\n")

	var err error
	errOut := captureStderr(t, func() {
		err = refsTextCmd(context.Background(), w.Root(), "", false, nil)
	})
	assert.Equal(t, errSilent{exitCode: 2}, err)
	assert.NotEmpty(t, errOut)
}
