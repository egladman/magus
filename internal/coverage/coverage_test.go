package coverage

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/spells"
)

type ReportSuite struct{ suite.Suite }

func TestReportSuite(t *testing.T) { suite.Run(t, new(ReportSuite)) }

const testModule = "github.com/egladman/magus"

func (s *ReportSuite) TestGoProfileAggregatesPerFile() {
	profile := []byte("mode: atomic\n" +
		"github.com/egladman/magus/internal/foo.go:10.2,12.16 3 1\n" +
		"github.com/egladman/magus/internal/foo.go:20.2,20.10 2 0\n" +
		"github.com/egladman/magus/internal/bar.go:5.2,6.3 1 0\n")

	s.Equal([]File{
		{Path: "internal/bar.go", Total: 1, Blocks: []Block{{StartLine: 5, EndLine: 6, Units: 1}}},
		{Path: "internal/foo.go", Covered: 3, Total: 5, Blocks: []Block{
			{StartLine: 10, EndLine: 12, Units: 3, Hits: 1},
			{StartLine: 20, EndLine: 20, Units: 2},
		}},
	}, ParseGoProfile(profile, testModule))
}

func (s *ReportSuite) TestGoProfileDropsForeignAndMalformedLines() {
	profile := []byte("mode: set\n" +
		"other.com/pkg/x.go:1.1,2.2 1 1\n" +
		"github.com/egladman/magus/ok.go:1.1,2.2 1 1\n" +
		"garbage line without colon location\n" +
		"github.com/egladman/magus/bad.go:1.1,2.2 notanum 1\n")

	s.Equal([]File{{Path: "ok.go", Covered: 1, Total: 1, Blocks: []Block{{StartLine: 1, EndLine: 2, Units: 1, Hits: 1}}}},
		ParseGoProfile(profile, testModule))
}

func (s *ReportSuite) TestGoProfileWithNoModuleReadsNothing() {
	s.Nil(ParseGoProfile([]byte("mode: atomic\ngithub.com/egladman/magus/x.go:1.1,2.2 1 1\n"), ""))
}

func (s *ReportSuite) TestLCOVCountsEachDALineOnce() {
	trace := []byte("TN:\nSF:src/a.ts\nFN:1,a\nDA:1,4\nDA:2,0\nDA:3,1,abc\nend_of_record\nSF:src/b.ts\nDA:7,0\nend_of_record\n")

	s.Equal([]File{
		{Path: "src/a.ts", Covered: 2, Total: 3, Blocks: []Block{
			{StartLine: 1, EndLine: 1, Units: 1, Hits: 4},
			{StartLine: 2, EndLine: 2, Units: 1},
			{StartLine: 3, EndLine: 3, Units: 1, Hits: 1},
		}},
		{Path: "src/b.ts", Total: 1, Blocks: []Block{{StartLine: 7, EndLine: 7, Units: 1}}},
	}, ParseLCOV(trace))
}

func (s *ReportSuite) TestLCOVSkipsADALineOutsideAFile() {
	s.Nil(ParseLCOV([]byte("DA:1,1\nend_of_record\n")))
}

func (s *ReportSuite) TestLCOVSkipsAMalformedDALine() {
	s.Equal([]File{{Path: "a.py", Total: 1, Blocks: []Block{{StartLine: 2, EndLine: 2, Units: 1}}}},
		ParseLCOV([]byte("SF:a.py\nDA:x,1\nDA:2,0\nend_of_record\n")))
}

func (s *ReportSuite) TestParseRefusesNoFormat() {
	_, err := Parse(spells.CoverageFormatNone, nil, "")
	s.EqualError(err, `coverage format "unset" is not one magus reads (lcov, go-coverprofile)`)
}

func (s *ReportSuite) TestParseReadsLCOV() {
	got, err := Parse(spells.CoverageFormatLCOV, []byte("SF:a.rs\nDA:1,1\nend_of_record\n"), "")
	s.Require().NoError(err)
	s.Equal([]File{{Path: "a.rs", Covered: 1, Total: 1, Blocks: []Block{{StartLine: 1, EndLine: 1, Units: 1, Hits: 1}}}}, got)
}

func (s *ReportSuite) TestParseReadsAGoProfile() {
	got, err := Parse(spells.CoverageFormatGoProfile, []byte("mode: set\nm/a.go:1.1,3.2 2 0\n"), "m")
	s.Require().NoError(err)
	s.Equal([]File{{Path: "a.go", Total: 2, Blocks: []Block{{StartLine: 1, EndLine: 3, Units: 2}}}}, got)
}

func (s *ReportSuite) TestSpanCountsBlocksStartingInside() {
	f := File{Blocks: []Block{
		{StartLine: 4, EndLine: 5, Units: 2, Hits: 1},
		{StartLine: 10, EndLine: 12, Units: 3, Hits: 1},
		{StartLine: 13, EndLine: 13, Units: 1},
		{StartLine: 21, EndLine: 22, Units: 4, Hits: 1},
	}}

	covered, coverable := f.Span(10, 20)

	s.Equal([2]int{3, 4}, [2]int{covered, coverable})
}

func (s *ReportSuite) TestRebaseJoinsARelativePathToItsProject() {
	got := Rebase([]File{{Path: "src/a.ts"}}, "/ws", "web")
	s.Equal([]File{{Path: "web/src/a.ts"}}, got)
}

func (s *ReportSuite) TestRebaseMakesAnAbsolutePathWorkspaceRelative() {
	got := Rebase([]File{{Path: "/ws/web/src/a.ts"}}, "/ws", "web")
	s.Equal([]File{{Path: "web/src/a.ts"}}, got)
}

func (s *ReportSuite) TestRebaseDropsAPathOutsideTheWorkspace() {
	s.Empty(Rebase([]File{{Path: "/elsewhere/a.ts"}}, "/ws", "web"))
}

func (s *ReportSuite) TestDirIsPerProjectUnderTheCache() {
	s.Equal(filepath.Join("/c", "coverage", "5feceb66ffc86f38"), Dir("/c", "0"))
	s.NotEqual(Dir("/c", "web"), Dir("/c", "api"))
}

func (s *ReportSuite) TestParseRefusesAnLCOVReportWithNoRecord() {
	_, err := Parse(spells.CoverageFormatLCOV, []byte("<html>not a report</html>\n"), "")
	s.EqualError(err, "the report holds 26 bytes and no lcov record")
}

func (s *ReportSuite) TestParseRefusesAGoProfileWithNoBlock() {
	_, err := Parse(spells.CoverageFormatGoProfile, []byte("not a profile\n"), "m")
	s.EqualError(err, "the report holds 14 bytes and no go-coverprofile record")
}

func (s *ReportSuite) TestParseAcceptsAnEmptyReport() {
	got, err := Parse(spells.CoverageFormatLCOV, []byte("\n"), "")
	s.Require().NoError(err)
	s.Nil(got)
}

func (s *ReportSuite) TestParseAcceptsAGoProfileWithOnlyItsModeLine() {
	got, err := Parse(spells.CoverageFormatGoProfile, []byte("mode: set\n"), "m")
	s.Require().NoError(err)
	s.Nil(got)
}

func (s *ReportSuite) TestParseKeepsAGoProfileWhoseBlocksAreAllForeign() {
	got, err := Parse(spells.CoverageFormatGoProfile, []byte("mode: set\nother/a.go:1.1,3.2 2 0\n"), "m")
	s.Require().NoError(err)
	s.Nil(got)
}
