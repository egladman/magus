package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus"
	flaggen "github.com/egladman/magus/cmd/magus/gen"
	"github.com/egladman/magus/internal/graph/knowledge"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/textindex"
	"github.com/egladman/magus/libs/cmdline/exit"
	"github.com/egladman/magus/libs/cmdline/output"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// A bare name two workspace definitions carry must not quietly answer for one of them: refs
// exits 2 and names each candidate as a command that asks for it alone.
func TestRefsSymbolRefusesANameSeveralDefinitionsCarry(t *testing.T) {
	const hintID, queueID, depID = "symbol:x `hint`/Classify().", "symbol:x `queue`/Workspace#Classify().", "symbol:dep/Classify()."
	sym := func(id string) types.KnowledgeNode {
		return types.KnowledgeNode{ID: id, Kind: types.KindSymbol, Label: "Classify"}
	}
	defines := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}
	}
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{sym(hintID), sym(queueID), sym(depID)},
		[]types.KnowledgeEdge{defines("hint/a.go", hintID), defines("queue/b.go", queueID)})

	var stderr bytes.Buffer
	_, err := refsSymbol(&stderr, g, "Classify")

	var silent reportedExit
	require.ErrorAs(t, err, &silent)
	assert.Equal(t, exit.Usage, silent.ExitCode())
	assert.Equal(t, fmt.Sprintf(`magus refs: "Classify" names 2 symbols defined in this workspace; ask for one by id:
  %s
  %s
`, hint.Refs.With("'"+hintID+"'"), hint.Refs.With("'"+queueID+"'")), stderr.String())

	got, err := refsSymbol(&stderr, g, queueID)
	require.NoError(t, err)
	assert.Equal(t, queueID, got, "an exact id picks itself")

	only := knowledge.NewGraph()
	only.Merge([]types.KnowledgeNode{sym(hintID), sym(depID)}, []types.KnowledgeEdge{defines("hint/a.go", hintID)})
	got, err = refsSymbol(&stderr, only, "Classify")
	require.NoError(t, err)
	assert.Equal(t, hintID, got, "the one workspace definition wins over a dependency's symbol of that name")
}

// Retrieval ranks by text, so an exact symbol id can fall outside g.Resolve's matches;
// `refs <id> --rename` was refused for an id `refs <id>` listed.
func TestRenameCandidatesIncludeAnExactSymbolID(t *testing.T) {
	const id = "symbol:gomod x `x/internal/edit`/checkUnchanged()."
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{
		{ID: id, Kind: types.KindSymbol, Label: "checkUnchanged"},
		{ID: "file:edit.go", Kind: types.KindFile, Label: "edit.go"},
	}, []types.KnowledgeEdge{{Source: "file:edit.go", Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}})

	ids := []string{}
	for _, m := range renameCandidates(g, id) {
		ids = append(ids, m.ID)
	}
	assert.Contains(t, ids, id)
}

// A project with a Go and a Buzz index judges them apart, and a rename is refused only for
// an index that could hold the symbol's sites. `refs probeTimeoutFrom --rename` was refused
// for "project . changed after its index was built" when only the Buzz index was behind.
func TestRenameBlindSpotsCountOnlyTheSymbolsLanguage(t *testing.T) {
	const goID, buzzID, bareID = "symbol:gomod x `x`/probeTimeoutFrom().", "symbol:buzz x spell/probe().", "symbol:x bare()."
	defines := func(file, id string) types.KnowledgeEdge {
		return types.KnowledgeEdge{Source: "file:" + file, Target: id, Relation: types.RelationDefines, Confidence: types.ConfidenceExtracted, Score: 1}
	}
	g := knowledge.NewGraph()
	g.Merge([]types.KnowledgeNode{
		{ID: goID, Kind: types.KindSymbol, Label: "probeTimeoutFrom", Attrs: map[string]string{types.AttrLanguage: "go"}},
		// No language on the node: the defining file's is the symbol's.
		{ID: buzzID, Kind: types.KindSymbol, Label: "probe"},
		{ID: "file:spell.buzz", Kind: types.KindFile, Label: "spell.buzz", Attrs: map[string]string{types.AttrLanguage: "buzz"}},
		{ID: bareID, Kind: types.KindSymbol, Label: "bare"},
	}, []types.KnowledgeEdge{defines("probe.go", goID), defines("spell.buzz", buzzID), defines("bare.txt", bareID)})

	staleGo := types.KnowledgeStaleIndex{Project: ".", Language: "go", Op: "scip"}
	staleBuzz := types.KnowledgeStaleIndex{Project: ".", Language: "buzz", Op: "scip-buzz"}
	missingBuzz := types.KnowledgeSymbolGap{Project: types.NewProjectRef(".", ""), Language: "buzz", State: types.SymbolIndexNotBuilt}
	refused := func(index string) types.EditRefusal {
		return types.EditRefusal{Reason: fmt.Sprintf("project . changed after its %s was built; refresh with `%s`", index, hint.GraphBuild)}
	}

	for _, tc := range []struct {
		name  string
		id    string
		stale []types.KnowledgeStaleIndex
		gaps  []types.KnowledgeSymbolGap
		want  []types.EditRefusal
	}{
		{name: "go symbol beside a stale buzz index", id: goID, stale: []types.KnowledgeStaleIndex{staleBuzz}},
		{name: "go symbol beside a missing buzz index", id: goID, gaps: []types.KnowledgeSymbolGap{missingBuzz}},
		{name: "go symbol beside a stale go index", id: goID, stale: []types.KnowledgeStaleIndex{staleBuzz, staleGo}, want: []types.EditRefusal{refused("go index")}},
		{name: "buzz symbol beside a stale go index", id: buzzID, stale: []types.KnowledgeStaleIndex{staleGo}},
		{name: "buzz symbol beside a stale buzz index", id: buzzID, stale: []types.KnowledgeStaleIndex{staleGo, staleBuzz}, want: []types.EditRefusal{refused("buzz index")}},
		{name: "a symbol of no known language counts every index", id: bareID, stale: []types.KnowledgeStaleIndex{staleBuzz}, want: []types.EditRefusal{refused("buzz index")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cov := knowledge.Coverage{Seeded: true, Probed: true, Stale: tc.stale, Gaps: tc.gaps}
			got := renameBlindSpots(tc.id, g.SymbolLanguage(tc.id), cov)
			if tc.want == nil {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tc.want, got)
		})
	}
}

// `magus --root A refs x --rename y` run from inside checkout B grades its edits against A,
// the workspace it loaded. Grading through the cwd asked inspectWorkspace for B after it
// had loaded A, which panics.
func TestRenameGradeInspectsTheLoadedRoot(t *testing.T) {
	loaded := rootElsewhere(t)
	ws, err := inspectWorkspace(t.Context(), loaded)
	require.NoError(t, err)

	grade := renameGrade(t.Context(), ws, loaded, true)
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
	checkDefinitions(w.Root(), "F", sites, indexedAt, sourceWindow{Show: true})

	fn := "func F() {\n\treturn\n}"
	assert.Equal(t, []types.KnowledgeDefinitionSite{
		{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified, Source: fn, SourceStartLine: 3},
		{File: "b.go", StartLine: 3, EndLine: 5, Status: types.DefinitionUnverified, Source: fn, SourceStartLine: 3},
		{File: "a.go", StartLine: 7, Status: types.DefinitionChanged, Source: "func FF() {}", SourceStartLine: 7},
		{File: "a.go", StartLine: 3, EndLine: 40, Status: types.DefinitionUnreadable},
		{File: "gone.go", StartLine: 1, Status: types.DefinitionUnreadable},
		{File: "other.go", Status: types.DefinitionUnverified},
	}, sites)
}

// A definition's source carries the comment block directly above it, where the symbol
// states its contract and names the test that pins it; a blank line ends the block.
func TestCheckDefinitionsIncludesTheDocComment(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "package a\n\n// unrelated\n\n// F splits a letter from a digit.\n// TestF pins it.\nfunc F() {}\n")
	sites := []types.KnowledgeDefinitionSite{{File: "a.go", StartLine: 7, Status: types.DefinitionUnverified}}
	checkDefinitions(w.Root(), "F", sites, nil, sourceWindow{Show: true})
	assert.Equal(t, "// F splits a letter from a digit.\n// TestF pins it.\nfunc F() {}", sites[0].Source)
}

type refsSourceSuite struct {
	suite.Suite
	w     *testkit.Workspace
	files []types.SymbolOccurrenceFile
}

func TestRefsSourceSuite(t *testing.T) { suite.Run(t, new(refsSourceSuite)) }

func (s *refsSourceSuite) SetupTest() {
	s.w = testkit.NewWorkspace(s.T())
	s.w.Write("a.go", "one\ntwo\nthree\nfour\nfive\n")
	s.files = []types.SymbolOccurrenceFile{{File: "a.go", Occurrences: []types.SymbolOccurrence{{Line: 3, EndLine: 3}}}}
}

func (s *refsSourceSuite) window(args ...string) (sourceWindow, error) {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse(args))
	return refsSourceWindow(rf, fs)
}

func (s *refsSourceSuite) TestContextSetsBothSidesAndImpliesSource() {
	w, err := s.window("--context", "5")
	s.Require().NoError(err)
	s.Equal(sourceWindow{Show: true, From: -5, To: 5, MaxLines: 200}, w)
}

func (s *refsSourceSuite) TestBeforeAndAfterImplySourceAlone() {
	w, err := s.window("--before", "5", "--after", "4")
	s.Require().NoError(err)
	s.Equal(sourceWindow{Show: true, From: -5, To: 4, MaxLines: 200}, w)
}

func (s *refsSourceSuite) TestBeforeOverridesOneSideOfContext() {
	w, err := s.window("--context", "5", "--before", "1")
	s.Require().NoError(err)
	s.Equal(sourceWindow{Show: true, From: -1, To: 5, MaxLines: 200}, w)
}

func (s *refsSourceSuite) TestAnExplicitZeroOverridesContext() {
	w, err := s.window("--context", "5", "--after", "0")
	s.Require().NoError(err)
	s.Equal(sourceWindow{Show: true, From: -5, To: 0, MaxLines: 200}, w)
}

func (s *refsSourceSuite) TestNoWindowFlagsPrintNoSource() {
	w, err := s.window()
	s.Require().NoError(err)
	s.False(w.Show)
}

func (s *refsSourceSuite) long(w sourceWindow) types.SymbolOccurrence {
	s.w.Write("long.go", "1\n2\n3\n4\n5\n6\n")
	files := []types.SymbolOccurrenceFile{{File: "long.go", Occurrences: []types.SymbolOccurrence{{Line: 1, EndLine: 6}}}}
	fillOccurrenceSources(s.w.Root(), files, w)
	return files[0].Occurrences[0]
}

func (s *refsSourceSuite) TestABodyShorterThanTheCapIsNotCut() {
	occ := s.long(sourceWindow{Show: true, MaxLines: 200})
	s.Equal("1\n2\n3\n4\n5\n6", occ.Source)
	s.Zero(occ.SourceTotalLines)
	s.Empty(occ.SourceNote)
}

func (s *refsSourceSuite) TestACutBodySaysSoAndNamesTheNextCommand() {
	occ := s.long(sourceWindow{Show: true, MaxLines: 2, again: "magus refs 'F' --source"})
	s.Equal("1\n2", occ.Source)
	s.Equal(6, occ.SourceTotalLines)
	s.Equal("2 of 6 lines; next: magus refs 'F' --source --max-lines 2 --skip-lines 2", occ.SourceNote)
}

func (s *refsSourceSuite) TestSkippedLinesCarryTheLineNumbersOn() {
	occ := s.long(sourceWindow{Show: true, MaxLines: 2, SkipLines: 2, again: "x"})
	s.Equal("3\n4", occ.Source)
	s.Equal(3, occ.SourceStartLine)
	s.Equal("2 of 6 lines; next: x --max-lines 2 --skip-lines 4", occ.SourceNote)
}

func (s *refsSourceSuite) TestTheLastBodyPageNamesNoNextCommand() {
	occ := s.long(sourceWindow{Show: true, MaxLines: 2, SkipLines: 4})
	s.Equal("5\n6", occ.Source)
	s.Equal("2 of 6 lines", occ.SourceNote)
}

func (s *refsSourceSuite) TestASkipPastTheEndSaysSo() {
	occ := s.long(sourceWindow{Show: true, SkipLines: 9})
	s.Empty(occ.Source)
	s.Zero(occ.SourceStartLine)
	s.Equal("no lines: --skip-lines 9 is past the last of 6 lines", occ.SourceNote)
}

func (s *refsSourceSuite) TestMaxLinesZeroPrintsEverything() {
	occ := s.long(sourceWindow{Show: true})
	s.Equal(1, occ.SourceStartLine)
	s.Empty(occ.SourceNote)
}

func (s *refsSourceSuite) TestTheCapAppliesToALinesRange() {
	occ := s.long(sourceWindow{Show: true, From: 0, To: 0, MaxLines: 1, again: "x"})
	s.Equal("1", occ.Source)
	s.Equal("1 of 6 lines; next: x --max-lines 1 --skip-lines 1", occ.SourceNote)
}

func (s *refsSourceSuite) TestMaxLinesAloneImpliesSource() {
	w, err := s.window("--max-lines", "3")
	s.Require().NoError(err)
	s.True(w.Show)
	s.Equal(3, w.MaxLines)
}

func (s *refsSourceSuite) TestANegativeCapIsAUsageError() {
	_, err := s.window("--max-lines=-1")
	s.Equal(exit.Usage, exit.Code(err))
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

func (s *refsSourceSuite) TestMarksATestFile() {
	files := []types.SymbolOccurrenceFile{{File: "a_test.go"}, {File: "a.go"}}
	s.Empty(markOccurrenceFiles(context.Background(), files, func(context.Context, []string) ([]types.FileEntry, error) { return nil, nil }))
	s.True(files[0].Test)
	s.False(files[1].Test)
}

func (s *refsSourceSuite) TestMarksAGeneratedFile() {
	files := []types.SymbolOccurrenceFile{{File: "gen/a.go"}, {File: "a.go"}}
	s.Empty(markOccurrenceFiles(context.Background(), files, func(context.Context, []string) ([]types.FileEntry, error) {
		return []types.FileEntry{{Path: "gen/a.go", Role: types.DiffRoleOutput}, {Path: "a.go", Role: "source"}}, nil
	}))
	s.True(files[0].Generated)
	s.False(files[1].Generated)
}

func (s *refsSourceSuite) TestAClassifyFailureSaysGeneratedIsUnmarked() {
	note := markOccurrenceFiles(context.Background(), []types.SymbolOccurrenceFile{{File: "a.go"}}, nil)
	s.Contains(note, "generated files are not marked")
}

func (s *refsSourceSuite) section(attrs map[string]string) types.KnowledgeNode {
	return types.KnowledgeNode{ID: "docsection:d.md#b", Kind: types.KindDocSection, Source: "d.md#b", Attrs: attrs}
}

func (s *refsSourceSuite) TestADocSectionPrintsItsExtent() {
	s.w.Write("d.md", "# A\n\n## B\n\nbody\n\n# C\n")
	sum := sha256.Sum256([]byte("## B\n\nbody"))
	site, err := docSectionSource(s.w.Root(), s.section(map[string]string{"start_line": "3", "end_line": "5", "digest": hex.EncodeToString(sum[:])}), sourceWindow{Show: true})
	s.Require().NoError(err)
	s.Equal("## B\n\nbody", site.Source)
	s.Equal(3, site.SourceStartLine)
	s.Equal(types.DefinitionVerified, site.Status)
}

func (s *refsSourceSuite) TestAnEditedDocSectionSaysChanged() {
	s.w.Write("d.md", "# A\n\n## B\n\nedited\n\n# C\n")
	site, err := docSectionSource(s.w.Root(), s.section(map[string]string{"start_line": "3", "end_line": "5", "digest": "old"}), sourceWindow{Show: true})
	s.Require().NoError(err)
	s.Equal(types.DefinitionChanged, site.Status)
	s.Equal(exit.Failure, exit.Code(docSectionExit(site)))
}

func (s *refsSourceSuite) TestADocSectionWithNoExtentSaysSo() {
	site, err := docSectionSource(s.w.Root(), s.section(nil), sourceWindow{Show: true})
	s.Require().NoError(err)
	s.Contains(site.SourceNote, "no extent")
}

func (s *refsSourceSuite) TestADocSectionPastTheFileIsUnreadable() {
	s.w.Write("d.md", "# A\n")
	site, err := docSectionSource(s.w.Root(), s.section(map[string]string{"start_line": "3", "end_line": "50"}), sourceWindow{Show: true})
	s.Require().NoError(err)
	s.Equal(types.DefinitionUnreadable, site.Status)
	s.Contains(site.SourceNote, "is shorter")
}

func (s *refsSourceSuite) TestExplainSourceRefusesANodeThatIsNoSection() {
	_, err := docSectionSource(s.w.Root(), types.KnowledgeNode{ID: "file:a", Kind: types.KindFile}, sourceWindow{Show: true})
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "magus refs --source")
}

func (s *refsSourceSuite) TestANegativeWindowIsAUsageError() {
	_, err := s.window("--context=-1")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *refsSourceSuite) TestTextRefusesAWindow() {
	_, err := s.window("--text", "--context", "2")
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *refsSourceSuite) TestAnOccurrencePrintsItsOwnLine() {
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true})
	s.Equal("three", s.files[0].Occurrences[0].Source)
}

func (s *refsSourceSuite) TestBeforeAndAfterWidenTheBlock() {
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true, From: -1, To: 1})
	s.Equal("two\nthree\nfour", s.files[0].Occurrences[0].Source)
}

func (s *refsSourceSuite) TestTheWindowClampsToTheFile() {
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true, From: -9, To: 9})
	s.Equal("one\ntwo\nthree\nfour\nfive", s.files[0].Occurrences[0].Source)
}

func (s *refsSourceSuite) TestAnUnreadableFileSaysSo() {
	s.files[0].File = "gone.go"
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true})
	s.Empty(s.files[0].Occurrences[0].Source)
	s.Equal("no lines: cannot read gone.go", s.files[0].Occurrences[0].SourceNote)
}

func (s *refsSourceSuite) TestSourceRecordsItsFirstLine() {
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true, From: -1, To: 0})
	s.Equal(2, s.files[0].Occurrences[0].SourceStartLine)
}

func (s *refsSourceSuite) TestABlockNumbersItsLinesAndMarksTheFocus() {
	var b bytes.Buffer
	writeSourceBlock(&b, "  ", "nine\nten", 9, 10, "")
	s.Equal("    9  nine\n  >10  ten\n", b.String())
}

func (s *refsSourceSuite) TestABlockAboveTheMatchMarksNothing() {
	var b bytes.Buffer
	writeSourceBlock(&b, "", "one\ntwo", 1, 3, "")
	s.Equal(" 1  one\n 2  two\n", b.String())
}

func (s *refsSourceSuite) TestABlankLineIsALineAndNotAnEmptyRange() {
	s.w.Write("d.go", "one\n\nthree\n")
	s.files[0].File = "d.go"
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true, From: -1, To: -1})
	occ := s.files[0].Occurrences[0]
	s.Empty(occ.Source)
	s.Equal(2, occ.SourceStartLine)
	s.Empty(occ.SourceNote)
}

func (s *refsSourceSuite) TestAnEmptyRangeSaysSoInText() {
	var b bytes.Buffer
	writeSourceBlock(&b, "  ", "", 0, 3, "no lines: 9:12 falls outside the file")
	s.Equal("  (no lines: 9:12 falls outside the file)\n", b.String())
}

func (s *refsSourceSuite) TestAnEmptyRangeReachesJSON() {
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true, From: 9, To: 12})
	got, err := json.Marshal(s.files[0].Occurrences[0])
	s.Require().NoError(err)
	s.Contains(string(got), `"source_note":"no lines: 9:12 falls outside the file"`)
}

func (s *refsSourceSuite) TestLinesTakesASeparateNegativeValue() {
	w, err := s.window("--lines", "-5:-1")
	s.Require().NoError(err)
	s.Equal(sourceWindow{Show: true, From: -5, To: -1, MaxLines: 200}, w)
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

func (s *refsSourceSuite) TestATrailingPathKeepsOneFile() {
	s.w.Write("a.go", "package a\n")
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse([]string{"--occurrences"}))
	page, err := refsOccurrencePage(rf, fs, s.w.Root(), []string{"Sym", filepath.Join(s.w.Root(), "a.go")})
	s.Require().NoError(err)
	s.Len(page.scoped(s.sites(4)), 1)
	s.Equal("a.go keeps 2 of 4 sites", page.scopeNote(s.sites(4), 2))
}

func (s *refsSourceSuite) TestATrailingPathKeepsADirectoryAndNotASharedPrefix() {
	page := occurrencePage{scopes: []string{"internal/a"}}
	kept := page.scoped([]types.SymbolOccurrenceFile{{File: "internal/a/x.go"}, {File: "internal/ab/x.go"}})
	s.Require().Len(kept, 1)
	s.Equal("internal/a/x.go", kept[0].File)
}

func (s *refsSourceSuite) TestNoPathKeepsTheWholeList() {
	s.Len(occurrencePage{}.scoped(s.sites(4)), 2)
	s.Empty(occurrencePage{}.scopeNote(s.sites(4), 4))
}

func (s *refsSourceSuite) TestAPathThatDoesNotExistIsAUsageError() {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse(nil))
	_, err := refsOccurrencePage(rf, fs, s.w.Root(), []string{"Sym", filepath.Join(s.w.Root(), "AnotherSymbol")})
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "AnotherSymbol")
}

func (s *refsSourceSuite) TestAPathNarrowsThePlainListAndRecounts() {
	out := types.KnowledgeRefsOutput{FileCount: 2, RefCount: 5, Refs: []types.KnowledgeRefSite{{File: "a/x.go", Count: 2}, {File: "b/y.go", Count: 3}}}
	got, note := occurrencePage{scopes: []string{"a"}}.scopedRefs(out)
	s.Equal(types.KnowledgeRefsOutput{FileCount: 1, RefCount: 2, Refs: []types.KnowledgeRefSite{{File: "a/x.go", Count: 2}}}, got)
	s.Equal("a keeps 2 of 5 references", note)
}

func (s *refsSourceSuite) TestNoPathLeavesThePlainListAlone() {
	out := types.KnowledgeRefsOutput{RefCount: 5}
	got, note := occurrencePage{}.scopedRefs(out)
	s.Equal(out, got)
	s.Empty(note)
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
	s.Contains(page.note(5, 37), fmt.Sprintf("refs 'Sym' --occurrences -o json %s --limit 5 --offset 5", path))
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

func (s *refsSourceSuite) TestADefinitionWidensPastItsDocComment() {
	s.w.Write("b.go", "package a\n\n// F is f.\nfunc F() {}\n\nvar x\n")
	sites := []types.KnowledgeDefinitionSite{{File: "b.go", StartLine: 4, Status: types.DefinitionUnverified}}
	checkDefinitions(s.w.Root(), "F", sites, nil, sourceWindow{Show: true, From: -1, To: 1})
	s.Equal("\n// F is f.\nfunc F() {}\n", sites[0].Source)
}

func (s *refsSourceSuite) TestLinesParsesARangeAndImpliesSource() {
	w, err := s.window("--lines=-5:-1")
	s.Require().NoError(err)
	s.Equal(sourceWindow{Show: true, From: -5, To: -1, MaxLines: 200}, w)
}

func (s *refsSourceSuite) TestLinesRefusesAMalformedRange() {
	_, err := s.window("--lines=5")
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "--lines=-5:-1")
}

func (s *refsSourceSuite) TestLinesRefusesABackwardsRange() {
	_, err := s.window("--lines=3:1")
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "smaller number first")
}

func (s *refsSourceSuite) TestLinesRefusesContext() {
	_, err := s.window("--lines=-1:1", "--context", "2")
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "use it alone")
}

func (s *refsSourceSuite) TestLinesRefusesText() {
	_, err := s.window("--lines=-1:1", "--text")
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "--text prints no source")
}

func (s *refsSourceSuite) TestLinesAboveLeavesOutTheMatch() {
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true, From: -2, To: -1})
	s.Equal("one\ntwo", s.files[0].Occurrences[0].Source)
}

func (s *refsSourceSuite) TestLinesBelowLeavesOutTheMatch() {
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true, From: 1, To: 2})
	s.Equal("four\nfive", s.files[0].Occurrences[0].Source)
}

func (s *refsSourceSuite) TestLinesPastTheEndOfAFileSaySo() {
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true, From: 9, To: 12})
	s.Empty(s.files[0].Occurrences[0].Source)
	s.Equal("no lines: 9:12 falls outside the file", s.files[0].Occurrences[0].SourceNote)
}

func (s *refsSourceSuite) TestLinesAboveTheFirstLineSaySo() {
	s.files[0].Occurrences[0].Line, s.files[0].Occurrences[0].EndLine = 1, 1
	fillOccurrenceSources(s.w.Root(), s.files, sourceWindow{Show: true, From: -3, To: -1})
	s.Empty(s.files[0].Occurrences[0].Source)
	s.Equal("no lines: -3:-1 falls outside the file", s.files[0].Occurrences[0].SourceNote)
}

func (s *refsSourceSuite) multiLineDefinition(w sourceWindow) string {
	s.w.Write("c.go", "above\nfunc F() {\n\treturn\n}\nbelow\n")
	sites := []types.KnowledgeDefinitionSite{{File: "c.go", StartLine: 2, EndLine: 4, Status: types.DefinitionUnverified}}
	checkDefinitions(s.w.Root(), "F", sites, nil, w)
	return sites[0].Source
}

func (s *refsSourceSuite) TestLinesZeroIsAMultiLineDefinitionsWholeBody() {
	s.Equal("func F() {\n\treturn\n}", s.multiLineDefinition(sourceWindow{Show: true}))
}

func (s *refsSourceSuite) TestLinesAboveAMultiLineDefinition() {
	s.Equal("above", s.multiLineDefinition(sourceWindow{Show: true, From: -1, To: -1}))
}

func (s *refsSourceSuite) TestLinesBelowCountFromAMultiLineDefinitionsLastLine() {
	s.Equal("below", s.multiLineDefinition(sourceWindow{Show: true, From: 1, To: 1}))
}

func (s *refsSourceSuite) sites(n int) []types.SymbolOccurrenceFile {
	first := types.SymbolOccurrenceFile{File: "a.go"}
	second := types.SymbolOccurrenceFile{File: "b.go"}
	for i := 1; i <= n; i++ {
		if i <= n/2 {
			first.Occurrences = append(first.Occurrences, types.SymbolOccurrence{Line: i})
		} else {
			second.Occurrences = append(second.Occurrences, types.SymbolOccurrence{Line: i})
		}
	}
	return []types.SymbolOccurrenceFile{first, second}
}

func (s *refsSourceSuite) TestAPageCrossesAFile() {
	kept, shown := occurrencePage{limit: 2, offset: 1}.cut(s.sites(4))
	s.Equal(2, shown)
	s.Equal(2, kept[0].Occurrences[0].Line)
	s.Equal("b.go", kept[1].File)
	s.Equal(3, kept[1].Occurrences[0].Line)
}

func (s *refsSourceSuite) TestNoLimitKeepsEverySite() {
	_, shown := occurrencePage{}.cut(s.sites(4))
	s.Equal(4, shown)
}

func (s *refsSourceSuite) TestACutPageNamesTheNextCommand() {
	page := occurrencePage{limit: 5, offset: 5, again: "magus refs 'F' --occurrences"}
	s.Equal("5 of 37 sites; next: magus refs 'F' --occurrences --limit 5 --offset 10", page.note(5, 37))
}

func (s *refsSourceSuite) TestTheLastPageNamesNoNextCommand() {
	s.Equal("2 of 7 sites", occurrencePage{limit: 5, offset: 5}.note(2, 7))
}

func (s *refsSourceSuite) TestAnUncutPageSaysNothing() {
	s.Empty(occurrencePage{}.note(7, 7))
}

func (s *refsSourceSuite) TestOffsetPastTheLastSiteSaysSo() {
	page := occurrencePage{limit: 5, offset: 9}
	kept, shown := page.cut(s.sites(4))
	s.Empty(kept)
	s.Equal("0 of 4 sites; --offset 9 is past the last site", page.note(shown, 4))
}

func (s *refsSourceSuite) TestTheNextCommandKeepsTheWindowFlags() {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse([]string{"--occurrences", "--lines=-5:-1", "--limit", "5"}))
	page, err := refsOccurrencePage(rf, fs, s.w.Root(), []string{"Sym"})
	s.Require().NoError(err)
	s.Contains(page.note(5, 37), "refs 'Sym' --occurrences --lines=-5:-1 --limit 5 --offset 5")
}

func (s *refsSourceSuite) TestOffsetWithoutOccurrencesIsAUsageError() {
	fs := flag.NewFlagSet("refs", flag.ContinueOnError)
	rf := flaggen.BindRefs(fs)
	s.Require().NoError(fs.Parse([]string{"--offset", "5"}))
	_, err := refsOccurrencePage(rf, fs, s.w.Root(), []string{"Sym"})
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *refsSourceSuite) TestOccurrencesPrintTheirBlockUnderTheirSite() {
	refs := types.KnowledgeRefsOutput{Symbol: "symbol:a F().", Label: "F", Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound}}
	s.files[0].Occurrences[0].Status = types.SymbolOccurrenceVerified
	var err error
	got := captureStdout(s.T(), func() {
		err = emitOccurrences(context.Background(), s.w.Root(), output.Options{Format: outputText}, refs,
			&magus.SymbolOccurrenceRead{Files: s.files, Names: []string{"F"}}, sourceWindow{Show: true, To: 1}, occurrencePage{})
	})
	s.Require().NoError(err)
	s.Contains(got, "    3:0-3:0  verified\n      >3  three\n       4  four\n")
}

func TestEmitDefinitionsText(t *testing.T) {
	out := types.KnowledgeDefinitionsOutput{
		Symbol: "symbol:a F().", Label: "F",
		Definitions: []types.KnowledgeDefinitionSite{
			{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified, Source: "func F() {\n\treturn\n}", SourceStartLine: 3},
			{File: "b.go", StartLine: 9, Status: types.DefinitionVerified, Source: "var F = 1", SourceStartLine: 9},
			{File: "c.go"},
		},
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}
	var err error
	got := captureStdout(t, func() {
		err = emitDefinitions(context.Background(), os.Stdout, output.Options{Format: outputText}, out)
	})
	require.NoError(t, err)
	assert.Equal(t, "symbol: symbol:a F().  (F)\n"+
		"a.go:3-5  verified\n>3  func F() {\n 4  \treturn\n 5  }\n"+
		"\nb.go:9  verified  (the index recorded no end line; the declaration line alone)\n>9  var F = 1\n"+
		"c.go  (the index recorded no line in this file)\n", got)
}

func TestWriteRefsText(t *testing.T) {
	out := types.KnowledgeRefsOutput{
		Symbol: "symbol:a F().", Label: "F",
		Defs:      []types.KnowledgeRefSite{{File: "a.go"}},
		Refs:      []types.KnowledgeRefSite{{File: "b.go", Count: 2, Lines: []int{4, 9}}, {File: "c.go", Count: 1}},
		FileCount: 2, RefCount: 3,
		Answer: types.KnowledgeAnswer{Verdict: types.VerdictFound},
	}
	var b bytes.Buffer
	require.NoError(t, writeRefsText(&b, out))
	assert.Equal(t, fmt.Sprintf(`symbol: symbol:a F().  (F)
defined in:
  a.go
referenced in 2 file(s), 3 occurrence(s):
  b.go  (2)%s
  c.go  (1)
`, linesSuffix([]int{4, 9})), b.String())

	// A resolved symbol nothing references is a verified answer, unless the verdict is not.
	out.Refs, out.FileCount, out.RefCount = nil, 0, 0
	b.Reset()
	require.NoError(t, writeRefsText(&b, out))
	assert.Equal(t, "symbol: symbol:a F().  (F)\ndefined in:\n  a.go\nno references found\n", b.String())

	out.Answer.Verdict = types.VerdictUnknown
	b.Reset()
	err := writeRefsText(&b, out)
	assert.True(t, exit.AlreadyReported(err))
	assert.Contains(t, b.String(), "verdict: unknown, not absent")
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
	got := captureStdout(t, func() {
		err = emitDefinitions(context.Background(), os.Stdout, output.Options{Format: outputText}, out)
	})
	assert.Equal(t, exit.Reported(exit.Failure), err)
	assert.Contains(t, got, "a.go:3-5  changed  (edited since indexing; refresh with `")

	got = captureStdout(t, func() {
		err = emitDefinitions(context.Background(), os.Stdout, output.Options{Format: outputName}, out)
	})
	assert.Equal(t, exit.Reported(exit.Failure), err)
	assert.Equal(t, "b.go:1-2\n", got)
}

// A stale index fails a refs answer, but a digest-verified definition is proof of itself:
// the notice still prints, and only an answer with an unverified site exits 1 for it.
func TestEmitDefinitionsStaleIndex(t *testing.T) {
	answer := types.KnowledgeAnswer{Verdict: types.VerdictFound, StaleIndexes: []string{"."}}
	site := types.KnowledgeDefinitionSite{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified}
	out := types.KnowledgeDefinitionsOutput{Symbol: "symbol:a F().", Definitions: []types.KnowledgeDefinitionSite{site}, Answer: answer}

	var err error
	got := captureStdout(t, func() {
		err = emitDefinitions(context.Background(), os.Stdout, output.Options{Format: outputText}, out)
	})
	require.NoError(t, err)
	assert.Contains(t, got, staleIndexNotice(answer))

	out.Definitions[0].Status = types.DefinitionUnverified
	captureStdout(t, func() {
		err = emitDefinitions(context.Background(), os.Stdout, output.Options{Format: outputText}, out)
	})
	assert.Equal(t, exit.Reported(exit.Failure), err)
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

type refsGapsSuite struct {
	suite.Suite
	proto types.KnowledgeAnswer
}

func TestRefsGapsSuite(t *testing.T) { suite.Run(t, new(refsGapsSuite)) }

func (s *refsGapsSuite) SetupTest() {
	s.proto = types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{{
		Project: types.NewProjectRef("proto", ""), Language: "protobuf", State: types.SymbolIndexNotBuilt, Unindexable: true,
	}})
}

// A name only in Go files is not explained by a project holding only protobuf.
func (s *refsGapsSuite) TestAProjectWithNoMatchCannotExplainTheMiss() {
	got := withoutUnheldGaps(s.proto, "/w", []textindex.Match{{Path: "cmd/buzz-playground/main.go", Line: 1}})
	s.Equal(types.VerdictAbsent, got.Verdict)
	s.Empty(got.Gaps)
}

func (s *refsGapsSuite) TestAnAbsoluteMatchInTheProjectKeepsItsGap() {
	got := withoutUnheldGaps(s.proto, "/w", []textindex.Match{{Path: "/w/proto/magus/a.proto", Line: 1}})
	s.Equal(types.VerdictUnknown, got.Verdict)
	s.Len(got.Gaps, 1)
}

func (s *refsGapsSuite) TestAProjectSharingAPrefixHoldsNoMatch() {
	got := withoutUnheldGaps(s.proto, "/w", []textindex.Match{{Path: "protobuf/a.proto", Line: 1}})
	s.Equal(types.VerdictAbsent, got.Verdict)
}

func (s *refsGapsSuite) TestTheRootProjectHoldsEveryMatch() {
	root := types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{{Project: types.NewProjectRef(".", ""), Unindexable: true}})
	s.Equal(types.VerdictUnknown, withoutUnheldGaps(root, "/w", nil).Verdict)
}

func (s *refsGapsSuite) TestAGapABuildClosesStays() {
	unbuilt := types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{{Project: types.NewProjectRef("libs/api", ""), State: types.SymbolIndexNotBuilt}})
	got := withoutUnheldGaps(unbuilt, "/w", nil)
	s.Equal(types.VerdictUnknown, got.Verdict)
	s.Len(got.Gaps, 1)
}

// A name refs cannot resolve, from a workspace with an unbuilt index: -o json prints the
// record with its verdict and index cause on stdout, and the text block on stderr carries
// the same why and fix in every format.
func TestReportRefsMissCarriesTheIndexCause(t *testing.T) {
	ans := types.ClassifyAnswer(false, "", []types.KnowledgeSymbolGap{{Project: types.NewProjectRef("libs/api", ""), State: types.SymbolIndexNotBuilt}})
	ans.IndexCause = &types.KnowledgeIndexCause{
		Why: "no server is running, so the refresh hook's `magus job run sync-graph` does nothing",
		Fix: "`magus server start` keeps it current from now on",
	}
	miss := refsMiss{name: "Foo", answer: ans}

	var stderr bytes.Buffer
	var err error
	stdout := captureStdout(t, func() { err = reportRefsMiss(&stderr, output.Options{Format: outputJSON}, miss) })
	require.NoError(t, err)
	var record types.KnowledgeRefsOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &record), "stdout: %q", stdout)
	assert.Equal(t, types.KnowledgeRefsOutput{
		Definition:    record.Definition,    // the noun's prose, which the describe catalog pins
		SchemaVersion: record.SchemaVersion, // the wire version, pinned by its own test
		Symbol:        "Foo",
		Answer: types.KnowledgeAnswer{
			Verdict:    types.VerdictUnknown,
			Reason:     types.ReasonSymbolIndexMissing,
			Gaps:       record.Answer.Gaps, // the gap's own wire form is pinned by the stderr block below
			IndexCause: ans.IndexCause,
		},
	}, record)
	assert.Contains(t, stdout, `"index_cause"`)
	assert.Contains(t, stderr.String(), fmt.Sprintf("why: %s\n", ans.IndexCause.Why))
	assert.Contains(t, stderr.String(), fmt.Sprintf("fix: %s\n", ans.IndexCause.Fix))

	stderr.Reset()
	stdout = captureStdout(t, func() { err = reportRefsMiss(&stderr, output.Options{Format: outputText}, miss) })
	require.NoError(t, err)
	assert.Empty(t, stdout, "text output keeps stdout for answers")
	assert.Contains(t, stderr.String(), `magus refs: no node matches "Foo"`)
	assert.Contains(t, stderr.String(), "outside coverage: libs/api (not-indexed)")
	assert.Contains(t, stderr.String(), fmt.Sprintf(`  why: %s
  fix: %s
`, ans.IndexCause.Why, ans.IndexCause.Fix))
}

func TestPresenceOfCapsSitesAndCountsTheRest(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "ws")
	var matches []textindex.Match
	for i := 1; i <= types.TextPresenceSiteCap+3; i++ {
		matches = append(matches, textindex.Match{Path: filepath.Join(root, "pkg", "a.go"), Line: i})
	}
	matches = append(matches, textindex.Match{Path: filepath.Join(root, "b.go"), Line: 7})

	sites := make([]string, types.TextPresenceSiteCap)
	for i := range sites {
		sites[i] = fmt.Sprintf("pkg/a.go:%d", i+1)
	}
	assert.Equal(t, types.KnowledgeTextPresence{Hits: types.TextPresenceSiteCap + 4, Files: 2, Sites: sites, More: 4},
		missPresence(root, matches))
}

// A symbol miss with text hits names the sites in the text block and in the -o json record.
func TestReportRefsMissPrintsTextSites(t *testing.T) {
	ans := types.ClassifyAnswer(false, "", nil)
	ans.Text = &types.KnowledgeTextPresence{Hits: 3, Files: 2, Sites: []string{"a/b.go:4", "c.go:9"}, More: 1}
	miss := refsMiss{name: "Foo", answer: ans, searched: 10}

	var stderr bytes.Buffer
	var err error
	stdout := captureStdout(t, func() { err = reportRefsMiss(&stderr, output.Options{Format: outputText}, miss) })
	require.NoError(t, err)
	assert.Empty(t, stdout)
	assert.Contains(t, stderr.String(), "    a/b.go:4\n    c.go:9\n    ... and 1 more\n")

	stderr.Reset()
	stdout = captureStdout(t, func() { err = reportRefsMiss(&stderr, output.Options{Format: outputJSON}, miss) })
	require.NoError(t, err)
	var record types.KnowledgeRefsOutput
	require.NoError(t, json.Unmarshal([]byte(stdout), &record), "stdout: %q", stdout)
	assert.Equal(t, ans.Text, record.Answer.Text)
}

// An absent verdict with no text match says what the text search did not read.
func TestReportRefsMissNamesTheSkippedFilesUnderAbsent(t *testing.T) {
	miss := refsMiss{name: "Foo", answer: types.ClassifyAnswer(false, "", nil), searched: 10, notes: textPresenceNotes(6, 0, 0, true, false)}
	var stderr bytes.Buffer
	require.NoError(t, reportRefsMiss(&stderr, output.Options{Format: outputText}, miss))
	assert.Contains(t, stderr.String(), "verdict: absent")
	assert.Contains(t, stderr.String(), "no text match in 10 file(s) searched (6 skipped: binary, empty, or over")
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
