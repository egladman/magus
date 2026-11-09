package magus_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type guideSuite struct{ suite.Suite }

func TestGuide(t *testing.T) { suite.Run(t, new(guideSuite)) }

// TestGuideBlocksAreExamples holds every Go block of the guide to a verbatim
// copy in example_test.go, so a block that stops compiling fails here.
func (s *guideSuite) TestGuideBlocksAreExamples() {
	guide, err := os.ReadFile(filepath.Join("docs", "guides", "go-sdk.md"))
	s.Require().NoError(err)
	examples, err := os.ReadFile("example_test.go")
	s.Require().NoError(err)
	source := spacedTabs(string(examples))
	blocks := strings.Split(string(guide), "```go\n")[1:]
	s.Require().NotEmpty(blocks, "docs/guides/go-sdk.md has no go blocks")
	for _, block := range blocks {
		body, _, _ := strings.Cut(block, "```")
		s.Contains(source, body, "a go block of docs/guides/go-sdk.md is not in example_test.go")
	}
}

// TestAnEditedBlockIsCaught pins that the comparison is not vacuous.
func (s *guideSuite) TestAnEditedBlockIsCaught() {
	source := spacedTabs("func f() {\n\treturn\n}\n")
	s.Contains(source, "func f() {\n    return\n}\n")
	s.NotContains(source, "func f() {\n    return nil\n}\n")
}

// spacedTabs writes each leading tab of source as four spaces, the way the
// guide shows Go.
func spacedTabs(source string) string {
	lines := strings.Split(source, "\n")
	for i, line := range lines {
		code := strings.TrimLeft(line, "\t")
		lines[i] = strings.Repeat("    ", len(line)-len(code)) + code
	}
	return strings.Join(lines, "\n")
}
