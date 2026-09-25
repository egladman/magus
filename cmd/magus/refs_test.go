package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// checkDefinitions is what stands between a recorded range and a wrong edit. A file older
// than its index verifies; a newer one keeps the range only as unverified; a start line
// that no longer names the symbol is changed; a range past the end of the file is
// unreadable rather than silently truncated.
func TestCheckDefinitions(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "package a\n\nfunc F() {\n\treturn\n}\n\nfunc FF() {}\n")
	w.Write("b.go", "package a\n\nfunc F() {\n\treturn\n}\n")
	info, err := os.Stat(w.Path("a.go"))
	require.NoError(t, err)
	indexedAt := func(file string) (time.Time, bool) {
		switch file {
		case "a.go":
			return info.ModTime(), true
		case "b.go":
			return info.ModTime().Add(-time.Hour), true
		}
		return time.Time{}, false
	}
	site := func(file string, start, end int) types.KnowledgeDefinitionSite {
		return types.KnowledgeDefinitionSite{File: file, StartLine: start, EndLine: end, Status: types.DefinitionUnverified}
	}
	sites := []types.KnowledgeDefinitionSite{
		site("a.go", 3, 5), site("b.go", 3, 5), site("a.go", 7, 0),
		site("a.go", 3, 40), site("gone.go", 1, 0), site("other.go", 0, 0),
	}
	checkDefinitions(w.Root(), "F", sites, indexedAt, true)

	fn := "func F() {\n\treturn\n}"
	assert.Equal(t, []types.KnowledgeDefinitionSite{
		{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified, Source: fn},
		{File: "b.go", StartLine: 3, EndLine: 5, Status: types.DefinitionUnverified, Source: fn},
		{File: "a.go", StartLine: 7, Status: types.DefinitionChanged, Source: "func FF() {}"},
		{File: "a.go", StartLine: 3, EndLine: 40, Status: types.DefinitionUnreadable},
		{File: "gone.go", StartLine: 1, Status: types.DefinitionUnreadable},
		{File: "other.go", Status: types.DefinitionUnverified},
	}, sites)
}

func TestEmitDefinitionsText(t *testing.T) {
	out := types.KnowledgeDefinitionsOutput{
		Symbol: "symbol:a F().", Label: "F",
		Definitions: []types.KnowledgeDefinitionSite{
			{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified, Source: "func F() {\n\treturn\n}"},
			{File: "b.go", StartLine: 9, Status: types.DefinitionVerified, Source: "var F = 1"},
			{File: "c.go"},
		},
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}
	var err error
	got := captureStdout(t, func() { err = emitDefinitions(os.Stdout, OutputOptions{Format: FormatText}, out) })
	require.NoError(t, err)
	assert.Equal(t, "symbol: symbol:a F().  (F)\n"+
		"a.go:3-5  verified\nfunc F() {\n\treturn\n}\n"+
		"\nb.go:9  verified  (the index recorded no end line; the declaration line alone)\nvar F = 1\n"+
		"c.go  (the index recorded no line in this file)\n", got)
}

// A changed range still prints, marked, but the exit status says the lines are not the
// symbol's; -o name withholds it, since a bare range has nowhere to carry the mark.
func TestEmitDefinitionsChangedExitsOne(t *testing.T) {
	out := types.KnowledgeDefinitionsOutput{
		Symbol: "symbol:a F().", Label: "F",
		Definitions: []types.KnowledgeDefinitionSite{
			{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionChanged},
			{File: "b.go", StartLine: 1, EndLine: 2, Status: types.DefinitionVerified},
		},
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}
	var err error
	got := captureStdout(t, func() { err = emitDefinitions(os.Stdout, OutputOptions{Format: FormatText}, out) })
	assert.Equal(t, errSilent{exitCode: 1}, err)
	assert.Contains(t, got, "a.go:3-5  changed  (edited since indexing; refresh with `")

	got = captureStdout(t, func() { err = emitDefinitions(os.Stdout, OutputOptions{Format: outputName}, out) })
	assert.Equal(t, errSilent{exitCode: 1}, err)
	assert.Equal(t, "b.go:1-2\n", got)
}

// A stale index fails a refs answer, but a digest-verified definition is proof of itself:
// the notice still prints, and only an answer with an unverified site exits 1 for it.
func TestEmitDefinitionsStaleIndex(t *testing.T) {
	answer := types.KnowledgeAnswer{Verdict: types.VerdictFound, StaleIndexes: []string{"."}}
	site := types.KnowledgeDefinitionSite{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified}
	out := types.KnowledgeDefinitionsOutput{Symbol: "symbol:a F().", Definitions: []types.KnowledgeDefinitionSite{site}, Answer: answer}

	var err error
	got := captureStdout(t, func() { err = emitDefinitions(os.Stdout, OutputOptions{Format: FormatText}, out) })
	require.NoError(t, err)
	assert.Contains(t, got, staleIndexNotice(answer.StaleIndexes))

	out.Definitions[0].Status = types.DefinitionUnverified
	captureStdout(t, func() { err = emitDefinitions(os.Stdout, OutputOptions{Format: FormatText}, out) })
	assert.Equal(t, errSilent{exitCode: 1}, err)
}

// TestRefsTextPrintsMatchingLines pins the output shape: path:line:text, the one
// every grep-shaped tool and every agent already parses. classify is nil (the
// cold-worktree case: no workspace, no graph, no symbol index); refsTextCmd
// must still answer, which is the whole reason it runs textScan directly
// instead of going through refs' usual symbol-graph path.
func TestRefsTextPrintsMatchingLines(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "line one\nconst Ref = \"NEEDLE\"\nline three\n")

	var err error
	out := captureStdout(t, func() {
		err = refsTextCmd(context.Background(), w.Root(), "NEEDLE", nil, false, 0, nil)
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
		err = refsTextCmd(context.Background(), w.Root(), "NEEDLE_NOT_PRESENT", nil, false, 0, nil)
	})
	assert.Equal(t, errSilent{exitCode: 1}, err)
	assert.Empty(t, out)
}

// TestRefsTextUnreadableRootExitsTwo pins the distinction a bare walk cannot make
// on its own: searchableFiles SKIPS a file that vanishes mid-walk (a live
// checkout's ordinary churn), but a root that never existed, or cannot be listed
// at all, is a search that never ran: conflating that with "ran and found
// nothing" (exit 1) is the one thing worse than the grep this replaces.
func TestRefsTextUnreadableRootExitsTwo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	var err error
	errOut := captureStderr(t, func() {
		err = refsTextCmd(context.Background(), missing, "NEEDLE", nil, false, 0, nil)
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
		err = refsTextCmd(context.Background(), w.Root(), "NEEDLE", nil, false, 0, nil)
	})
	require.NoError(t, err)
	assert.Contains(t, out, "NEEDLE")
}

// TestRefsTextScopesToNamedPaths is the grep shape this had no answer for: `grep -r NEEDLE
// internal/guard`. Without a scope the only text search magus offered was the whole
// workspace, so a reader asking about one directory had to reach past magus to ask it.
func TestRefsTextScopesToNamedPaths(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"internal/guard/shell.go": "const A = \"NEEDLE\"\n",
		"internal/hint/hint.go":   "const B = \"NEEDLE\"\n",
	})

	var err error
	out := captureStdout(t, func() {
		err = refsTextCmd(context.Background(), w.Root(), "NEEDLE", []string{w.Path("internal/guard")}, false, 0, nil)
	})
	require.NoError(t, err)
	assert.Contains(t, out, filepath.Join("internal", "guard", "shell.go"))
	assert.NotContains(t, out, filepath.Join("internal", "hint", "hint.go"), "the scope is a narrowing, not a hint")
}

// TestRefsTextScopeOutsideWorkspaceExitsTwo keeps a refused scope distinguishable from an
// honest miss. Exit 1 would say the pattern is not in a tree magus never looked at.
func TestRefsTextScopeOutsideWorkspaceExitsTwo(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "const Ref = \"NEEDLE\"\n")

	var err error
	errOut := captureStderr(t, func() {
		err = refsTextCmd(context.Background(), w.Root(), "NEEDLE", []string{filepath.Join(w.Root(), "..")}, false, 0, nil)
	})
	assert.Equal(t, errSilent{exitCode: 2}, err)
	assert.Contains(t, errOut, "outside the workspace")
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
			cmdErr = refsTextCmd(ctx, root, "NEEDLE", nil, true, 0, m.ClassifyFiles)
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
		err = refsTextCmd(context.Background(), w.Root(), "", nil, false, 0, nil)
	})
	assert.Equal(t, errSilent{exitCode: 2}, err)
	assert.NotEmpty(t, errOut)
}
