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
		Mutations: &MutationSyntax{
			Swaps: []Swap{
				{Name: "lt-le", Find: "<", Replace: "<="},
				{Name: "le-lt", Find: "<=", Replace: "<"},
				{Name: "and-or", Find: "&&", Replace: "||"},
				{Name: "true-false", Find: "true", Replace: "false"},
			},
			Bodies: []string{"{ return }"},
			Arid:   []string{`^\s*log\.`},
		},
	}
}

func pythonLike() Syntax {
	return Syntax{
		Comments:  &CommentSyntax{LineComments: []string{"#"}, Quotes: []Quote{{Open: `"`, Close: `"`}}},
		Stubs:     &StubSyntax{Kinds: []string{"Function"}, BodyStyle: StubBodyIndent, Body: "raise NotImplementedError"},
		Decisions: &DecisionSyntax{Keywords: []string{"if", "and"}},
		Mutations: &MutationSyntax{Bodies: []string{"return None"}},
	}
}

func swapsOnly() Syntax {
	syn := goLike()
	syn.Mutations.Bodies = nil
	return syn
}

func bodiesOnly() Syntax {
	syn := goLike()
	syn.Mutations.Swaps = nil
	return syn
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

type mutationSiteSuite struct{ suite.Suite }

func TestMutationSite(t *testing.T) { suite.Run(t, new(mutationSiteSuite)) }

func (s *mutationSiteSuite) TestReadsATwoCharacterOperatorWhole() {
	sites, err := swapsOnly().MutationSites("{\n\treturn a <= b\n}")
	s.Require().NoError(err)
	s.Equal([]MutationSite{{Operator: "le-lt", Offset: 12, Line: 2, Col: 11, Find: "<=", Replace: "<"}}, sites)
}

func (s *mutationSiteSuite) TestLeavesTheSignatureUnswapped() {
	sites, err := swapsOnly().MutationSites("func f(a <-chan int) bool {\n\treturn a < b\n}")
	s.Require().NoError(err)
	s.Equal([]MutationSite{{Operator: "lt-le", Offset: 38, Line: 2, Col: 11, Find: "<", Replace: "<="}}, sites)
}

func (s *mutationSiteSuite) TestSwapsAWordOnlyAsAWholeWord() {
	sites, err := swapsOnly().MutationSites("{ return trueish || true }")
	s.Require().NoError(err)
	s.Equal([]MutationSite{{Operator: "true-false", Offset: 20, Line: 1, Col: 21, Find: "true", Replace: "false"}}, sites)
}

func (s *mutationSiteSuite) TestSkipsCommentsStringsAndAridLines() {
	sites, err := swapsOnly().MutationSites("{\n\t// a < b\n\tlog.Print(a < b)\n\tx := \"a < b\"\n}")
	s.Require().NoError(err)
	s.Empty(sites)
}

func (s *mutationSiteSuite) TestReplacesABraceBodyWhole() {
	sites, err := bodiesOnly().MutationSites("func f(x struct{}) int {\n\treturn 1\n}")
	s.Require().NoError(err)
	s.Equal([]MutationSite{{Operator: BodyMutation, Offset: 23, Line: 1, Col: 24,
		Find: "{\n\treturn 1\n}", Replace: "{ return }"}}, sites)
}

func (s *mutationSiteSuite) TestReplacesAnIndentBodyAtItsIndentation() {
	sites, err := pythonLike().MutationSites("def f(x: int) -> int:\n    y = x\n    return y\n")
	s.Require().NoError(err)
	s.Equal([]MutationSite{{Operator: BodyMutation, Offset: 21, Line: 1, Col: 22,
		Find: "\n    y = x\n    return y", Replace: "\n    return None"}}, sites)
}

func (s *mutationSiteSuite) TestReplacesAOneLineIndentBodyOnItsLine() {
	sites, err := pythonLike().MutationSites("def f(): return {1: 2}")
	s.Require().NoError(err)
	s.Equal([]MutationSite{{Operator: BodyMutation, Offset: 8, Line: 1, Col: 9,
		Find: " return {1: 2}", Replace: " return None"}}, sites)
}

func (s *mutationSiteSuite) TestYieldsNoBodyForAnExpressionBody() {
	sites, err := bodiesOnly().MutationSites("(a, b) => a")
	s.Require().NoError(err)
	s.Empty(sites)
}

func (s *mutationSiteSuite) TestRefusesASyntaxWithNoMutations() {
	syn := goLike()
	syn.Mutations = nil
	sites, err := syn.MutationSites("{}")
	s.Require().EqualError(err, "the syntax declares no mutations, so a function yields no mutants")
	s.Nil(sites)
}

func (s *mutationSiteSuite) TestRefusesBodiesWithNoBodyStyle() {
	syn := goLike()
	syn.Stubs = nil
	sites, err := syn.MutationSites("{}")
	s.Require().EqualError(err, "mutations.bodies need stubs.bodyStyle to find the body they replace")
	s.Nil(sites)
}

func (s *mutationSiteSuite) TestRefusesAnUnparseableAridPattern() {
	syn := goLike()
	syn.Mutations.Arid = []string{"("}
	sites, err := syn.MutationSites("{}")
	s.Require().ErrorContains(err, "mutations.arid[0]: ")
	s.Nil(sites)
}

func (s *mutationSiteSuite) TestApplyReplacesTheSite() {
	out, err := MutationSite{Offset: 4, Find: "<=", Replace: "<"}.Apply("a = <= b")
	s.Require().NoError(err)
	s.Equal("a = < b", out)
}

func (s *mutationSiteSuite) TestApplyRefusesASourceWithoutTheSite() {
	out, err := MutationSite{Offset: 4, Find: "<=", Replace: "<"}.Apply("a = b")
	s.Require().EqualError(err, `the source holds no "<=" at offset 4`)
	s.Empty(out)
}

func (s *mutationSiteSuite) TestApplyRefusesAnOffsetPastTheEnd() {
	out, err := MutationSite{Offset: 9, Find: "<", Replace: "<="}.Apply("a")
	s.Require().EqualError(err, `the source holds no "<" at offset 9`)
	s.Empty(out)
}

type lexDirectiveSuite struct{ suite.Suite }

func TestLexDirective(t *testing.T) { suite.Run(t, new(lexDirectiveSuite)) }

func (s *lexDirectiveSuite) TestAddsNoDecision() {
	n, err := goLike().Complexity("{\n\tx := f() //nolint:x // if a && b\n}")
	s.Require().NoError(err)
	s.Equal(1, n)
}

func (s *lexDirectiveSuite) TestYieldsNoMutant() {
	sites, err := swapsOnly().MutationSites("{\n\tx := f() //nolint:x // a < b\n}")
	s.Require().NoError(err)
	s.Empty(sites)
}
