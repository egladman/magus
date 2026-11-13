package configuration

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/apiversion"
	"github.com/egladman/magus/libs/clikit/command"
)

type SettingSuite struct{ suite.Suite }

func TestSettingSuite(t *testing.T) { suite.Run(t, new(SettingSuite)) }

func (s *SettingSuite) TestStringNamesTheLongFlag() {
	got := Setting{EnvVar: "MAGUS_CACHE_DIR", YamlPath: "cache.dir", Flag: FlagNames{Long: "cache-dir"}}.String()
	s.Equal("MAGUS_CACHE_DIR (--cache-dir, cache.dir)", got)
}

func (s *SettingSuite) TestStringNamesTheShortFlagFirst() {
	got := Setting{EnvVar: "MAGUS_OUTPUT", YamlPath: "output", Flag: FlagNames{Long: "output", Short: "o"}}.String()
	s.Equal("MAGUS_OUTPUT (-o, --output, output)", got)
}

func (s *SettingSuite) TestStringSaysEnvOnly() {
	got := Setting{EnvVar: "MAGUS_HINTS_ENABLED", YamlPath: "hints.enabled"}.String()
	s.Equal("MAGUS_HINTS_ENABLED (env-only, hints.enabled)", got)
}

func (s *SettingSuite) TestDescribeWithFlag() {
	got := Setting{YamlPath: "cache.dir", EnvVar: "MAGUS_CACHE_DIR", Flag: FlagNames{Long: "cache-dir"}, Kind: command.String}.Describe()
	s.Equal("Env: MAGUS_CACHE_DIR\nFlags: --cache-dir\nConfig: cache.dir", got)
}

func (s *SettingSuite) TestDescribeWithShort() {
	got := Setting{YamlPath: "output", EnvVar: "MAGUS_OUTPUT", Flag: FlagNames{Long: "output", Short: "o"}}.Describe()
	s.Equal("Env: MAGUS_OUTPUT\nFlags: -o, --output\nConfig: output", got)
}

func (s *SettingSuite) TestDescribeEnvOnly() {
	got := Setting{YamlPath: "hints.enabled", EnvVar: "MAGUS_HINTS_ENABLED", Kind: command.OptionalBool}.Describe()
	s.Equal("Env: MAGUS_HINTS_ENABLED\nFlags: (env-only)\nConfig: hints.enabled", got)
}

type ConfigurationSpecSuite struct{ suite.Suite }

func TestConfigurationSpecSuite(t *testing.T) { suite.Run(t, new(ConfigurationSpecSuite)) }

var spec = ConfigurationSpec{Settings: []Setting{
	{YamlPath: "pinned"},
	{EnvVar: "APP_LEVEL", Internal: true},
}}

func (s *ConfigurationSpecSuite) TestVariableFindsTheSettingByName() {
	got, ok := spec.Variable("APP_LEVEL")
	s.True(ok)
	s.True(got.Internal)
}

func (s *ConfigurationSpecSuite) TestARecordNamesTheClikitConfigurationGroup() {
	header := apiversion.SpecHeader[ConfigurationSpec]()
	s.Equal(apiversion.APIVersion("clikit.configuration/v1alpha1"), header.APIVersion)
	s.True(header.APIVersion.Valid())
	s.Equal(apiversion.Kind("Configuration"), header.Kind)
}

func (s *ConfigurationSpecSuite) TestVariableFindsNoFileOnlyKey() {
	_, ok := spec.Variable("")
	s.False(ok)
}
