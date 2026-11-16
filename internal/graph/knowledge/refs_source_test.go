package knowledge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/clikit/exit"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/diff"
)

type refsSourceSuite struct {
	suite.Suite
	root string
}

func TestRefsSourceSuite(t *testing.T) { suite.Run(t, new(refsSourceSuite)) }

func (s *refsSourceSuite) SetupTest() { s.root = s.T().TempDir() }

func (s *refsSourceSuite) write(name, content string) {
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, name), []byte(content), 0o644))
}

func (s *refsSourceSuite) section(attrs map[string]string) types.KnowledgeNode {
	return types.KnowledgeNode{ID: "docsection:d.md#b", Kind: types.KindDocSection, Source: "d.md#b", Attrs: attrs}
}

var showWindow = RefsWindow{SourceWindow: SourceWindow{Show: true}}

func (s *refsSourceSuite) TestADocSectionPrintsItsExtent() {
	s.write("d.md", "# A\n\n## B\n\nbody\n\n# C\n")
	sum := sha256.Sum256([]byte("## B\n\nbody"))
	site, err := DocSectionSource(s.root, s.section(map[string]string{"start_line": "3", "end_line": "5", "digest": hex.EncodeToString(sum[:])}), showWindow)
	s.Require().NoError(err)
	s.Equal(types.KnowledgeDefinitionSite{File: "d.md", StartLine: 3, EndLine: 5, Status: types.DefinitionVerified, Source: "## B\n\nbody", SourceStartLine: 3}, site)
}

func (s *refsSourceSuite) TestAnEditedDocSectionSaysChanged() {
	s.write("d.md", "# A\n\n## B\n\nedited\n\n# C\n")
	site, err := DocSectionSource(s.root, s.section(map[string]string{"start_line": "3", "end_line": "5", "digest": "old"}), showWindow)
	s.Require().NoError(err)
	s.Equal(types.DefinitionChanged, site.Status)
	s.Equal(exit.Failure, exit.Code(DocSectionExit(site)))
}

func (s *refsSourceSuite) TestADocSectionWithNoExtentSaysSo() {
	site, err := DocSectionSource(s.root, s.section(nil), showWindow)
	s.Require().NoError(err)
	s.Contains(site.SourceNote, "no extent")
}

func (s *refsSourceSuite) TestADocSectionPastTheFileIsUnreadable() {
	s.write("d.md", "# A\n")
	site, err := DocSectionSource(s.root, s.section(map[string]string{"start_line": "3", "end_line": "50"}), showWindow)
	s.Require().NoError(err)
	s.Equal(types.DefinitionUnreadable, site.Status)
	s.Contains(site.SourceNote, "is shorter")
}

func (s *refsSourceSuite) TestExplainSourceRefusesANodeThatIsNoSection() {
	_, err := DocSectionSource(s.root, types.KnowledgeNode{ID: "file:a", Kind: types.KindFile}, showWindow)
	s.Equal(exit.Usage, exit.Code(err))
	s.ErrorContains(err, "magus refs --source")
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

func (s *refsSourceSuite) TestAnEmptyRangeSaysSoInText() {
	var b bytes.Buffer
	writeSourceBlock(&b, "  ", "", 0, 3, "no lines: 9:12 falls outside the file")
	s.Equal("  (no lines: 9:12 falls outside the file)\n", b.String())
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
		return []types.FileEntry{{Path: "gen/a.go", Role: diff.RoleOutput}, {Path: "a.go", Role: "source"}}, nil
	}))
	s.True(files[0].Generated)
	s.False(files[1].Generated)
}

func (s *refsSourceSuite) TestAClassifyFailureSaysGeneratedIsUnmarked() {
	note := markOccurrenceFiles(context.Background(), []types.SymbolOccurrenceFile{{File: "a.go"}}, nil)
	s.Contains(note, "generated files are not marked")
}

func (s *refsSourceSuite) TestLinesSuffix() {
	s.Equal("", linesSuffix(nil))
	s.Equal("  lines 7", linesSuffix([]int{7}))
	s.Equal("  lines 7,19,42", linesSuffix([]int{7, 19, 42}))
}

func (s *refsSourceSuite) TestCheckSourcePageRefusesANegativeCap() {
	s.Equal(exit.Usage, exit.Code(CheckSourcePage("explain", -1, 0)))
	s.Equal(exit.Usage, exit.Code(CheckSourcePage("explain", 0, -1)))
	s.NoError(CheckSourcePage("explain", 0, 0))
}
