package emit

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/types"
)

type ParseSuite struct{ suite.Suite }

func TestParse(t *testing.T) { suite.Run(t, new(ParseSuite)) }

func (s *ParseSuite) codes(ps Problems) []types.DiagnosticCode {
	out := make([]types.DiagnosticCode, len(ps))
	for i, p := range ps {
		out[i] = p.Code
	}
	return out
}

func (s *ParseSuite) TestAHolePassesTheTextAroundItThrough() {
	p, ps := Parse([]byte("{{hole}}\nfunc {{name:ident}}() {}\n"))
	s.Empty(ps)
	s.Equal(Parsed{
		Count:   2,
		Strings: []string{"func ", "() {}\n"},
		Holes:   []Hole{{Name: "name", Spec: "ident", Line: 2, Column: 6}},
	}, p)
}

func (s *ParseSuite) TestAFileWithNoHolesIsOneString() {
	p, ps := Parse([]byte("{{{hole}}}\nplain {{ text }}\n"))
	s.Empty(ps)
	s.Equal([]string{"plain {{ text }}\n"}, p.Strings)
	s.Empty(p.Holes)
}

func (s *ParseSuite) TestAShorterBraceRunIsText() {
	p, ps := Parse([]byte("{{hole}}\nx := T{a: 1} ${VAR}\n"))
	s.Empty(ps)
	s.Equal([]string{"x := T{a: 1} ${VAR}\n"}, p.Strings)
}

func (s *ParseSuite) TestExtraBracesAroundAHoleAreText() {
	p, ps := Parse([]byte("{{hole}}\n{{{name:ident}}}\n"))
	s.Empty(ps)
	s.Equal([]string{"{", "}\n"}, p.Strings)
	s.Equal(2, p.Holes[0].Column)
}

func (s *ParseSuite) TestAGithubExpressionPassesUnderCountThree() {
	p, ps := Parse([]byte("{{{hole}}}\nsha: ${{ github.sha }} {{{svc:ident}}}\n"))
	s.Empty(ps)
	s.Equal([]string{"sha: ${{ github.sha }} ", "\n"}, p.Strings)
}

func (s *ParseSuite) TestWhitespaceAndConversionAreRead() {
	p, ps := Parse([]byte("{{hole}}\n{{ a }}{{\tb!r:q }}{{c!s}}\n"))
	s.Empty(ps)
	s.Equal([]Hole{
		{Name: "a", Line: 2, Column: 1},
		{Name: "b", Conversion: "r", Spec: "q ", Line: 2, Column: 8},
		{Name: "c", Conversion: "s", Line: 2, Column: 19},
	}, []Hole{p.Holes[0], p.Holes[1], p.Holes[2]})
}

func (s *ParseSuite) TestTheFinalNewlineIsKept() {
	p, ps := Parse([]byte("{{hole}}\na\n\n{{x:raw}}"))
	s.Empty(ps)
	s.Equal([]string{"a\n\n", ""}, p.Strings)
}

func (s *ParseSuite) TestABadHeaderIsRefused() {
	for _, src := range []string{"", "hole\nx", "{{hole}}x\n", "{{hole}\nx", "{{ hole }}\nx", "{{{{{{{{{hole}}}}}}}}}\nx", "{{hole}}\r\nx"} {
		_, ps := Parse([]byte(src))
		s.Contains(s.codes(ps), types.EmitBadHeader, "%q", src)
	}
}

func (s *ParseSuite) TestABraceRunOfTwiceTheCountIsRefused() {
	_, ps := Parse([]byte("{{hole}}\nx {{{{a:raw}}}}\n"))
	s.Equal(Problems{{
		Code:    types.EmitBadBraces,
		Line:    2,
		Column:  3,
		Message: "4 opening braces in a row: text holds at most 1 with delimiter 2; raise the count on line 1",
	}}, ps)
}

func (s *ParseSuite) TestAnUnclosedHoleIsRefused() {
	_, ps := Parse([]byte("{{hole}}\n{{a:raw\n}}\n"))
	s.Equal([]types.DiagnosticCode{types.EmitBadBraces}, s.codes(ps))
	s.Equal(2, ps[0].Line)
}

func (s *ParseSuite) TestAHoleClosedByTooFewBracesIsRefused() {
	_, ps := Parse([]byte("{{{hole}}}\n{{{a:raw}}\n"))
	s.Equal([]types.DiagnosticCode{types.EmitBadBraces}, s.codes(ps))
}

func (s *ParseSuite) TestABadHoleInsideIsRefused() {
	for _, body := range []string{"", "  ", "9a", "a.b", "a[0]", "a=", "a!x", "a!", "a b", "a:{x}", "é"} {
		_, ps := Parse([]byte(fmt.Sprintf("{{hole}}\n{{%s}}\n", body)))
		s.Equal([]types.DiagnosticCode{types.EmitBadHole}, s.codes(ps), "%q", body)
	}
}

func (s *ParseSuite) TestBadBytesAreRefusedOncePerKindPerLine() {
	_, ps := Parse([]byte("\xef\xbb\xbf{{hole}}\na\r\nb\r\nc\x00\x00\xff\n"))
	s.Equal([]types.DiagnosticCode{
		types.EmitBadBytes, types.EmitBadBytes, types.EmitBadBytes, types.EmitBadBytes, types.EmitBadBytes,
	}, s.codes(ps))
	s.Equal([]int{1, 2, 3, 4, 4}, []int{ps[0].Line, ps[1].Line, ps[2].Line, ps[3].Line, ps[4].Line})
}

func (s *ParseSuite) TestLoadBindsValuesInOrder() {
	t, ps := Load("a.tmpl", []byte("{{hole}}\n{{x:ident}} {{y!r:string}} {{x:raw}}\n"), map[string]any{"x": "A", "y": 3})
	s.Empty(ps)
	s.Equal(types.Template{
		Strings: []string{"", " ", " ", "\n"},
		Interpolations: []types.Interpolation{
			{Value: "A", Expression: "x", FormatSpec: "ident"},
			{Value: 3, Expression: "y", Conversion: "r", FormatSpec: "string"},
			{Value: "A", Expression: "x", FormatSpec: "raw"},
		},
		Source: "a.tmpl",
	}, t)
}

func (s *ParseSuite) TestLoadRefusesMissingUnusedAndNullValues() {
	_, ps := Load("a.tmpl", []byte("{{hole}}\n{{a:raw}} {{b:raw}} {{n:raw}} {{n:raw}}\n"), map[string]any{"a": "1", "n": nil, "z": "9"})
	s.Equal([]types.DiagnosticCode{types.EmitMissingValue, types.EmitNullValue, types.EmitUnusedValue}, s.codes(ps))
	s.Equal(0, ps[2].Line)
}

func (s *ParseSuite) TestLoadRefusesAHoleTypedWithTheWrongCount() {
	_, ps := Load("a.tmpl", []byte("{{hole}}\n{{a:raw}} {b:raw} {c}\n"), map[string]any{"a": "1", "b": "2"})
	s.Equal([]types.DiagnosticCode{types.EmitBadBraces}, s.codes(ps))
	s.Equal(11, ps[0].Column)
}

func (s *ParseSuite) TestLoadReportsEveryProblemInOnePass() {
	_, ps := Load("a.tmpl", []byte("{{hole}}\n{{a.b:raw}}\n{{{{x}}}}\n{{q:raw}}\n"), map[string]any{})
	s.Equal([]types.DiagnosticCode{types.EmitBadHole, types.EmitBadBraces}, s.codes(ps))
}

func (s *ParseSuite) TestAProblemNamesItsPlace() {
	_, ps := Parse([]byte("{{hole}}\n  {{a.b}}\n"))
	s.Equal("t.tmpl:2:5: [MGS1066] hole \"a.b\" reads a dotted or indexed name: compute one name in Buzz", ps.Lines("t.tmpl")[0])
}
