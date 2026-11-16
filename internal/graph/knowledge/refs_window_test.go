package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/libs/codec"
	"github.com/egladman/magus/types"
)

type refsWindowSuite struct {
	suite.Suite
	root  string
	files []types.SymbolOccurrenceFile
}

func TestRefsWindowSuite(t *testing.T) { suite.Run(t, new(refsWindowSuite)) }

func (s *refsWindowSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.write("a.go", "one\ntwo\nthree\nfour\nfive\n")
	s.files = []types.SymbolOccurrenceFile{{File: "a.go", Occurrences: []types.SymbolOccurrence{{Line: 3, EndLine: 3}}}}
}

func (s *refsWindowSuite) write(name, content string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, name), []byte(content), 0o644))
}

func (s *refsWindowSuite) TestBeforeAndAfterImplySourceAlone() {
	w, err := NewRefsWindow(RefsWindowOptions{Before: 5, After: 4, BeforeGiven: true, AfterGiven: true, Sides: true, MaxLines: 200})
	s.Require().NoError(err)
	s.Equal(RefsWindow{SourceWindow: SourceWindow{Show: true, From: -5, To: 4, MaxLines: 200}}, w)
}

func (s *refsWindowSuite) TestNoWindowFlagsPrintNoSource() {
	w, err := NewRefsWindow(RefsWindowOptions{MaxLines: 200})
	s.Require().NoError(err)
	s.False(w.Show)
}

func (s *refsWindowSuite) TestANegativeCapIsAUsageError() {
	_, err := NewRefsWindow(RefsWindowOptions{MaxLines: -1, Paged: true})
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *refsWindowSuite) TestANegativeWindowIsAUsageError() {
	_, err := NewRefsWindow(RefsWindowOptions{Context: -1, Sides: true, MaxLines: 200})
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *refsWindowSuite) TestLinesParsesARangeAndImpliesSource() {
	w, err := NewRefsWindow(RefsWindowOptions{Lines: "-5:-1", Ranged: true, MaxLines: 200})
	s.Require().NoError(err)
	s.Equal(RefsWindow{SourceWindow: SourceWindow{Show: true, From: -5, To: -1, MaxLines: 200}}, w)
}

func (s *refsWindowSuite) TestLinesRefusesAMalformedRange() {
	_, err := NewRefsWindow(RefsWindowOptions{Lines: "5", Ranged: true})
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "--lines=-5:-1")
}

func (s *refsWindowSuite) TestLinesRefusesABackwardsRange() {
	_, err := NewRefsWindow(RefsWindowOptions{Lines: "3:1", Ranged: true})
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "smaller number first")
}

func (s *refsWindowSuite) TestLinesRefusesContext() {
	_, err := NewRefsWindow(RefsWindowOptions{Lines: "-1:1", Ranged: true, Context: 2, Sides: true})
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "use it alone")
}

func (s *refsWindowSuite) TestLinesRefusesText() {
	_, err := NewRefsWindow(RefsWindowOptions{Lines: "-1:1", Ranged: true, Text: true})
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "--text prints no source")
}

func (s *refsWindowSuite) long(w RefsWindow) types.SymbolOccurrence {
	s.write("long.go", "1\n2\n3\n4\n5\n6\n")
	files := []types.SymbolOccurrenceFile{{File: "long.go", Occurrences: []types.SymbolOccurrence{{Line: 1, EndLine: 6}}}}
	w.fillSources(s.root, files)
	return files[0].Occurrences[0]
}

func (s *refsWindowSuite) TestABodyShorterThanTheCapIsNotCut() {
	occ := s.long(RefsWindow{SourceWindow: SourceWindow{Show: true, MaxLines: 200}})
	s.Equal(types.SymbolOccurrence{Line: 1, EndLine: 6, Source: "1\n2\n3\n4\n5\n6", SourceStartLine: 1}, occ)
}

func (s *refsWindowSuite) TestACutBodySaysSoAndNamesTheNextCommand() {
	occ := s.long(RefsWindow{SourceWindow: SourceWindow{Show: true, MaxLines: 2}, Again: "magus refs 'F' --source"})
	s.Equal(types.SymbolOccurrence{Line: 1, EndLine: 6, Source: "1\n2", SourceStartLine: 1, SourceTotalLines: 6,
		SourceNote: "2 of 6 lines; next: magus refs 'F' --source --max-lines 2 --skip-lines 2"}, occ)
}

func (s *refsWindowSuite) TestSkippedLinesCarryTheLineNumbersOn() {
	occ := s.long(RefsWindow{SourceWindow: SourceWindow{Show: true, MaxLines: 2, SkipLines: 2}, Again: "x"})
	s.Equal(types.SymbolOccurrence{Line: 1, EndLine: 6, Source: "3\n4", SourceStartLine: 3, SourceTotalLines: 6,
		SourceNote: "2 of 6 lines; next: x --max-lines 2 --skip-lines 4"}, occ)
}

func (s *refsWindowSuite) TestTheLastBodyPageNamesNoNextCommand() {
	occ := s.long(RefsWindow{SourceWindow: SourceWindow{Show: true, MaxLines: 2, SkipLines: 4}})
	s.Equal(types.SymbolOccurrence{Line: 1, EndLine: 6, Source: "5\n6", SourceStartLine: 5, SourceTotalLines: 6, SourceNote: "2 of 6 lines"}, occ)
}

func (s *refsWindowSuite) TestASkipPastTheEndSaysSo() {
	occ := s.long(RefsWindow{SourceWindow: SourceWindow{Show: true, SkipLines: 9}})
	s.Equal(types.SymbolOccurrence{Line: 1, EndLine: 6, SourceTotalLines: 6, SourceNote: "no lines: --skip-lines 9 is past the last of 6 lines"}, occ)
}

func (s *refsWindowSuite) TestMaxLinesZeroPrintsEverything() {
	occ := s.long(RefsWindow{SourceWindow: SourceWindow{Show: true}})
	s.Equal(types.SymbolOccurrence{Line: 1, EndLine: 6, Source: "1\n2\n3\n4\n5\n6", SourceStartLine: 1}, occ)
}

func (s *refsWindowSuite) TestTheCapAppliesToALinesRange() {
	occ := s.long(RefsWindow{SourceWindow: SourceWindow{Show: true, From: 0, To: 0, MaxLines: 1}, Again: "x"})
	s.Equal(types.SymbolOccurrence{Line: 1, EndLine: 6, Source: "1", SourceStartLine: 1, SourceTotalLines: 6,
		SourceNote: "1 of 6 lines; next: x --max-lines 1 --skip-lines 1"}, occ)
}

func (s *refsWindowSuite) TestAnOccurrencePrintsItsOwnLine() {
	RefsWindow{SourceWindow: SourceWindow{Show: true}}.fillSources(s.root, s.files)
	s.Equal("three", s.files[0].Occurrences[0].Source)
}

func (s *refsWindowSuite) TestBeforeAndAfterWidenTheBlock() {
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: -1, To: 1}}.fillSources(s.root, s.files)
	s.Equal("two\nthree\nfour", s.files[0].Occurrences[0].Source)
}

func (s *refsWindowSuite) TestTheWindowClampsToTheFile() {
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: -9, To: 9}}.fillSources(s.root, s.files)
	s.Equal("one\ntwo\nthree\nfour\nfive", s.files[0].Occurrences[0].Source)
}

func (s *refsWindowSuite) TestAnUnreadableFileSaysSo() {
	s.files[0].File = "gone.go"
	RefsWindow{SourceWindow: SourceWindow{Show: true}}.fillSources(s.root, s.files)
	s.Empty(s.files[0].Occurrences[0].Source)
	s.Equal("no lines: cannot read gone.go", s.files[0].Occurrences[0].SourceNote)
}

func (s *refsWindowSuite) TestSourceRecordsItsFirstLine() {
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: -1, To: 0}}.fillSources(s.root, s.files)
	s.Equal(2, s.files[0].Occurrences[0].SourceStartLine)
}

func (s *refsWindowSuite) TestABlankLineIsALineAndNotAnEmptyRange() {
	s.write("d.go", "one\n\nthree\n")
	s.files[0].File = "d.go"
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: -1, To: -1}}.fillSources(s.root, s.files)
	occ := s.files[0].Occurrences[0]
	s.Empty(occ.Source)
	s.Equal(2, occ.SourceStartLine)
	s.Empty(occ.SourceNote)
}

func (s *refsWindowSuite) TestAnEmptyRangeReachesJSON() {
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: 9, To: 12}}.fillSources(s.root, s.files)
	got, err := codec.Marshal(s.files[0].Occurrences[0])
	s.Require().NoError(err)
	s.Contains(string(got), `"source_note":"no lines: 9:12 falls outside the file"`)
}

func (s *refsWindowSuite) TestLinesAboveLeavesOutTheMatch() {
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: -2, To: -1}}.fillSources(s.root, s.files)
	s.Equal("one\ntwo", s.files[0].Occurrences[0].Source)
}

func (s *refsWindowSuite) TestLinesBelowLeavesOutTheMatch() {
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: 1, To: 2}}.fillSources(s.root, s.files)
	s.Equal("four\nfive", s.files[0].Occurrences[0].Source)
}

func (s *refsWindowSuite) TestLinesPastTheEndOfAFileSaySo() {
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: 9, To: 12}}.fillSources(s.root, s.files)
	s.Empty(s.files[0].Occurrences[0].Source)
	s.Equal("no lines: 9:12 falls outside the file", s.files[0].Occurrences[0].SourceNote)
}

func (s *refsWindowSuite) TestLinesAboveTheFirstLineSaySo() {
	s.files[0].Occurrences[0].Line, s.files[0].Occurrences[0].EndLine = 1, 1
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: -3, To: -1}}.fillSources(s.root, s.files)
	s.Empty(s.files[0].Occurrences[0].Source)
	s.Equal("no lines: -3:-1 falls outside the file", s.files[0].Occurrences[0].SourceNote)
}

func (s *refsWindowSuite) TestADefinitionWidensPastItsDocComment() {
	s.write("b.go", "package a\n\n// F is f.\nfunc F() {}\n\nvar x\n")
	sites := []types.KnowledgeDefinitionSite{{File: "b.go", StartLine: 4, Status: types.DefinitionUnverified}}
	RefsWindow{SourceWindow: SourceWindow{Show: true, From: -1, To: 1}}.CheckSites(s.root, "F", sites, nil)
	s.Equal("\n// F is f.\nfunc F() {}\n", sites[0].Source)
}

func (s *refsWindowSuite) multiLineDefinition(w RefsWindow) string {
	s.write("c.go", "above\nfunc F() {\n\treturn\n}\nbelow\n")
	sites := []types.KnowledgeDefinitionSite{{File: "c.go", StartLine: 2, EndLine: 4, Status: types.DefinitionUnverified}}
	w.CheckSites(s.root, "F", sites, nil)
	return sites[0].Source
}

func (s *refsWindowSuite) TestLinesZeroIsAMultiLineDefinitionsWholeBody() {
	s.Equal("func F() {\n\treturn\n}", s.multiLineDefinition(RefsWindow{SourceWindow: SourceWindow{Show: true}}))
}

func (s *refsWindowSuite) TestLinesAboveAMultiLineDefinition() {
	s.Equal("above", s.multiLineDefinition(RefsWindow{SourceWindow: SourceWindow{Show: true, From: -1, To: -1}}))
}

func (s *refsWindowSuite) TestLinesBelowCountFromAMultiLineDefinitionsLastLine() {
	s.Equal("below", s.multiLineDefinition(RefsWindow{SourceWindow: SourceWindow{Show: true, From: 1, To: 1}}))
}
