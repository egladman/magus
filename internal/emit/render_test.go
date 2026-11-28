package emit

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type RenderSuite struct{ suite.Suite }

func TestRender(t *testing.T) { suite.Run(t, new(RenderSuite)) }

func goIndex() map[string]spells.LanguageEncodings {
	return map[string]spells.LanguageEncodings{".go": {Spell: "golang", Language: "go", Encodings: []spells.Encoding{
		{Name: "ident", Pattern: `[\p{L}_][\p{L}\p{Nd}_]*`, Reserved: []string{"type", "range"}},
		{Name: "string", Open: `"`, Close: `"`, Control: `\x{hex2}`, Escapes: []spells.Escape{
			{Text: `\`, To: `\\`}, {Text: `"`, To: `\"`}, {Text: "\n", To: `\n`},
		}},
	}}}
}

func hole(expr, conv, spec string, v any) types.Interpolation {
	return types.Interpolation{Value: v, Expression: expr, Conversion: conv, FormatSpec: spec}
}

func tmpl(strs []string, holes ...types.Interpolation) types.Template {
	return types.Template{Strings: strs, Interpolations: holes}
}

func (s *RenderSuite) render(t types.Template) (string, Problems) {
	return Render(t, "x/a.go", goIndex())
}

func (s *RenderSuite) codes(ps Problems) []types.DiagnosticCode {
	var out []types.DiagnosticCode
	for _, p := range ps {
		out = append(out, p.Code)
	}
	return out
}

func (s *RenderSuite) TestEncodedHolesWriteTheirTokens() {
	out, ps := s.render(tmpl([]string{"func ", "() { return ", " }\n"},
		hole("name", "", "ident", "Run"), hole("msg", "", "string", "say \"hi\"\n")))
	s.Empty(ps)
	s.Equal("func Run() { return \"say \\\"hi\\\"\\n\" }\n", out)
}

func (s *RenderSuite) TestRawCopiesTheValueAndRefusesTheRisky() {
	out, ps := s.render(tmpl([]string{"a ", " b"}, hole("x", "", "raw", "!?")))
	s.Empty(ps)
	s.Equal("a !? b", out)

	_, ps = s.render(tmpl([]string{"", ""}, hole("x", "", "raw", "")))
	s.Equal([]types.DiagnosticCode{types.EmitRefusedValue}, s.codes(ps))
	_, ps = s.render(tmpl([]string{"", ""}, hole("x", "", "raw", "a\nb")))
	s.Equal([]types.DiagnosticCode{types.EmitRefusedValue}, s.codes(ps))
}

func (s *RenderSuite) TestBangSWritesAScalarAsText() {
	out, ps := s.render(tmpl([]string{"port ", " ", " ", ""},
		hole("port", "s", "raw", 8080), hole("on", "s", "raw", true), hole("r", "s", "raw", 1.5)))
	s.Empty(ps)
	s.Equal("port 8080 true 1.5", out)

	_, ps = s.render(tmpl([]string{"", ""}, hole("t", "s", "raw", types.Template{Strings: []string{""}})))
	s.Equal([]types.DiagnosticCode{types.EmitWrongKind}, s.codes(ps))
}

func (s *RenderSuite) TestOtherConversionsAreRefused() {
	for _, c := range []string{"r", "a"} {
		_, ps := s.render(tmpl([]string{"", ""}, hole("x", c, "raw", "v")))
		s.Equal([]types.DiagnosticCode{types.EmitConversion}, s.codes(ps))
	}
}

func (s *RenderSuite) TestKindsAreChecked() {
	_, ps := s.render(tmpl([]string{"", ""}, hole("port", "", "raw", 80)))
	s.Equal([]types.DiagnosticCode{types.EmitWrongKind}, s.codes(ps))
	s.Contains(ps[0].Message, "{{port!s:raw}}")

	_, ps = s.render(tmpl([]string{"", ""}, hole("body", "", "block", "text")))
	s.Equal([]types.DiagnosticCode{types.EmitWrongKind}, s.codes(ps))
	s.Contains(ps[0].Message, `emit\text`)

	_, ps = s.render(tmpl([]string{"", ""}, hole("n", "", "ident", types.Template{Strings: []string{""}})))
	s.Equal([]types.DiagnosticCode{types.EmitWrongKind}, s.codes(ps))
}

func (s *RenderSuite) TestAHoleWithNoEncodingIsRefused() {
	_, ps := s.render(tmpl([]string{"", ""}, hole("pkg", "", "", "p")))
	s.Equal([]types.DiagnosticCode{types.EmitNoEncoding}, s.codes(ps))
}

func (s *RenderSuite) TestAnUnknownEncodingNamesWhatExists() {
	_, ps := s.render(tmpl([]string{"", ""}, hole("v", "", "comment", "p")))
	s.Equal([]types.DiagnosticCode{types.EmitUnknownEncoding}, s.codes(ps))
	s.Contains(ps[0].Message, "block, ident, raw, string")

	_, ps = Render(tmpl([]string{"", ""}, hole("v", "", "string", "p")), "a.yaml", goIndex())
	s.Equal([]types.DiagnosticCode{types.EmitUnknownEncoding}, s.codes(ps))
	s.Contains(ps[0].Message, ".yaml")
}

func (s *RenderSuite) TestEncodingsRefuseWhatTheyCannotWrite() {
	_, ps := s.render(tmpl([]string{"", ""}, hole("n", "", "ident", "type")))
	s.Equal([]types.DiagnosticCode{types.EmitRefusedValue}, s.codes(ps))
	_, ps = s.render(tmpl([]string{"", ""}, hole("n", "", "ident", "two words")))
	s.Equal([]types.DiagnosticCode{types.EmitRefusedValue}, s.codes(ps))
}

func (s *RenderSuite) TestABlockCarriesTheHoleIndentation() {
	inner := tmpl([]string{"if x {\n\treturn\n}\n"})
	out, ps := s.render(tmpl([]string{"func f() {\n\t", "\n}\n"}, hole("body", "", "block", inner)))
	s.Empty(ps)
	s.Equal("func f() {\n\tif x {\n\t\treturn\n\t}\n}\n", out)
}

func (s *RenderSuite) TestABlockLeavesEmptyLinesEmpty() {
	inner := tmpl([]string{"a\n\nb\n"})
	out, ps := s.render(tmpl([]string{"  ", "\n"}, hole("body", "", "block", inner)))
	s.Empty(ps)
	s.Equal("  a\n\n  b\n", out)
}

func (s *RenderSuite) TestAOneLineBlockIsInline() {
	inner := tmpl([]string{"a, b\n"})
	out, ps := s.render(tmpl([]string{"f(", ") // end\n"}, hole("args", "", "block", inner)))
	s.Empty(ps)
	s.Equal("f(a, b) // end\n", out)
}

func (s *RenderSuite) TestAMultiLineBlockSharingALineIsRefused() {
	inner := tmpl([]string{"a\nb\n"})
	_, ps := s.render(tmpl([]string{"x = ", "\n"}, hole("body", "", "block", inner)))
	s.Equal([]types.DiagnosticCode{types.EmitBlockShape}, s.codes(ps))
	s.Equal(2, ps[0].Line)

	_, ps = s.render(tmpl([]string{"\t", " tail\n"}, hole("body", "", "block", inner)))
	s.Equal([]types.DiagnosticCode{types.EmitBlockShape}, s.codes(ps))
}

func (s *RenderSuite) TestAnEmptyBlockOnItsOwnLineRemovesTheLine() {
	empty := tmpl([]string{""})
	out, ps := s.render(tmpl([]string{"a\n\t", "\nb\n"}, hole("opt", "", "block", empty)))
	s.Empty(ps)
	s.Equal("a\nb\n", out)

	out, ps = s.render(tmpl([]string{"a\nx", "y\n"}, hole("opt", "", "block", empty)))
	s.Empty(ps)
	s.Equal("a\nxy\n", out)
}

func (s *RenderSuite) TestBlocksNestAndRenderWithTheSameEncodings() {
	leaf := tmpl([]string{"name := ", "\n"}, hole("v", "", "string", "q\"x"))
	mid := tmpl([]string{"if x {\n\t", "\n}\n"}, hole("leaf", "", "block", leaf))
	out, ps := s.render(tmpl([]string{"func f() {\n\t", "\n}\n"}, hole("mid", "", "block", mid)))
	s.Empty(ps)
	s.Equal("func f() {\n\tif x {\n\t\tname := \"q\\\"x\"\n\t}\n}\n", out)
}

func (s *RenderSuite) TestANestedRefusalSaysWhereItSits() {
	leaf := types.Template{Strings: []string{"\n", ""}, Interpolations: []types.Interpolation{hole("n", "", "ident", "type")}, Source: "leaf.go.tmpl"}
	_, ps := s.render(tmpl([]string{"", ""}, hole("leaf", "", "block", leaf)))
	s.Equal([]types.DiagnosticCode{types.EmitRefusedValue}, s.codes(ps))
	s.Contains(ps[0].Message, "leaf.go.tmpl:3")
}

func (s *RenderSuite) TestEveryRefusalIsReportedWithItsLine() {
	_, ps := s.render(tmpl([]string{"a\n", "\n", "\n", ""},
		hole("x", "", "", "v"), hole("y", "r", "raw", "v"), hole("z", "", "raw", 1)))
	s.Equal([]types.DiagnosticCode{types.EmitNoEncoding, types.EmitConversion, types.EmitWrongKind}, s.codes(ps))
	s.Equal([]int{3, 4, 5}, []int{ps[0].Line, ps[1].Line, ps[2].Line})
}

func (s *RenderSuite) TestAMalformedTemplateIsRefused() {
	_, ps := s.render(types.Template{Strings: []string{"a"}, Interpolations: []types.Interpolation{hole("x", "", "raw", "v")}})
	s.Equal([]types.DiagnosticCode{types.EmitBadShape}, s.codes(ps))
}

func (s *RenderSuite) TestAParsedFileRendersByteForByte() {
	tpl, probs := Load("a.go.tmpl", []byte("{{hole}}\npackage {{pkg:ident}}\n\n// {{doc:string}}\n"),
		map[string]any{"pkg": "price", "doc": "Round"})
	s.Empty(probs)
	out, ps := s.render(tpl)
	s.Empty(ps)
	s.Equal("package price\n\n// \"Round\"\n", out)
}

func (s *RenderSuite) TestJoinConcatenatesTemplatesAroundTheSeparator() {
	one := tmpl([]string{"case ", ":\n"}, hole("c", "", "string", "a"))
	two := tmpl([]string{"case ", ":\n"}, hole("c", "", "string", "b"))
	joined := Join([]types.Template{one, two}, "")
	s.Equal([]string{"case ", ":\ncase ", ":\n"}, joined.Strings)
	out, ps := s.render(tmpl([]string{"switch {\n\t", "\n}\n"}, hole("cases", "", "block", joined)))
	s.Empty(ps)
	s.Equal("switch {\n\tcase \"a\":\n\tcase \"b\":\n}\n", out)
}

func (s *RenderSuite) TestJoinPutsTheSeparatorBetweenPartsOnly() {
	a := tmpl([]string{"x"})
	b := tmpl([]string{"y"})
	out, ps := s.render(Join([]types.Template{a, b, a}, ", "))
	s.Empty(ps)
	s.Equal("x, y, x", out)

	empty := Join(nil, ", ")
	s.Equal([]string{""}, empty.Strings)
	s.Empty(empty.Interpolations)
}

func (s *RenderSuite) TestAHoleWithNoExpressionIsStillNamed() {
	_, ps := s.render(tmpl([]string{"", ""}, hole("", "", "", "x")))
	s.Require().Len(ps, 1)
	s.True(strings.HasPrefix(ps[0].Message, "an emit\\hole value with no expression names no encoding"), ps[0].Message)
}

func (s *RenderSuite) TestAnUnknownEncodingNamesTheLanguageWhenTheSpellIsUnknown() {
	index := map[string]spells.LanguageEncodings{".go": {Language: "go"}}
	_, ps := Render(tmpl([]string{"", ""}, hole("x", "", "path", "a")), "a.go", index)
	s.Require().Len(ps, 1)
	s.Equal(types.EmitUnknownEncoding, ps[0].Code)
	s.Equal("the spell declaring go declares no path encoding for .go (a.go); block, raw exist. add it to the spell's mgs_getLanguage, or encode in Buzz and name raw", ps[0].Message)
}
