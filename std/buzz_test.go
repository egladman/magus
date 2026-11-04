package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type BuzzParseSuite struct {
	suite.Suite
	dir string
}

func TestBuzzParseSuite(t *testing.T) {
	suite.Run(t, new(BuzzParseSuite))
}

func (s *BuzzParseSuite) SetupTest() {
	s.dir = s.T().TempDir()
}

func (s *BuzzParseSuite) parse(src string, embedded bool) types.BuzzFile {
	path := filepath.Join(s.dir, "fixture.buzz")
	s.Require().NoError(os.WriteFile(path, []byte(src), 0o600))
	file, err := BuzzParse(context.Background(), path, embedded)
	s.Require().NoError(err)
	s.Equal(path, file.Path)
	return file
}

const buzzFixture = `import "std";

// helper raises.
fun parsePort(text: str) > int !> str {
    if (text == "") {
        throw "empty";
    }
    return 1;
}

object Counter {
    n: int = 0,

    fun bump() > void !> str {
        final check = fun () > void !> str {
            throw "inner";
        };
        throw "outer";
    }
}

fun main(args: [str]) > void !> any {
    std\print("{parsePort("80")}");
}

test "port parses" {
    throw "fails";
}
`

func (s *BuzzParseSuite) TestFunctionsCarryExtentErrorTypeAndThrows() {
	file := s.parse(buzzFixture, false)
	s.Empty(file.Error)
	s.Equal([]types.BuzzFunction{
		{Name: "parsePort", Line: 4, EndLine: 9, ErrorType: "str", Throws: []int{6}},
		{Name: "bump", ObjectName: "Counter", Line: 14, EndLine: 19, ErrorType: "str", Throws: []int{18}},
		{Name: "", Line: 15, EndLine: 17, ErrorType: "str", Throws: []int{16}},
		{Name: "main", Line: 22, EndLine: 24, ErrorType: "any", Throws: []int{}},
	}, file.Functions)
}

func (s *BuzzParseSuite) TestStatementsSpanTheirLines() {
	file := s.parse(buzzFixture, false)
	s.Equal([]types.BuzzStatement{
		{Kind: "import", Name: "std", Line: 1, EndLine: 1},
		{Kind: "fun", Name: "parsePort", Line: 4, EndLine: 9},
		{Kind: "object", Name: "Counter", Line: 11, EndLine: 20},
		{Kind: "fun", Name: "main", Line: 22, EndLine: 24},
		{Kind: "test", Name: "port parses", Line: 26, EndLine: 28},
	}, file.Statements)
}

func (s *BuzzParseSuite) TestTestBlocksOwnTheirThrows() {
	file := s.parse(buzzFixture, false)
	s.Equal([]types.BuzzTest{
		{Name: "port parses", Line: 26, EndLine: 28, Throws: []int{27}},
	}, file.Tests)
}

func (s *BuzzParseSuite) TestParseErrorIsAValue() {
	file := s.parse("fun broken( > int {\n", false)
	s.Contains(file.Error, "line 1")
	s.Empty(file.Functions)
	s.Empty(file.Statements)
	s.Empty(file.Tests)
}

func (s *BuzzParseSuite) TestMissingFileIsAValue() {
	missing := filepath.Join(s.dir, "absent.buzz")
	file, err := BuzzParse(context.Background(), missing, false)
	s.Require().NoError(err)
	s.Contains(file.Error, "absent.buzz")
	s.Empty(file.Functions)
}

func (s *BuzzParseSuite) TestEmbeddedAcceptsTopLevelControlFlow() {
	src := "import \"std\";\nif (true) {\n    std\\print(\"x\");\n}\n"
	s.NotEmpty(s.parse(src, false).Error)
	file := s.parse(src, true)
	s.Empty(file.Error)
	s.Equal([]types.BuzzStatement{
		{Kind: "import", Name: "std", Line: 1, EndLine: 1},
		{Kind: "if", Line: 2, EndLine: 4},
	}, file.Statements)
}
