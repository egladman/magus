package knowledge

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type sourceWindowSuite struct {
	suite.Suite
	root string
}

func TestSourceWindowSuite(t *testing.T) { suite.Run(t, new(sourceWindowSuite)) }

func (s *sourceWindowSuite) SetupTest() { s.root = s.T().TempDir() }

func (s *sourceWindowSuite) write(name, content string) string {
	path := filepath.Join(s.root, name)
	s.Require().NoError(os.WriteFile(path, []byte(content), 0o644))
	return path
}

func (s *sourceWindowSuite) lines(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte{byte('a' + i)}
	}
	return out
}

func (s *sourceWindowSuite) TestAnUncutBlockCarriesNoTotal() {
	got := SourceWindow{MaxLines: 200}.Text(s.lines(3), 0, 3)
	s.Equal(SourceBlock{Text: "a\nb\nc", StartLine: 1}, got)
}

func (s *sourceWindowSuite) TestACutBlockCarriesTheWindowsLength() {
	got := SourceWindow{MaxLines: 2}.Text(s.lines(5), 0, 5)
	s.Equal(SourceBlock{Text: "a\nb", StartLine: 1, Total: 5}, got)
}

func (s *sourceWindowSuite) TestASkipCarriesTheLineNumbersOn() {
	got := SourceWindow{MaxLines: 2, SkipLines: 2}.Text(s.lines(5), 0, 5)
	s.Equal(SourceBlock{Text: "c\nd", StartLine: 3, Total: 5}, got)
}

func (s *sourceWindowSuite) TestASkipPastTheEndKeepsOnlyTheTotal() {
	s.Equal(SourceBlock{Total: 3}, SourceWindow{SkipLines: 9}.Text(s.lines(3), 0, 3))
}

func (s *sourceWindowSuite) TestARangeOutsideTheFileKeepsNothing() {
	s.Equal(SourceBlock{}, SourceWindow{From: 9, To: 12}.Text(s.lines(3), 0, 1))
}

func (s *sourceWindowSuite) TestTheElementAfterAFinalNewlineIsNoLine() {
	got := SourceWindow{To: 9}.Text(types.SplitSourceLines([]byte("a\nb\n")), 0, 1)
	s.Equal("a\nb", got.Text)
}

func (s *sourceWindowSuite) TestFillOccurrencesSetsTheSource() {
	s.write("a.go", "one\ntwo\nthree\n")
	files := []types.SymbolOccurrenceFile{{File: "a.go", Occurrences: []types.SymbolOccurrence{{Line: 2, EndLine: 2}}}}
	SourceWindow{Show: true, From: -1, To: 0}.FillOccurrences(s.root, files)
	s.Equal(types.SymbolOccurrence{Line: 2, EndLine: 2, Source: "one\ntwo", SourceStartLine: 1}, files[0].Occurrences[0])
}

func (s *sourceWindowSuite) TestFillOccurrencesNamesAFileItCannotRead() {
	files := []types.SymbolOccurrenceFile{{File: "gone.go", Occurrences: []types.SymbolOccurrence{{Line: 1}}}}
	SourceWindow{Show: true}.FillOccurrences(s.root, files)
	s.Equal("no lines: cannot read gone.go", files[0].Occurrences[0].SourceNote)
}

// These statuses stand between a recorded range and a wrong edit.
func (s *sourceWindowSuite) TestCheckDefinitionsJudgesEachSite() {
	path := s.write("a.go", "package a\n\nfunc F() {\n\treturn\n}\n\nfunc FF() {}\n")
	s.write("b.go", "package a\n\nfunc F() {\n\treturn\n}\n")
	info, err := os.Stat(path)
	s.Require().NoError(err)
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
	SourceWindow{Show: true}.CheckDefinitions(s.root, "F", sites, indexedAt)

	fn := "func F() {\n\treturn\n}"
	s.Equal([]types.KnowledgeDefinitionSite{
		{File: "a.go", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified, Source: fn, SourceStartLine: 3},
		{File: "b.go", StartLine: 3, EndLine: 5, Status: types.DefinitionUnverified, Source: fn, SourceStartLine: 3},
		{File: "a.go", StartLine: 7, Status: types.DefinitionChanged, Source: "func FF() {}", SourceStartLine: 7},
		{File: "a.go", StartLine: 3, EndLine: 40, Status: types.DefinitionUnreadable},
		{File: "gone.go", StartLine: 1, Status: types.DefinitionUnreadable},
		{File: "other.go", Status: types.DefinitionUnverified},
	}, sites)
}

// A blank line ends the comment block a definition's source carries.
func (s *sourceWindowSuite) TestCheckDefinitionsIncludesTheDocComment() {
	s.write("a.go", "package a\n\n// unrelated\n\n// F splits a letter from a digit.\n// TestF pins it.\nfunc F() {}\n")
	sites := []types.KnowledgeDefinitionSite{{File: "a.go", StartLine: 7, Status: types.DefinitionUnverified}}
	SourceWindow{Show: true}.CheckDefinitions(s.root, "F", sites, nil)
	s.Equal("// F splits a letter from a digit.\n// TestF pins it.\nfunc F() {}", sites[0].Source)
}

type occurrencePageSuite struct{ suite.Suite }

func TestOccurrencePageSuite(t *testing.T) { suite.Run(t, new(occurrencePageSuite)) }

func (s *occurrencePageSuite) sites(n int) []types.SymbolOccurrenceFile {
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

func (s *occurrencePageSuite) TestNewOccurrencePageMakesScopesRelative() {
	page, err := NewOccurrencePage("/w", []string{"/w/internal/a"}, 5, 2)
	s.Require().NoError(err)
	s.Equal(OccurrencePage{Scopes: []string{"internal/a"}, Limit: 5, Offset: 2}, page)
}

func (s *occurrencePageSuite) TestAScopeKeepsADirectoryAndNotASharedPrefix() {
	page := OccurrencePage{Scopes: []string{"internal/a"}}
	kept := page.Scoped([]types.SymbolOccurrenceFile{{File: "internal/a/x.go"}, {File: "internal/ab/x.go"}})
	s.Require().Len(kept, 1)
	s.Equal("internal/a/x.go", kept[0].File)
}

func (s *occurrencePageSuite) TestNoScopeKeepsEveryFile() {
	s.Len(OccurrencePage{}.Scoped(s.sites(4)), 2)
}

func (s *occurrencePageSuite) TestAScopeNarrowsThePlainListAndRecounts() {
	out := types.KnowledgeRefsOutput{FileCount: 2, RefCount: 5, Refs: []types.KnowledgeRefSite{{File: "a/x.go", Count: 2}, {File: "b/y.go", Count: 3}}}
	got := OccurrencePage{Scopes: []string{"a"}}.ScopedRefs(out)
	s.Equal(types.KnowledgeRefsOutput{FileCount: 1, RefCount: 2, Refs: []types.KnowledgeRefSite{{File: "a/x.go", Count: 2}}}, got)
}

func (s *occurrencePageSuite) TestNoScopeLeavesThePlainListAlone() {
	out := types.KnowledgeRefsOutput{RefCount: 5}
	s.Equal(out, OccurrencePage{}.ScopedRefs(out))
}

func (s *occurrencePageSuite) TestAPageCrossesAFile() {
	kept, shown := OccurrencePage{Limit: 2, Offset: 1}.Cut(s.sites(4))
	s.Equal(2, shown)
	s.Equal(2, kept[0].Occurrences[0].Line)
	s.Equal("b.go", kept[1].File)
	s.Equal(3, kept[1].Occurrences[0].Line)
}

func (s *occurrencePageSuite) TestNoLimitKeepsEverySite() {
	_, shown := OccurrencePage{}.Cut(s.sites(4))
	s.Equal(4, shown)
}

func (s *occurrencePageSuite) TestAnOffsetPastTheLastSiteKeepsNothing() {
	kept, shown := OccurrencePage{Limit: 5, Offset: 9}.Cut(s.sites(4))
	s.Empty(kept)
	s.Zero(shown)
}
