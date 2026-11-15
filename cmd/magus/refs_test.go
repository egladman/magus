package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/edit"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/clikit/output"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// `magus --root A refs x --rename y` run from inside checkout B grades its edits against A,
// the workspace it loaded. Grading through the cwd asked inspectWorkspace for B after it
// had loaded A, which panics.
func TestRenameGradeInspectsTheLoadedRoot(t *testing.T) {
	loaded := rootElsewhere(t)
	ws, err := inspectWorkspace(t.Context(), loaded)
	require.NoError(t, err)

	grade := edit.RenameGrade(t.Context(), guardDependencies(t.Context(), loaded), ws, true)
	var rule, reason string
	require.NotPanics(t, func() { rule, reason = grade("a.go", []byte("package a\n"), []byte("package b\n")) })
	assert.Empty(t, rule, reason)
}

// rootElsewhere stands up two workspaces, moves the test into the second, and returns the
// first: the --root a command loads from inside another checkout. The memoized workspace
// load is the test's own.
func rootElsewhere(t *testing.T) string {
	t.Helper()
	testkit.Isolate(t)
	resetWorkspaceMemo(t)
	checkout := func() string {
		w := testkit.NewWorkspace(t)
		w.Write("magus.yaml", "")
		return w.Root()
	}
	loaded := checkout()
	t.Chdir(checkout())
	return loaded
}

type refsSourceSuite struct {
	suite.Suite
	w *testkit.Workspace
}

func TestRefsSourceSuite(t *testing.T) { suite.Run(t, new(refsSourceSuite)) }

func (s *refsSourceSuite) SetupTest() {
	s.w = testkit.NewWorkspace(s.T())
	s.w.Write("a.go", "one\ntwo\nthree\nfour\nfive\n")
}

func (s *refsSourceSuite) window(args ...string) (knowledge.RefsWindow, error) {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse(args))
	return refsSourceWindow(rf, fs)
}

func (s *refsSourceSuite) TestContextSetsBothSidesAndImpliesSource() {
	w, err := s.window("--context", "5")
	s.Require().NoError(err)
	s.Equal(knowledge.RefsWindow{SourceWindow: knowledge.SourceWindow{Show: true, From: -5, To: 5, MaxLines: 200}}, w)
}

func (s *refsSourceSuite) TestBeforeOverridesOneSideOfContext() {
	w, err := s.window("--context", "5", "--before", "1")
	s.Require().NoError(err)
	s.Equal(knowledge.RefsWindow{SourceWindow: knowledge.SourceWindow{Show: true, From: -1, To: 5, MaxLines: 200}}, w)
}

func (s *refsSourceSuite) TestAnExplicitZeroOverridesContext() {
	w, err := s.window("--context", "5", "--after", "0")
	s.Require().NoError(err)
	s.Equal(knowledge.RefsWindow{SourceWindow: knowledge.SourceWindow{Show: true, From: -5, To: 0, MaxLines: 200}}, w)
}

func (s *refsSourceSuite) TestMaxLinesAloneImpliesSource() {
	w, err := s.window("--max-lines", "3")
	s.Require().NoError(err)
	s.True(w.Show)
	s.Equal(3, w.MaxLines)
}

func (s *refsSourceSuite) TestTextRefusesAWindow() {
	_, err := s.window("--text", "--context", "2")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *refsSourceSuite) TestLinesTakesASeparateNegativeValue() {
	w, err := s.window("--lines", "-5:-1")
	s.Require().NoError(err)
	s.Equal(knowledge.RefsWindow{SourceWindow: knowledge.SourceWindow{Show: true, From: -5, To: -1, MaxLines: 200}}, w)
}

func (s *refsSourceSuite) TestTheBodyNextCommandDropsOnlyItsOwnFlags() {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse([]string{"--occurrences", "--limit", "5", "--max-lines", "2", "--skip-lines", "2"}))
	got := commandAgain(hint.Refs.With, fs, "Sym", flaggen.FlagRefsMaxLines, flaggen.FlagRefsSkipLines)
	s.Contains(got, "refs 'Sym' --occurrences --limit=5")
	s.NotContains(got, "lines")
}

func (s *refsSourceSuite) TestShellArgLeavesARangeBare() {
	s.Equal("-5:-1", shellArg("-5:-1"))
}

func (s *refsSourceSuite) TestShellArgQuotesAValueWithASpace() {
	s.Equal("'template={{.a}} b'", shellArg("template={{.a}} b"))
}

func (s *refsSourceSuite) TestATrailingPathKeepsOneFile() {
	s.w.Write("a.go", "package a\n")
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse([]string{"--occurrences"}))
	page, err := refsOccurrencePage(rf, fs, s.w.Root(), []string{"Sym", filepath.Join(s.w.Root(), "a.go")})
	s.Require().NoError(err)
	s.Equal([]string{"a.go"}, page.Scopes)
}

func (s *refsSourceSuite) TestAPathThatDoesNotExistIsAUsageError() {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse(nil))
	_, err := refsOccurrencePage(rf, fs, s.w.Root(), []string{"Sym", filepath.Join(s.w.Root(), "AnotherSymbol")})
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "AnotherSymbol")
}

func (s *refsSourceSuite) TestTheNextCommandKeepsTheOutputFlagAndThePath() {
	s.w.Write("a.go", "package a\n")
	path := filepath.Join(s.w.Root(), "a.go")
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	fs.String("o", "", "")
	s.Require().NoError(fs.Parse([]string{"--occurrences", "-o", "json", "--limit", "5"}))
	page, err := refsOccurrencePage(rf, fs, s.w.Root(), []string{"Sym", path})
	s.Require().NoError(err)
	s.Contains(page.Again, fmt.Sprintf("refs 'Sym' --occurrences -o json %s", path))
	s.Equal(5, page.Limit)
}

// check parses args the way refsCommand does, positionals after the flags,
// and returns what checkRefsInput says of them.
func (s *refsSourceSuite) check(args ...string) error {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse(args))
	source, err := refsSourceWindow(rf, fs)
	s.Require().NoError(err)
	rf.Source = source.Show
	return checkRefsInput(rf, fs, fs.Args())
}

func (s *refsSourceSuite) TestAPathOnASymbolBodyIsRefusedNamingOccurrences() {
	err := s.check("--source", "Foo", "some/dir")
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "refs Foo --occurrences some/dir`")
}

func (s *refsSourceSuite) TestAPathOnARenameIsRefused() {
	s.Equal(exit.Usage, exit.Code(s.check("--rename", "Bar", "Foo", "some/dir")))
}

func (s *refsSourceSuite) TestAPathOnThePlainListOrOccurrencesPasses() {
	s.NoError(s.check("Foo", "some/dir"))
	s.NoError(s.check("--occurrences", "Foo", "some/dir"))
}

func (s *refsSourceSuite) TestLimitOnThePlainListIsRefusedNamingOccurrences() {
	err := s.check("--limit", "1", "Foo")
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "add --occurrences")
}

func (s *refsSourceSuite) TestLimitOnOccurrencesAndTextPasses() {
	s.NoError(s.check("--occurrences", "--limit", "1", "Foo"))
	s.NoError(s.check("--text", "--limit", "1", "Foo"))
}

func (s *refsSourceSuite) TestDefinitionWithOccurrencesIsRefused() {
	s.Equal(exit.Usage, exit.Code(s.check("--definition", "--occurrences", "Foo")))
}

func (s *refsSourceSuite) TestOccurrencesWithSourcePasses() {
	s.NoError(s.check("--occurrences", "--source", "Foo"))
}

func (s *refsSourceSuite) TestTextWithASymbolFlagIsRefusedNamingIt() {
	for _, name := range []string{"--occurrences", "--definition", "--source", "--refresh"} {
		err := s.check("--text", name, "Foo")
		s.Equal(exit.Usage, exit.Code(err), name)
		s.ErrorContains(err, name)
	}
	s.ErrorContains(s.check("--text", "--rename", "Bar", "Foo"), "--rename")
	s.ErrorContains(s.check("--text", "--offset", "1", "Foo"), "--offset")
}

func (s *refsSourceSuite) TestRenameWithAReadModeIsRefused() {
	s.Equal(exit.Usage, exit.Code(s.check("--rename", "Bar", "--occurrences", "Foo")))
	s.Equal(exit.Usage, exit.Code(s.check("--rename", "Bar", "--source", "Foo")))
}

func (s *refsSourceSuite) TestTheNextCommandKeepsTheWindowFlags() {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse([]string{"--occurrences", "--lines=-5:-1", "--limit", "5"}))
	page, err := refsOccurrencePage(rf, fs, s.w.Root(), []string{"Sym"})
	s.Require().NoError(err)
	s.Contains(page.Again, "refs 'Sym' --occurrences --lines=-5:-1")
	s.Equal(5, page.Limit)
}

func (s *refsSourceSuite) TestOffsetWithoutOccurrencesIsAUsageError() {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse([]string{"--offset", "5"}))
	_, err := refsOccurrencePage(rf, fs, s.w.Root(), []string{"Sym"})
	s.Equal(exit.Usage, exit.Code(err))
}

// TestRefsTextPrintsMatchingLines pins the output shape: path:line:text, the one
// every grep-shaped tool and every agent already parses. classify is nil (the
// cold-worktree case: no workspace, no graph, no symbol index); refsTextCommand
// must still answer, which is the whole reason it runs textScan directly
// instead of going through refs' usual symbol-graph path.
func TestRefsTextPrintsMatchingLines(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "line one\nconst Ref = \"NEEDLE\"\nline three\n")

	var err error
	out := captureStdout(t, func() {
		err = refsTextCommand(context.Background(), w.Root(), "NEEDLE", nil, false, 0, nil)
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
		err = refsTextCommand(context.Background(), w.Root(), "NEEDLE_NOT_PRESENT", nil, false, 0, nil)
	})
	assert.Equal(t, exit.Reported(exit.Failure), err)
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
		err = refsTextCommand(context.Background(), missing, "NEEDLE", nil, false, 0, nil)
	})
	assert.Equal(t, exit.Reported(exit.Usage), err)
	assert.Contains(t, errOut, missing)
}

// TestRefsTextAnswersColdWithNoClassifier is the case the whole task exists to
// guarantee: refsTextCommand must never answer `unknown`. A nil classify is exactly
// what a cold worktree with no workspace, no graph, and no symbol index hands
// refsCommand (inspectWorkspace failed), and the search must still find real
// matches rather than erroring out or reporting nothing.
func TestRefsTextAnswersColdWithNoClassifier(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "const Ref = \"NEEDLE\"\n")

	var err error
	out := captureStdout(t, func() {
		err = refsTextCommand(context.Background(), w.Root(), "NEEDLE", nil, false, 0, nil)
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
		err = refsTextCommand(context.Background(), w.Root(), "NEEDLE", []string{w.Path("internal/guard")}, false, 0, nil)
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
		err = refsTextCommand(context.Background(), w.Root(), "NEEDLE", []string{filepath.Join(w.Root(), "..")}, false, 0, nil)
	})
	assert.Equal(t, exit.Reported(exit.Usage), err)
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
	root := w.Root()

	ctx := context.Background()
	m, err := magus.Open(ctx, root, magus.WithSpawnRule(magus.TierCeiling))
	require.NoError(t, err, "magus.Open")
	t.Cleanup(func() { _ = m.Close() })

	var cmdErr error
	var out, errOut string
	errOut = captureStderr(t, func() {
		out = captureStdout(t, func() {
			cmdErr = refsTextCommand(ctx, root, "NEEDLE", nil, true, 0, m.ClassifyFiles)
		})
	})
	require.NoError(t, cmdErr, "the hand-written file still matches")
	assert.Contains(t, out, filepath.Join("src", "a.go"))
	assert.NotContains(t, out, filepath.Join("gen", "b.go"), "excluded entirely, not just marked")
	assert.Contains(t, errOut, "1 generated file(s) excluded: declared output")
}

// TestRefsTextExitsTwoOnScanError covers the remaining error path (not a root
// problem): an unparseable request. An empty pattern is textindex.Scan's own
// refusal, and refsTextCommand must surface it as exit 2, not silently match nothing.
func TestRefsTextExitsTwoOnScanError(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "anything\n")

	var err error
	errOut := captureStderr(t, func() {
		err = refsTextCommand(context.Background(), w.Root(), "", nil, false, 0, nil)
	})
	assert.Equal(t, exit.Reported(exit.Usage), err)
	assert.NotEmpty(t, errOut)
}

// The miss block carries the real verdict and index cause in every format: the cmd's
// verdict printer, not a stand-in, so the wiring that supplies it is under test.
func TestReportRefsMissCarriesTheIndexCauseInTheVerdictBlock(t *testing.T) {
	ans := types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{{Project: types.NewProjectRef("libs/api", ""), State: types.SymbolIndexNotBuilt}})
	ans.IndexCause = &types.KnowledgeIndexCause{
		Why: "no server is running, so the refresh hook's `magus job run sync-graph` does nothing",
		Fix: "`magus server start` keeps it current from now on",
	}
	miss := knowledge.RefsMiss{Name: "Foo", Answer: ans}

	var stderr bytes.Buffer
	var err error
	stdout := captureStdout(t, func() {
		err = knowledge.ReportRefsMiss(&stderr, refsEnv(t.Context(), ""), output.Options{Format: outputJSON}, miss)
	})
	require.NoError(t, err)
	var record types.KnowledgeRefsOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &record), "stdout: %q", stdout)
	assert.Equal(t, ans.IndexCause, record.Answer.IndexCause)
	assert.Contains(t, stderr.String(), fmt.Sprintf("why: %s\n", ans.IndexCause.Why))
	assert.Contains(t, stderr.String(), fmt.Sprintf("fix: %s\n", ans.IndexCause.Fix))

	stderr.Reset()
	stdout = captureStdout(t, func() {
		err = knowledge.ReportRefsMiss(&stderr, refsEnv(t.Context(), ""), output.Options{Format: outputText}, miss)
	})
	require.NoError(t, err)
	assert.Empty(t, stdout, "text output keeps stdout for answers")
	assert.Contains(t, stderr.String(), "outside coverage: libs/api (not-indexed)")
	assert.Contains(t, stderr.String(), fmt.Sprintf(`  why: %s
  fix: %s
`, ans.IndexCause.Why, ans.IndexCause.Fix))
}

// withIndexCause diagnoses only an answer drawn from a missing or stale index; a clean
// answer stays without the key.
func TestWithIndexCauseFillsOnlyAnIndexBehind(t *testing.T) {
	w := testkit.NewWorkspace(t)
	behind := types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{{Project: types.NewProjectRef(".", ""), State: types.SymbolIndexNotBuilt}})
	got := withIndexCause(t.Context(), w.Root(), behind)
	require.NotNil(t, got.IndexCause)
	assert.NotEmpty(t, got.IndexCause.Why)
	assert.NotEmpty(t, got.IndexCause.Fix)

	clean := withIndexCause(t.Context(), w.Root(), types.ClassifyAnswer(true, "", nil))
	assert.Nil(t, clean.IndexCause)
}
