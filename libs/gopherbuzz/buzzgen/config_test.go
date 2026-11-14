package buzzgen

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
)

type ConfigSuite struct {
	suite.Suite
}

func TestConfig(t *testing.T) { suite.Run(t, new(ConfigSuite)) }

const layeredConfig = `version: v1alpha1
zeroCase: unset
pkgname: bindgen
packages:
  example.com/a:
    config: {module: fs, cases: {x: ex}}
    types:
      Step: {config: {name: WalkStep, cases: {y: why}}}
      Plain: {}
outputs:
  bindings: {kind: go, path: gen}
  decode: {kind: go-decode, path: gen/decode, pkgname: decode}
`

func (s *ConfigSuite) parse(text string) (Config, error) {
	return ParseConfig(strings.NewReader(text))
}

func (s *ConfigSuite) TestATypeInheritsItsPackageAndTheTopLevel() {
	cfg, err := s.parse(layeredConfig)
	s.Require().NoError(err)
	refs := cfg.Types()
	s.Require().Len(refs, 2)
	plain, step := refs[0], refs[1]
	s.Equal("Plain", plain.BuzzName())
	s.Equal("unset", plain.ZeroCase())
	s.Equal("fs", *plain.Settings.Module)
	s.Equal("WalkStep", step.BuzzName())
	s.Equal(map[string]string{"x": "ex", "y": "why"}, step.Settings.Cases)
}

func (s *ConfigSuite) TestAnOutputPkgnameOverridesTheTopLevel() {
	cfg, err := s.parse(layeredConfig)
	s.Require().NoError(err)
	s.Equal("bindgen", cfg.PkgName("bindings"))
	s.Equal("decode", cfg.PkgName("decode"))
}

func (s *ConfigSuite) TestTheZeroCaseDefaultsToNone() {
	cfg, err := s.parse("version: v1alpha1\npackages: {p: {types: {T: {}}}}\n")
	s.Require().NoError(err)
	s.Equal(DefaultZeroCase, cfg.Types()[0].ZeroCase())
}

func (s *ConfigSuite) TestAnUnlistedTypeTakesItsPackageSettings() {
	cfg, err := s.parse(layeredConfig)
	s.Require().NoError(err)
	ref := cfg.Ref("example.com/a", "Reached")
	s.Equal("Reached", ref.BuzzName())
	s.Equal("fs", *ref.Settings.Module)
	s.Equal("unset", ref.ZeroCase())
	s.Equal("WalkStep", cfg.Ref("example.com/a", "Step").BuzzName())
}

const namespacedConfig = `version: v1alpha1
module: shop
packages:
  example.com/types:
    types:
      Job: {}
  example.com/types/order:
    types:
      Step: {}
  example.com/types/cart:
    config: {module: ""}
  example.com/types/image:
    config: {module: image}
  example.com/types/till:
    types:
      Line: {config: {module: http}}
`

func (s *ConfigSuite) TestAChildPackageDerivesItsNamespace() {
	cfg, err := s.parse(namespacedConfig)
	s.Require().NoError(err)
	step := cfg.Ref("example.com/types/order", "Step")
	s.Equal("shop/order", *step.Settings.Module)
	s.Equal(`shop\order`, step.Namespace())
	s.Equal(`shop\order`, cfg.Ref("example.com/types/order", "Unlisted").Namespace())
}

func (s *ConfigSuite) TestTheTopLevelModuleIsNoDefault() {
	cfg, err := s.parse(namespacedConfig)
	s.Require().NoError(err)
	s.Empty(cfg.Ref("example.com/types", "Job").Namespace())
	s.Empty(cfg.Ref("example.org/other", "T").Namespace(), "a package whose parent is unlisted derives nothing")
}

func (s *ConfigSuite) TestAPackageModuleWinsOverTheDerivedOne() {
	cfg, err := s.parse(namespacedConfig)
	s.Require().NoError(err)
	s.Equal("image", cfg.Ref("example.com/types/image", "Platform").Namespace())
	s.Empty(cfg.Ref("example.com/types/cart", "Line").Namespace(), "an empty module keeps the type bundled")
	s.Equal("http", cfg.Ref("example.com/types/till", "Line").Namespace())
	s.Equal(`shop\till`, cfg.Ref("example.com/types/till", "Other").Namespace())
}

func (s *ConfigSuite) TestAMirrorNamesTheRecordATypeCrossesAs() {
	cfg, err := s.parse("version: v1alpha1\npackages: {p: {types: {Status: {config: {mirror: StatusRecord}}}}}\n")
	s.Require().NoError(err)
	s.Equal("StatusRecord", cfg.Ref("p", "Status").MirrorName())
	s.Empty(cfg.Ref("p", "Other").MirrorName())
}

func (s *ConfigSuite) TestOnlyATypeMaySetMirror() {
	_, err := s.parse("version: v1alpha1\nmirror: X\npackages: {p: {config: {mirror: Y}}}\n")
	s.ErrorContains(err, "mirror is set at the top level; only a type may set it")
	s.ErrorContains(err, "packages.p.config.mirror is set; only a type may set it")
}

func (s *ConfigSuite) TestAnUnknownKeyIsRefused() {
	_, err := s.parse("version: v1alpha1\nstray: 1\n")
	s.ErrorContains(err, "field stray not found")
}

func (s *ConfigSuite) TestEveryProblemIsReportedAtOnce() {
	_, err := s.parse(`version: v9
name: Top
modules: {dynamic: {merge.deep: " "}}
outputs: {x: {kind: rust, path: ""}}
`)
	s.Require().Error(err)
	for _, want := range []string{`version is "v9"`, "name is set at the top level", "merge.deep has no reason", `kind is "rust"`, "outputs.x has no path"} {
		s.ErrorContains(err, want)
	}
}

func (s *ConfigSuite) TestCasesFromLayersAndIsLoadedWithThePackages() {
	cfg, err := s.parse(`version: v1alpha1
packages:
  example.com/a:
    config: {casesFrom: [example.com/b]}
    types:
      Kind: {config: {casesFrom: [example.com/c, example.com/a]}}
      Other: {}
`)
	s.Require().NoError(err)
	refs := cfg.Types()
	s.Equal([]string{"example.com/c", "example.com/a"}, refs[0].Settings.CasesFrom)
	s.Equal([]string{"example.com/b"}, refs[1].Settings.CasesFrom)
	s.Equal([]string{"example.com/a", "example.com/b", "example.com/c"}, cfg.PackagePaths())
}
