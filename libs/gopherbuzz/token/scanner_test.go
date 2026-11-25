package token

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

// quoteLiteral is a test-only form, q"..."q, whose content may hold `"` and `}`.
var quoteLiteral = LiteralScanner{
	Name:   "quote",
	Prefix: 'q',
	Scan: func(src string) (string, int, error) {
		if !strings.HasPrefix(src, `q"`) {
			return "", 0, nil
		}
		end := strings.Index(src[2:], `"q`)
		if end < 0 {
			return "", 0, errors.New("unterminated quote literal")
		}
		return src[2 : 2+end], end + 4, nil
	},
}

type scannerSuite struct {
	suite.Suite
	quote *Scanner
}

func TestScannerSuite(t *testing.T) { suite.Run(t, new(scannerSuite)) }

func (s *scannerSuite) SetupTest() {
	sc, err := NewScanner(quoteLiteral)
	s.Require().NoError(err)
	s.quote = sc
}

func (s *scannerSuite) kinds(toks []Token) []Kind {
	out := make([]Kind, len(toks))
	for i, t := range toks {
		out[i] = t.Kind
	}
	return out
}

// TestTestdataLexesTheSameWithAnyScanner holds the off state. Every testdata file
// lexes alike with no scanner, a zero one, and one whose literal it never uses.
func (s *scannerSuite) TestTestdataLexesTheSameWithAnyScanner() {
	files, err := filepath.Glob(filepath.Join("..", "testdata", "*.buzz"))
	s.Require().NoError(err)
	s.Require().NotEmpty(files)
	for _, f := range files {
		src, err := os.ReadFile(f)
		s.Require().NoError(err)
		want, wantErr := Tokenize(string(src))
		for _, sc := range []*Scanner{nil, {}, s.quote} {
			got, gotErr := sc.Tokenize(string(src))
			s.Equal(wantErr, gotErr, "%s with scanner %q", f, sc.Name())
			s.Equal(want, got, "%s with scanner %q", f, sc.Name())
		}
	}
}

func (s *scannerSuite) TestUnterminatedMultilineStringUsesUpstreamWording() {
	_, err := Tokenize("x = `abc")
	s.EqualError(err, "buzz: unterminated string at line 1:5")
}

func (s *scannerSuite) TestRawStringIsNotOneTokenUpstream() {
	toks, err := Tokenize(`r"x"`)
	s.Require().NoError(err)
	s.Equal([]Token{
		{Kind: Ident, Val: "r", Line: 1, Col: 1},
		{Kind: String, Val: "x", Line: 1, Col: 2},
		{Kind: EOF, Line: 1, Col: 5},
	}, toks)

	_, err = Tokenize(`r#"x"#`)
	s.EqualError(err, "buzz: unexpected character '#' at line 1:2")
}

func (s *scannerSuite) TestLiteralYieldsAPlainString() {
	toks, err := s.quote.Tokenize("final a = q\"x \"{y}\" \\n\nz\"q; b")
	s.Require().NoError(err)
	s.Equal([]Token{
		{Kind: Final, Val: "final", Line: 1, Col: 1},
		{Kind: Ident, Val: "a", Line: 1, Col: 7},
		{Kind: Assign, Line: 1, Col: 9},
		{Kind: String, Val: "x \"{y}\" \\n\nz", Line: 1, Col: 11},
		{Kind: Semicolon, Line: 2, Col: 4},
		{Kind: Ident, Val: "b", Line: 2, Col: 6},
		{Kind: EOF, Line: 2, Col: 7},
	}, toks)
}

func (s *scannerSuite) TestLiteralDeclinesWhatItDoesNotOpen() {
	for _, src := range []string{"q", "quit", "q + 1", `"q"`, `aq"x"q`} {
		want, err := Tokenize(src)
		s.Require().NoError(err, src)
		got, err := s.quote.Tokenize(src)
		s.Require().NoError(err, src)
		s.Equal(want, got, src)
	}
}

func (s *scannerSuite) TestLiteralInsideAnInterpolationIsSkippedWhole() {
	toks, err := s.quote.Tokenize(`"<{f(q"}"q)}>"`)
	s.Require().NoError(err)
	s.Equal([]Kind{InterpStr, EOF}, s.kinds(toks))
	s.Equal([]StringPart{
		{Text: "<"},
		{IsExpr: true, Text: `f(q"}"q)`, Line: 1, Col: 3},
		{Text: ">"},
	}, toks[0].Parts)
}

func (s *scannerSuite) TestScanErrorIsReportedAtTheLiteral() {
	_, err := s.quote.Tokenize(`x = q"open`)
	s.EqualError(err, "buzz: unterminated quote literal at line 1:5")
}

func (s *scannerSuite) TestClaimOnAKeywordIsRefused() {
	greedy, err := NewScanner(LiteralScanner{
		Name: "greedy", Prefix: 'r',
		Scan: func(src string) (string, int, error) { return "", identWidth(src), nil },
	})
	s.Require().NoError(err)
	_, err = greedy.Tokenize("x; return x")
	s.EqualError(err, `buzz: literal "greedy" claimed an identifier at line 1:4`)
	_, err = greedy.Tokenize("rx")
	s.EqualError(err, `buzz: literal "greedy" claimed an identifier at line 1:1`)
}

func (s *scannerSuite) TestClaimPastTheEndIsRefused() {
	long, err := NewScanner(LiteralScanner{
		Name: "long", Prefix: 'r',
		Scan: func(src string) (string, int, error) { return "", len(src) + 1, nil },
	})
	s.Require().NoError(err)
	_, err = long.Tokenize(`r"x"`)
	s.EqualError(err, `buzz: literal "long" claimed 5 bytes past the end of the source at line 1:1`)
}

func (s *scannerSuite) TestNewScannerRefusesABadLiteral() {
	scan := quoteLiteral.Scan
	for _, prefix := range []byte{'"', '#', '$', '1', '_', 0} {
		_, err := NewScanner(LiteralScanner{Name: "bad", Prefix: prefix, Scan: scan})
		s.Error(err, "prefix %q", prefix)
	}
	_, err := NewScanner(LiteralScanner{Prefix: 'r', Scan: scan})
	s.EqualError(err, `token: literal name "" must be non-empty and hold no '+'`)
	_, err = NewScanner(LiteralScanner{Name: "a+b", Prefix: 'r', Scan: scan})
	s.EqualError(err, `token: literal name "a+b" must be non-empty and hold no '+'`)
	_, err = NewScanner(quoteLiteral, quoteLiteral)
	s.EqualError(err, `token: literal "quote" is registered twice`)
	_, err = NewScanner(LiteralScanner{Name: "none", Prefix: 'r'})
	s.EqualError(err, `token: literal "none" has no Scan`)
}

func (s *scannerSuite) TestNameSortsTheLiterals() {
	s.Empty((*Scanner)(nil).Name())
	s.Empty((&Scanner{}).Name())
	both, err := NewScanner(quoteLiteral, LiteralScanner{Name: "alpha", Prefix: 'a', Scan: quoteLiteral.Scan})
	s.Require().NoError(err)
	s.Equal("alpha+quote", both.Name())
}
