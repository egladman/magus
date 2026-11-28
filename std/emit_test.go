//go:build !wasm

package std

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/libs/safetyscissors/sandbox/filesystem"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type EmitModuleSuite struct {
	suite.Suite
	root string
}

func TestEmitModule(t *testing.T) { suite.Run(t, new(EmitModuleSuite)) }

func (s *EmitModuleSuite) SetupTest() {
	s.root = filesystem.ResolveRulePath(s.T().TempDir())
	s.write("tmpl/pkg.go.tmpl", "{{hole}}\npackage {{pkg:ident}}\n\nconst Name = {{name:string}}\n")
}

func (s *EmitModuleSuite) write(rel, body string) {
	abs := filepath.Join(s.root, filepath.FromSlash(rel))
	s.Require().NoError(os.MkdirAll(filepath.Dir(abs), 0o755))
	s.Require().NoError(os.WriteFile(abs, []byte(body), 0o644))
}

func (s *EmitModuleSuite) read(rel string) string {
	body, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(rel)))
	s.Require().NoError(err)
	return string(body)
}

func goSpell() *spells.Spell {
	return spells.NewSpell("golang",
		spells.WithLanguage("go"),
		spells.WithLanguageExtensions([]string{".go"}),
		spells.WithSyntax(&spells.Syntax{Encodings: []spells.Encoding{
			{Name: "ident", Pattern: `[\p{L}_][\p{L}\p{Nd}_]*`, Reserved: []string{"type", "range"}},
			{Name: "string", Open: `"`, Close: `"`, Control: `\x{hex2}`, Escapes: []spells.Escape{
				{Text: `\`, To: `\\`}, {Text: `"`, To: `\"`}, {Text: "\n", To: `\n`},
			}},
		}}))
}

// inRoot is a run in s.root carrying the go language, as magus buzz carries the
// workspace's.
func (s *EmitModuleSuite) inRoot() context.Context {
	return WithLanguages(WithCurrentWorkingDirectory(context.Background(), s.root), []*spells.Spell{goSpell()})
}

func (s *EmitModuleSuite) load(ctx context.Context, rel string, values map[string]any) types.Result[types.Template] {
	r, err := EmitLoad(ctx, rel, values)
	s.Require().NoError(err)
	return r
}

func (s *EmitModuleSuite) loaded(values map[string]any) types.Template {
	r := s.load(s.inRoot(), "tmpl/pkg.go.tmpl", values)
	s.Require().True(r.IsOk(), "%+v", r.Error)
	return r.Value
}

func (s *EmitModuleSuite) plan(ctx context.Context, files ...types.EmitFile) types.EditPlan {
	p, err := EmitPlan(ctx, files)
	s.Require().NoError(err)
	return p
}

func (s *EmitModuleSuite) TestLoadBindsEveryHole() {
	t := s.loaded(map[string]any{"pkg": "price", "name": "say \"hi\""})

	s.Equal(types.Template{
		Strings: []string{"package ", "\n\nconst Name = ", "\n"},
		Interpolations: []types.Interpolation{
			{Value: "price", Expression: "pkg", FormatSpec: "ident"},
			{Value: "say \"hi\"", Expression: "name", FormatSpec: "string"},
		},
		Source: "tmpl/pkg.go.tmpl",
	}, t)
}

func (s *EmitModuleSuite) TestLoadReportsEveryProblemUnderACode() {
	r := s.load(s.inRoot(), "tmpl/pkg.go.tmpl", map[string]any{"pkg": "price", "extra": "x"})

	s.Require().False(r.IsOk())
	s.Equal(types.Diagnostic{
		Code: string(types.EmitMissingValue),
		Message: "tmpl/pkg.go.tmpl:4:14: [MGS1068] hole \"name\" has no value: add \"name\" to values\n" +
			"tmpl/pkg.go.tmpl: [MGS1069] value \"extra\" is read by no hole: remove it from values",
		URL:  types.CodeURL(types.EmitMissingValue),
		Hint: "fix each problem in the template or in values, then load again",
	}, *r.Error)
}

func (s *EmitModuleSuite) TestLoadReadsThroughTheReadPolicy() {
	ctx := sandbox.WithPolicy(context.Background(), &sandbox.Policy{FS: filesystem.Ruleset{Rules: []filesystem.Rule{
		{Path: filepath.Join(s.root, "open"), Read: true, Write: true},
	}}})
	ctx = WithCurrentWorkingDirectory(ctx, s.root)

	r := s.load(ctx, "tmpl/pkg.go.tmpl", map[string]any{"pkg": "price", "name": "x"})

	s.Require().False(r.IsOk())
	s.Equal(string(types.PathReadDenied), r.Error.Code)
}

func (s *EmitModuleSuite) TestTextHoldsNoHole() {
	t, err := EmitText(s.inRoot(), "{{pkg:ident}}")
	s.Require().NoError(err)
	s.Equal(types.Template{Strings: []string{"{{pkg:ident}}"}}, t)
}

func (s *EmitModuleSuite) TestHoleWritesAScalarInItsStringForm() {
	t, err := EmitHole(s.inRoot(), int64(8080), "raw", "port")
	s.Require().NoError(err)
	s.Equal(types.Template{Strings: []string{"", ""}, Interpolations: []types.Interpolation{
		{Value: int64(8080), Expression: "port", Conversion: "s", FormatSpec: "raw"}}}, t)

	out, err := EmitRender(s.inRoot(), t, "a.go")
	s.Require().NoError(err)
	s.Equal(types.Ok("8080"), out)
}

func (s *EmitModuleSuite) TestJoinPutsSepBetweenParts() {
	a, _ := EmitHole(s.inRoot(), "A", "ident", "a")
	b, _ := EmitHole(s.inRoot(), "B", "ident", "b")
	t, err := EmitJoin(s.inRoot(), []types.Template{a, b}, ", ")
	s.Require().NoError(err)

	out, err := EmitRender(s.inRoot(), t, "a.go")
	s.Require().NoError(err)
	s.Equal(types.Ok("A, B"), out)
}

func (s *EmitModuleSuite) TestRenderWritesTheFileText() {
	out, err := EmitRender(s.inRoot(), s.loaded(map[string]any{"pkg": "price", "name": "say \"hi\""}), "x/price.go")
	s.Require().NoError(err)
	s.Equal(types.Ok("package price\n\nconst Name = \"say \\\"hi\\\"\"\n"), out)
}

func (s *EmitModuleSuite) TestRenderRefusalNamesTheTemplateLineAndCode() {
	out, err := EmitRender(s.inRoot(), s.loaded(map[string]any{"pkg": "type", "name": "x"}), "x/price.go")
	s.Require().NoError(err)
	s.Require().False(out.IsOk())
	s.Equal(types.Diagnostic{
		Code:    string(types.EmitRefusedValue),
		Message: "tmpl/pkg.go.tmpl:2:9: [MGS1074] pkg: \"type\" is reserved, so :ident refuses it",
		URL:     types.CodeURL(types.EmitRefusedValue),
		Hint:    "fix each problem in the template or its values, then render again",
	}, *out.Error)
}

func (s *EmitModuleSuite) TestRenderWithoutLanguagesIsRefused() {
	ctx := WithCurrentWorkingDirectory(context.Background(), s.root)
	t, _ := EmitText(ctx, "x\n")

	out, err := EmitRender(ctx, t, "a.go")

	s.Require().NoError(err)
	s.Require().False(out.IsOk())
	s.Equal(string(types.EmitUnknownEncoding), out.Error.Code)
	s.NotEmpty(out.Error.Hint)
}

// A template held by another template's hole crosses from Buzz as a map of its
// members, and renders as a block all the same.
func (s *EmitModuleSuite) TestANestedTemplateFromBuzzRendersAsABlock() {
	outer := types.Template{Strings: []string{"func f() {\n\t", "\n}\n"}, Interpolations: []types.Interpolation{{
		Expression: "body", FormatSpec: "block", Value: map[string]any{
			"strings": []any{"a()\nb()\n"}, "interpolations": []any{}, "source": "",
		},
	}}}

	out, err := EmitRender(s.inRoot(), outer, "a.go")

	s.Require().NoError(err)
	s.Equal(types.Ok("func f() {\n\ta()\n\tb()\n}\n"), out)
}

func (s *EmitModuleSuite) TestAMapThatIsNotATemplateIsRefusedByKind() {
	outer := types.Template{Strings: []string{"", "\n"}, Interpolations: []types.Interpolation{{
		Expression: "body", FormatSpec: "block", Value: map[string]any{"strings": []any{"a"}},
	}}}

	out, err := EmitRender(s.inRoot(), outer, "a.go")

	s.Require().NoError(err)
	s.Require().False(out.IsOk())
	s.Equal(string(types.EmitWrongKind), out.Error.Code)
}

func (s *EmitModuleSuite) TestFileCarriesPathAndTemplate() {
	t, _ := EmitText(s.inRoot(), "x\n")
	f, err := EmitFileOf(s.inRoot(), "a.go", t)
	s.Require().NoError(err)
	s.Equal(types.EmitFile{Path: "a.go", Template: t}, f)
}

// The create-only rule: a script loads a template, fills its holes and writes a
// file that did not exist, and a second run writes nothing.
func (s *EmitModuleSuite) TestPlanCreatesANewFileThatApplyWrites() {
	f := types.EmitFile{Path: "price/price.go", Template: s.loaded(map[string]any{"pkg": "price", "name": "p"})}

	p := s.plan(s.inRoot(), f)

	s.Require().Empty(p.Refused)
	s.Equal([]types.EditChange{{Path: "price/price.go", New: "package price\n\nconst Name = \"p\"\n", Create: true}}, p.Changes)
	s.Equal([]string{"price"}, p.MakesDirs)
	s.NoFileExists(filepath.Join(s.root, "price/price.go"))

	r, err := SpliceApply(s.inRoot(), p)
	s.Require().NoError(err)
	s.Require().True(r.IsOk(), "%+v", r.Error)
	s.Equal("package price\n\nconst Name = \"p\"\n", s.read("price/price.go"))

	again := s.plan(s.inRoot(), f)
	s.Empty(again.Refused)
	s.True(again.NothingToDo)
}

func (s *EmitModuleSuite) TestPlanRefusesToOverwriteAFile() {
	s.write("price/price.go", "package mine\n")
	f := types.EmitFile{Path: "price/price.go", Template: s.loaded(map[string]any{"pkg": "price", "name": "p"})}

	p := s.plan(s.inRoot(), f)

	s.Equal([]types.EditRefusal{{Path: "price/price.go", Rule: string(types.EditPlanRefused),
		Reason: "exists and holds other bytes, and emit writes only new files: change it with splice, or remove it and plan again"}}, p.Refused)
	s.Empty(p.Digest)
	s.Empty(p.Files)
	r, err := SpliceApply(s.inRoot(), p)
	s.Require().NoError(err)
	s.Require().False(r.IsOk())
	s.Equal(string(types.EditPlanRefused), r.Error.Code)
	s.Equal("package mine\n", s.read("price/price.go"))
}

func (s *EmitModuleSuite) TestPlanRefusesAPathNamedTwice() {
	t, _ := EmitText(s.inRoot(), "x\n")

	p := s.plan(s.inRoot(), types.EmitFile{Path: "a.go", Template: t}, types.EmitFile{Path: "./a.go", Template: t})

	s.Equal([]types.EditRefusal{{Path: "./a.go", Rule: string(types.EmitDuplicatePath),
		Reason: "is written twice (files 1 and 2): keep one file per path"}}, p.Refused)
}

func (s *EmitModuleSuite) TestPlanRefusesAnOutputWithNoFinalNewline() {
	t, _ := EmitText(s.inRoot(), "x")

	p := s.plan(s.inRoot(), types.EmitFile{Path: "a.go", Template: t})

	s.Equal([]types.EditRefusal{{Path: "a.go", Rule: string(types.EmitNoFinalNewline),
		Reason: "does not end in a line feed: end a template built in Buzz with a newline"}}, p.Refused)
}

func (s *EmitModuleSuite) TestPlanPlacesARenderRefusalInTheTemplate() {
	f := types.EmitFile{Path: "a.go", Template: s.loaded(map[string]any{"pkg": "range", "name": "p"})}

	p := s.plan(s.inRoot(), f)

	s.Equal([]types.EditRefusal{{Path: "tmpl/pkg.go.tmpl", Line: 2, Column: 9, Rule: string(types.EmitRefusedValue),
		Reason: "pkg: \"range\" is reserved, so :ident refuses it (writing a.go)"}}, p.Refused)
	s.Empty(p.Files)
}

func (s *EmitModuleSuite) TestPlanWithoutLanguagesIsRefused() {
	t, _ := EmitText(context.Background(), "x\n")

	p := s.plan(WithCurrentWorkingDirectory(context.Background(), s.root), types.EmitFile{Path: "a.go", Template: t})

	s.Require().Len(p.Refused, 1)
	s.Equal(string(types.EmitUnknownEncoding), p.Refused[0].Rule)
}

// An emit plan asks for nothing splice\create could not: every change creates.
func (s *EmitModuleSuite) TestEveryChangeIsACreation() {
	a, _ := EmitText(s.inRoot(), "a\n")
	b, _ := EmitText(s.inRoot(), "b\n")

	p := s.plan(s.inRoot(), types.EmitFile{Path: "a.go", Template: a}, types.EmitFile{Path: "b/b.go", Template: b})

	s.Require().Len(p.Changes, 2)
	for _, c := range p.Changes {
		s.True(c.Create, c.Path)
		s.Empty(c.Old, c.Path)
	}
}

func (s *EmitModuleSuite) TestNoMethodRaises() {
	for _, m := range Emit.Methods {
		s.False(m.Raises, m.Name)
	}
	s.NoError(ValidateModule(Emit.WithTags()))
}
