package buzzgen

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"testing"

	"github.com/stretchr/testify/suite"
)

type EnumSuite struct {
	suite.Suite
	pkg *types.Package
}

func TestEnum(t *testing.T) { suite.Run(t, new(EnumSuite)) }

const enumFixture = `package fixture

type WalkStep string

const (
	WalkNext    WalkStep = "next"
	WalkSkipDir WalkStep = "skipDir"
)

type Policy string

const (
	PolicyDefault Policy = ""
	PolicyExecutableBit Policy = "executable-bit"
	PolicyFixed Policy = "fixed"
)

type MediaType string

const MediaTypeManifest MediaType = "application/vnd.oci.image.manifest.v1+json"

type Kind string

const (
	KindSpawn    Kind = "spawn"
	KindContinue Kind = "continue"
)

type Plain string
`

func (s *EnumSuite) SetupSuite() {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", enumFixture, 0)
	s.Require().NoError(err)
	conf := types.Config{Importer: importer.Default()}
	s.pkg, err = conf.Check("fixture", fset, []*ast.File{file}, nil)
	s.Require().NoError(err)
}

func (s *EnumSuite) named(name string) *types.Named {
	return s.pkg.Scope().Lookup(name).Type().(*types.Named)
}

func (s *EnumSuite) cases(e Enum) []string {
	var out []string
	for _, c := range e.Cases {
		out = append(out, c.Name+"="+c.Value)
	}
	return out
}

func (s *EnumSuite) TestCasesComeFromValuesInDeclarationOrder() {
	none := ""
	e, err := DeriveEnum(s.named("WalkStep"), TypeRef{Name: "WalkStep", Settings: Settings{ZeroCase: &none}}, s.own())
	s.Require().NoError(err)
	s.Equal([]string{"next=next", "skipDir=skipDir"}, s.cases(e))
}

func (s *EnumSuite) TestTheZeroCaseComesFirstAndIsNamedNone() {
	e, err := DeriveEnum(s.named("Policy"), TypeRef{Name: "Policy"}, s.own())
	s.Require().NoError(err)
	s.Equal([]string{"none=", "executableBit=executable-bit", "fixed=fixed"}, s.cases(e))
	s.Equal("PolicyDefault", e.Cases[0].Const)
}

func (s *EnumSuite) TestAValueThatIsNoIdentifierIsNamedForItsConstant() {
	e, err := DeriveEnum(s.named("MediaType"), TypeRef{Name: "MediaType"}, s.own())
	s.Require().NoError(err)
	s.Equal("manifest", e.Cases[1].Name)
}

func (s *EnumSuite) TestAKeywordCaseIsRefused() {
	_, err := DeriveEnum(s.named("Kind"), TypeRef{Name: "Kind"}, s.own())
	s.ErrorContains(err, "a Buzz keyword; name it under cases")
}

func (s *EnumSuite) TestCasesOverrideADerivedName() {
	e, err := DeriveEnum(s.named("Kind"), TypeRef{Name: "Kind", Settings: Settings{Cases: map[string]string{"continue": "continuation"}}}, s.own())
	s.Require().NoError(err)
	s.Equal([]string{"none=", "spawn=spawn", "continuation=continue"}, s.cases(e))
}

func (s *EnumSuite) TestACasesKeyNoConstantDeclaresIsRefused() {
	_, err := DeriveEnum(s.named("Policy"), TypeRef{Name: "Policy", Settings: Settings{Cases: map[string]string{"gone": "gone"}}}, s.own())
	s.ErrorContains(err, `cases names value "gone"`)
}

func (s *EnumSuite) TestATypeWithNoConstantsIsRefused() {
	_, err := DeriveEnum(s.named("Plain"), TypeRef{Name: "Plain"}, s.own())
	s.ErrorContains(err, "declares no constants")
}

func (s *EnumSuite) TestCamelWordsKeepsAcronymsAndDigitsTogether() {
	s.Equal([]string{"HTTP", "Server", "AMD64"}, camelWords("HTTPServerAMD64"))
}

const casesElsewhereFixture = `package declared

import "fixture"

const (
	KindJob   fixture.Plain = "Job"
	KindAgent fixture.Plain = "Agent"
)
`

type importerFunc func(path string) (*types.Package, error)

func (f importerFunc) Import(path string) (*types.Package, error) { return f(path) }

func (s *EnumSuite) own() []*types.Package { return []*types.Package{s.pkg} }

func (s *EnumSuite) declaring() *types.Package {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "declared.go", casesElsewhereFixture, 0)
	s.Require().NoError(err)
	conf := types.Config{Importer: importerFunc(func(path string) (*types.Package, error) {
		if path == "fixture" {
			return s.pkg, nil
		}
		return importer.Default().Import(path)
	})}
	pkg, err := conf.Check("declared", fset, []*ast.File{file}, nil)
	s.Require().NoError(err)
	return pkg
}

func (s *EnumSuite) TestCasesComeFromAnotherPackageThanTheType() {
	none := ""
	ref := TypeRef{Name: "Plain", Settings: Settings{ZeroCase: &none}}
	e, err := DeriveEnum(s.named("Plain"), ref, []*types.Package{s.declaring()})
	s.Require().NoError(err)
	s.Equal([]string{"job=Job", "agent=Agent"}, s.cases(e))
	s.Equal("KindJob", e.Cases[0].Const)
}

func (s *EnumSuite) TestATypeWithNoCasesInAnyListedPackageIsRefused() {
	_, err := DeriveEnum(s.named("Plain"), TypeRef{Name: "Plain"}, s.own())
	s.ErrorContains(err, "declares no constants to derive cases from in fixture")
}

func (s *EnumSuite) TestNoPackageToReadFromIsRefused() {
	_, err := DeriveEnum(s.named("Kind"), TypeRef{Name: "Kind"}, nil)
	s.ErrorContains(err, "no package to read constants from")
}
