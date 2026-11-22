package edit

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type BlocksSuite struct{ suite.Suite }

func TestEditBlocks(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(BlocksSuite))
}

func doc(lines ...string) []byte { return []byte(strings.Join(lines, "\n") + "\n") }

func (s *BlocksSuite) parse(lines ...string) types.EditDocument {
	return ParseBlocks("edits.md", doc(lines...))
}

func (s *BlocksSuite) refusal(line int, reason string) types.EditDocument {
	return types.EditDocument{Refused: []types.EditRefusal{{Path: "edits.md", Line: line, Column: 1, Reason: reason}}}
}

func (s *BlocksSuite) TestOldThenNewIsOneLiteral() {
	s.Equal(types.EditDocument{Changes: []types.EditChange{
		{Path: "internal/edit/edit.go", Old: "func Resolve(root string) {", New: "func Resolve(root string, req Request) {", Count: 1},
	}}, s.parse(
		"## internal/edit/edit.go",
		"",
		"```old",
		"func Resolve(root string) {",
		"```",
		"",
		"```new",
		"func Resolve(root string, req Request) {",
		"```",
	))
}

func (s *BlocksSuite) TestOldAllIsEveryOccurrence() {
	s.Equal([]types.EditChange{{Path: "a.go", Old: "f()", New: "g()", Count: -1}},
		s.parse("## a.go", "```old all", "f()", "```", "```new", "g()", "```").Changes)
}

func (s *BlocksSuite) TestPatternIsARegexOverEveryMatch() {
	s.Equal([]types.EditChange{{Path: "a.go", Old: `v(\d)alpha1`, New: "v${1}beta1", Count: -1, Regex: true}},
		s.parse("## a.go", "```pattern", `v(\d)alpha1`, "```", "```new", "v${1}beta1", "```").Changes)
}

func (s *BlocksSuite) TestCreateKeepsEveryTerminator() {
	s.Equal([]types.EditChange{{Path: "b/new.go", New: "package b\n\nvar x = 1\n", Create: true}},
		s.parse("## b/new.go", "```create", "package b", "", "var x = 1", "```").Changes)
}

func (s *BlocksSuite) TestCreateInACRLFDocumentKeepsCRLF() {
	text := []byte("## a.txt\r\n```create\r\none\r\ntwo\r\n```\r\n")

	s.Equal([]types.EditChange{{Path: "a.txt", New: "one\r\ntwo\r\n", Create: true}}, ParseBlocks("edits.md", text).Changes)
}

func (s *BlocksSuite) TestEmptyCreateIsAnEmptyFile() {
	s.Equal([]types.EditChange{{Path: "b/.keep", Create: true}}, s.parse("## b/.keep", "```create", "```").Changes)
}

func (s *BlocksSuite) TestEmptyNewDeletes() {
	s.Equal([]types.EditChange{{Path: "a.go", Old: "// gone", Count: 1}},
		s.parse("## a.go", "```old", "// gone", "```", "```new", "```").Changes)
}

func (s *BlocksSuite) TestSectionsCarryTheirOwnPath() {
	s.Equal([]types.EditChange{
		{Path: "a.go", Old: "1", New: "2", Count: 1},
		{Path: "a.go", Old: "3", New: "4", Count: 1},
		{Path: "b.go", New: "b\n", Create: true},
		{Path: "a.go", Old: "5", New: "6", Count: 1},
	}, s.parse(
		"## a.go",
		"```old", "1", "```", "```new", "2", "```",
		"```old", "3", "```", "```new", "4", "```",
		"## b.go",
		"```create", "b", "```",
		"## a.go",
		"```old", "5", "```", "```new", "6", "```",
	).Changes)
}

func (s *BlocksSuite) TestNotesAndOtherHeadingsAreNotRead() {
	s.Equal(types.EditDocument{Changes: []types.EditChange{{Path: "a.go", Old: "x", New: "y", Count: 1}}}, s.parse(
		"# Rename x",
		"This note says why.",
		"## a.go",
		"### the declaration",
		"Change the name:",
		"```old", "x", "```",
		"then",
		"```new", "y", "```",
		"Done.",
	))
}

func (s *BlocksSuite) TestTextIsByteForByte() {
	text := "\tif x { return `raw` } // {brace} \\{ $1 \"q\"  "
	s.Equal([]types.EditChange{{Path: "a.go", Old: text, New: fmt.Sprintf("\n%s\n", text), Count: 1}},
		s.parse("## a.go", "```old", text, "```", "```new", "", text, "", "```").Changes)
}

func (s *BlocksSuite) TestALongerFenceHoldsAFence() {
	s.Equal([]types.EditChange{{Path: "README.md", Old: "```sh\nmake\n```", New: "~~~\n```sh\nmagus run build\n```", Count: 1}},
		s.parse("## README.md",
			"````old", "```sh", "make", "```", "````",
			"~~~~new", "~~~", "```sh", "magus run build", "```", "~~~~",
		).Changes)
}

func (s *BlocksSuite) TestAShorterFenceDoesNotClose() {
	s.Equal([]types.EditChange{{Path: "a.md", Old: "```", New: "~~~", Count: 1}},
		s.parse("## a.md", "````old", "```", "`````", "```new", "~~~", "```").Changes)
}

func (s *BlocksSuite) TestATildeFenceClosesOnlyOnTildes() {
	s.Equal([]types.EditChange{{Path: "a.md", Old: "```\nx", New: "y", Count: 1}},
		s.parse("## a.md", "~~~old", "```", "x", "~~~", "```new", "y", "```").Changes)
}

func (s *BlocksSuite) TestCRLFTerminatorsLeaveTheText() {
	text := []byte("## a.go\r\n```old\r\none\r\ntwo\r\n```\r\n```new\r\nthree\r\n```\r\n")

	s.Equal([]types.EditChange{{Path: "a.go", Old: "one\r\ntwo", New: "three", Count: 1}}, ParseBlocks("edits.md", text).Changes)
}

func (s *BlocksSuite) TestUnknownKindRefused() {
	s.Equal(s.refusal(2, `unknown block kind "go": expected old, old all, pattern, new or create`),
		s.parse("## a.go", "```go", "x", "```"))
}

func (s *BlocksSuite) TestOldWithAnotherWordRefused() {
	got := s.parse("## a.go", "```old some", "x", "```", "```new", "y", "```")

	s.Equal([]types.EditRefusal{
		{Path: "edits.md", Line: 2, Column: 1, Reason: `unknown block kind "old some": expected old, old all, pattern, new or create`},
		{Path: "edits.md", Line: 5, Column: 1, Reason: "```new has no ```old, ```old all or ```pattern before it"},
	}, got.Refused)
}

func (s *BlocksSuite) TestAFenceWithoutAKindRefused() {
	s.Equal(s.refusal(2, "the fence names no kind: expected old, old all, pattern, new or create"),
		s.parse("## a.go", "```", "x", "```"))
}

func (s *BlocksSuite) TestOldWithoutNewRefused() {
	s.Equal(s.refusal(2, "```old has no ```new after it: follow it with a ```new block"),
		s.parse("## a.go", "```old", "x", "```", "## b.go", "```create", "```"))
}

func (s *BlocksSuite) TestPatternFollowedByOldRefused() {
	s.Equal(s.refusal(2, "```pattern has no ```new after it: follow it with a ```new block"),
		s.parse("## a.go", "```pattern", "x", "```", "```old", "y", "```", "```new", "z", "```"))
}

func (s *BlocksSuite) TestOldAtTheEndRefused() {
	s.Equal(s.refusal(2, "```old all has no ```new after it: follow it with a ```new block"),
		s.parse("## a.go", "```old all", "x", "```"))
}

func (s *BlocksSuite) TestNewWithoutOldRefused() {
	s.Equal(s.refusal(2, "```new has no ```old, ```old all or ```pattern before it"),
		s.parse("## a.go", "```new", "x", "```"))
}

func (s *BlocksSuite) TestABlockBeforeTheFirstHeadingRefused() {
	s.Equal(s.refusal(1, "a block before the first `## <path>` heading belongs to no file: open a section first"),
		s.parse("```create", "x", "```", "## a.go", "```create", "```"))
}

func (s *BlocksSuite) TestAnUnclosedFenceRefused() {
	s.Equal(s.refusal(5, "the ```` fence opened here never closes: close it with a line of 4 or more `"),
		s.parse("## a.go", "```old", "x", "```", "````new", "y", "```"))
}

func (s *BlocksSuite) TestASectionWithoutABlockRefused() {
	s.Equal(s.refusal(1, "the section holds no block: expected ```old then ```new, or ```create"),
		s.parse("## a.go", "notes only", "## b.go", "```create", "```"))
}

func (s *BlocksSuite) TestAHeadingWithoutAPathRefused() {
	s.Equal(s.refusal(1, "a section heading names a workspace-relative path: `## <path>`"),
		s.parse("##", "```create", "```"))
}

func (s *BlocksSuite) TestAnEmptyDocumentRefused() {
	s.Equal(s.refusal(1, "the document holds no `## <path>` section"), ParseBlocks("edits.md", nil))
}

func (s *BlocksSuite) TestARefusalVoidsEveryChange() {
	got := s.parse("## a.go", "```old", "x", "```", "```new", "y", "```", "## b.go", "```nope", "```")

	s.Nil(got.Changes)
	s.Len(got.Refused, 1)
}

func (s *BlocksSuite) TestADocumentPlansAndApplies() {
	root := writeFiles(s.T(), map[string]string{"a.go": "package a\n\nfunc f() { g(); g() }\n"})
	parsed := ParseBlocks("edits.md", doc(
		"## a.go",
		"```old all", "g()", "```",
		"```new", "h()", "```",
		"## b/b.go",
		"```create", "package b", "```",
	))
	s.Require().Empty(parsed.Refused)

	x := Expand(root, parsed.Changes, nil)
	s.Require().Empty(x.Refused)
	p := Resolve(root, Request{Sites: x.Sites, Creates: x.Creates})
	s.Equal([]string{"b"}, p.MakesDirs())
	s.Require().NoError(p.Apply())

	s.Equal("package a\n\nfunc f() { h(); h() }\n", readFile(s.T(), root, "a.go"))
	s.Equal("package b\n", readFile(s.T(), root, "b/b.go"))
	rerun := Expand(root, parsed.Changes, nil)
	s.Equal(parsed.Changes, rerun.Already)
	s.Empty(rerun.Sites)
	s.Empty(rerun.Creates)
}
