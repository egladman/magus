package knowledge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
)

type refsPageSuite struct {
	suite.Suite
	root string
}

func TestRefsPageSuite(t *testing.T) { suite.Run(t, new(refsPageSuite)) }

func (s *refsPageSuite) SetupTest() { s.root = s.T().TempDir() }

func (s *refsPageSuite) sites(n int) []types.SymbolOccurrenceFile {
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

func (s *refsPageSuite) TestATrailingPathKeepsOneFile() {
	path := filepath.Join(s.root, "a.go")
	s.Require().NoError(os.WriteFile(path, []byte("package a\n"), 0o644))
	page, err := NewRefsPage(RefsPageOptions{Occurrences: true, Root: s.root, ScopeArgs: []string{path}})
	s.Require().NoError(err)
	s.Len(page.Scoped(s.sites(4)), 1)
	s.Equal("a.go keeps 2 of 4 sites", page.scopeNote(s.sites(4), 2))
}

func (s *refsPageSuite) TestNoPathKeepsTheWholeList() {
	s.Len(RefsPage{}.Scoped(s.sites(4)), 2)
	s.Empty(RefsPage{}.scopeNote(s.sites(4), 4))
}

func (s *refsPageSuite) TestAPathSaysWhatThePlainListKept() {
	page := RefsPage{OccurrencePage: OccurrencePage{Scopes: []string{"a"}}}
	s.Equal("a keeps 2 of 5 references", page.RefsNote(2, 5))
}

func (s *refsPageSuite) TestAPathThatDoesNotExistIsAUsageError() {
	_, err := NewRefsPage(RefsPageOptions{Root: s.root, ScopeArgs: []string{filepath.Join(s.root, "AnotherSymbol")}})
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "AnotherSymbol")
}

func (s *refsPageSuite) TestOffsetWithoutOccurrencesIsAUsageError() {
	_, err := NewRefsPage(RefsPageOptions{OffsetGiven: true, Offset: 5, Root: s.root})
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *refsPageSuite) TestANegativeLimitIsAUsageError() {
	_, err := NewRefsPage(RefsPageOptions{Occurrences: true, Limit: -1, Root: s.root})
	s.Equal(exit.Usage, exit.Code(err))
}

func (s *refsPageSuite) TestTextTakesNoPage() {
	page, err := NewRefsPage(RefsPageOptions{Text: true, Limit: -1, Root: s.root, ScopeArgs: []string{"nope"}})
	s.Require().NoError(err)
	s.Equal(RefsPage{}, page)
}

func (s *refsPageSuite) TestACutPageNamesTheNextCommand() {
	page := RefsPage{OccurrencePage: OccurrencePage{Limit: 5, Offset: 5}, Again: "magus refs 'F' --occurrences"}
	s.Equal("5 of 37 sites; next: magus refs 'F' --occurrences --limit 5 --offset 10", page.note(5, 37))
}

func (s *refsPageSuite) TestTheLastPageNamesNoNextCommand() {
	s.Equal("2 of 7 sites", RefsPage{OccurrencePage: OccurrencePage{Limit: 5, Offset: 5}}.note(2, 7))
}

func (s *refsPageSuite) TestAnUncutPageSaysNothing() {
	s.Empty(RefsPage{}.note(7, 7))
}

func (s *refsPageSuite) TestOffsetPastTheLastSiteSaysSo() {
	page := RefsPage{OccurrencePage: OccurrencePage{Limit: 5, Offset: 9}}
	kept, shown := page.Cut(s.sites(4))
	s.Empty(kept)
	s.Equal("0 of 4 sites; --offset 9 is past the last site", page.note(shown, 4))
}
