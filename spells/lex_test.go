package spells

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

// goLike is shaped like the go spell's declaration, small enough to read.
func goLike() Syntax {
	return Syntax{
		Comments: &CommentSyntax{
			LineComments:  []string{"//"},
			BlockComments: []CommentBlock{{Open: "/*", Close: "*/"}},
			Quotes:        []Quote{{Open: `"`, Close: `"`}, {Open: "`", Close: "`", IgnoreEscape: true}},
			Directives:    []string{"go:", "nolint"},
		},
		Stubs:     &StubSyntax{Kinds: []string{"Function"}, BodyStyle: StubBodyBrace, Body: `{ panic("stub") }`},
		Operators: []string{"==", "!=", "<=", ">=", "&&", "||", "++", ":="},
		Decisions: &DecisionSyntax{Keywords: []string{"if", "for", "case"}, Operators: []string{"&&", "||"}},
	}
}

func pythonLike() Syntax {
	return Syntax{
		Comments:  &CommentSyntax{LineComments: []string{"#"}, Quotes: []Quote{{Open: `"`, Close: `"`}}},
		Stubs:     &StubSyntax{Kinds: []string{"Function"}, BodyStyle: StubBodyIndent, Body: "raise NotImplementedError"},
		Decisions: &DecisionSyntax{Keywords: []string{"if", "and"}},
	}
}

type lexSpansSuite struct{ suite.Suite }

func TestLexSpans(t *testing.T) { suite.Run(t, new(lexSpansSuite)) }

func (s *lexSpansSuite) TestReportsAStringAndTheCommentAfterIt() {
	src := `x := "a // b" // note`
	s.Equal([]LexSpan{
		{Start: 5, End: 13, Kind: LexString},
		{Start: 14, End: 21, Kind: LexComment},
	}, goLike().Comments.Spans(src))
}

func (s *lexSpansSuite) TestMarksAWholeLineCommentAsOwningItsLine() {
	s.Equal([]LexSpan{{Start: 2, End: 9, Kind: LexComment, OwnLine: true}},
		goLike().Comments.Spans("\n\t// note\nx"))
}

func (s *lexSpansSuite) TestRecordsABlockCommentsCloser() {
	s.Equal([]LexSpan{{Start: 2, End: 9, Kind: LexComment, Closer: "*/"}},
		goLike().Comments.Spans("x /* a */ y"))
}

func (s *lexSpansSuite) TestReportsADirectiveAsItsOwnKind() {
	s.Equal([]LexSpan{{Start: 0, End: 16, Kind: LexDirective, OwnLine: true}},
		goLike().Comments.Spans("//go:build linux\n"))
}

func (s *lexSpansSuite) TestACommentAfterADirectiveOwnsNoLine() {
	s.Equal([]LexSpan{
		{Start: 0, End: 9, Kind: LexDirective, Closer: "*/", OwnLine: true},
		{Start: 10, End: 14, Kind: LexComment},
	}, goLike().Comments.Spans("/*go:x */ // a\n"))
}

func (s *lexSpansSuite) TestRunsAnUnterminatedStringToTheEnd() {
	s.Equal([]LexSpan{{Start: 4, End: 9, Kind: LexString}}, goLike().Comments.Spans(`x = "abc`+"\n"))
}

type complexitySuite struct{ suite.Suite }

func TestComplexity(t *testing.T) { suite.Run(t, new(complexitySuite)) }

func (s *complexitySuite) TestIsOneForStraightLineCode() {
	n, err := goLike().Complexity("{\n\treturn x + 1\n}")
	s.Require().NoError(err)
	s.Equal(1, n)
}

func (s *complexitySuite) TestCountsKeywordsAndOperators() {
	n, err := goLike().Complexity("{\n\tif a && b || c {\n\t\tfor {}\n\t}\n}")
	s.Require().NoError(err)
	s.Equal(5, n)
}

func (s *complexitySuite) TestIgnoresTokensInCommentsAndStrings() {
	n, err := goLike().Complexity("{\n\t// if a && b\n\tx := \"for case ||\"\n}")
	s.Require().NoError(err)
	s.Equal(1, n)
}

func (s *complexitySuite) TestMatchesKeywordsAsWholeWords() {
	n, err := goLike().Complexity("{ iffy := format; casement() }")
	s.Require().NoError(err)
	s.Equal(1, n)
}

func (s *complexitySuite) TestRefusesASyntaxWithNoDecisions() {
	syn := goLike()
	syn.Decisions = nil
	n, err := syn.Complexity("{}")
	s.Require().EqualError(err, "the syntax declares no decisions, so complexity is unmeasurable")
	s.Zero(n)
}

func (s *complexitySuite) TestRefusesASyntaxWithNoComments() {
	syn := goLike()
	syn.Comments = nil
	n, err := syn.Complexity("{}")
	s.Require().EqualError(err, "the syntax declares no comments, so a token in a comment or string would count as code")
	s.Zero(n)
}

func (s *complexitySuite) TestCountsAnIndentLanguagesWordOperators() {
	n, err := pythonLike().Complexity("def f(a, b):\n    if a and b:  # if\n        return 1\n")
	s.Require().NoError(err)
	s.Equal(3, n)
}

type lexDirectiveSuite struct{ suite.Suite }

func TestLexDirective(t *testing.T) { suite.Run(t, new(lexDirectiveSuite)) }

func (s *lexDirectiveSuite) TestAddsNoDecision() {
	n, err := goLike().Complexity("{\n\tx := f() //nolint:x // if a && b\n}")
	s.Require().NoError(err)
	s.Equal(1, n)
}
