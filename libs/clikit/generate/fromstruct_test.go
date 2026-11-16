package generate

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/configuration"
)

type fixtureLevel string

func (l *fixtureLevel) UnmarshalText(b []byte) error {
	*l = fixtureLevel(b)
	return nil
}

type fixtureMode string

type fixtureEmbedded struct{ X string }

type fixtureConfig struct {
	fixtureEmbedded
	// Where state lives.
	StateDir string `yaml:"state_dir" cli:"short=s"`
	Secret   string `yaml:"-"`
	// Pinned in the file.
	Pinned  string        `yaml:"pinned" cli:"-"`
	Retries int           `yaml:"retries" cli:"floor"`
	Level   fixtureLevel  `yaml:"level" cli:"floor"`
	Mode    fixtureMode   `yaml:"mode"`
	Ratio   float64       `yaml:"ratio"`
	Timeout time.Duration `yaml:"timeout"`
	Strict  *bool         `yaml:"strict"`
	Charms  []string      `yaml:"charms"`
	// A "quoted" and `backticked` description, deliberately overlong, exceeding
	// the hundred-and-twenty-character allowance generated usages respect
	// before truncation removes everything afterwards
	Long    string            `yaml:"long"`
	Headers map[string]string `yaml:"headers"`
	Ports   []int             `yaml:"ports"`
	Allow   []fixtureAllow    `yaml:"allow"`
	Cache   fixtureCache      `yaml:"cache"`
}

type fixtureAllow struct {
	Name string `yaml:"name"`
	// The path it may read.
	Path string `yaml:"path"`
}

type fixtureCache struct {
	// Dir is the directory holding cached artifacts.
	Dir  string `yaml:"dir"`
	Mode string `yaml:"mode" cli:"name=cache"`
}

type FromStructSuite struct {
	suite.Suite
	settings []configuration.Setting
}

func TestFromStructSuite(t *testing.T) { suite.Run(t, new(FromStructSuite)) }

func (s *FromStructSuite) SetupTest() {
	defaults := fixtureConfig{Retries: 3, Timeout: 90 * time.Minute, Charms: []string{"a", "b"}}
	var err error
	s.settings, err = FromStruct(defaults, "APP", "fromstruct_test.go")
	s.Require().NoError(err)
}

func (s *FromStructSuite) setting(yamlPath string) configuration.Setting {
	for _, got := range s.settings {
		if got.YamlPath == yamlPath {
			return got
		}
	}
	s.FailNow("no setting", yamlPath)
	return configuration.Setting{}
}

func (s *FromStructSuite) absent(yamlPath string) {
	for _, got := range s.settings {
		s.NotEqual(yamlPath, got.YamlPath)
	}
}

func (s *FromStructSuite) TestAScalarGetsAVariableAndAFlag() {
	s.Equal(configuration.Setting{
		GoPath: "StateDir", YamlPath: "state_dir", EnvVar: "APP_STATE_DIR",
		Flag: configuration.FlagNames{Long: "state-dir", Short: "s"}, Kind: command.String,
		Usage: "APP_STATE_DIR: Where state lives.", Doc: "Where state lives",
	}, s.setting("state_dir"))
}

func (s *FromStructSuite) TestANestedFieldJoinsItsPath() {
	got := s.setting("cache.dir")
	s.Equal("Cache.Dir", got.GoPath)
	s.Equal("APP_CACHE_DIR", got.EnvVar)
	s.Equal("cache-dir", got.Flag.Long)
	s.Equal("The directory holding cached artifacts", got.Doc)
}

func (s *FromStructSuite) TestANameTagReplacesThePathInBothNames() {
	got := s.setting("cache.mode")
	s.Equal("APP_CACHE", got.EnvVar)
	s.Equal("cache", got.Flag.Long)
}

func (s *FromStructSuite) TestDefaultIsTheValueInDefaults() {
	s.Equal("3", s.setting("retries").Default)
	s.Equal("1h30m", s.setting("timeout").Default)
	s.Equal("a,b", s.setting("charms").Default)
	s.Empty(s.setting("ratio").Default)
}

func (s *FromStructSuite) TestAListGetsAVariableAndNoFlag() {
	got := s.setting("charms")
	s.Equal(command.StringList, got.Kind)
	s.Equal("APP_CHARMS", got.EnvVar)
	s.Empty(got.Flag.Long)
}

func (s *FromStructSuite) TestAnOptionalBoolGetsAVariableAndNoFlag() {
	got := s.setting("strict")
	s.Equal(command.OptionalBool, got.Kind)
	s.Equal("APP_STRICT", got.EnvVar)
	s.Empty(got.Flag.Long)
}

func (s *FromStructSuite) TestAnOptedOutFieldIsFileOnly() {
	got := s.setting("pinned")
	s.Empty(got.EnvVar)
	s.Empty(got.Flag.Long)
	s.Equal("Pinned in the file.", got.Usage)
}

func (s *FromStructSuite) TestANamedTypeThatReadsNoTextIsFileOnly() {
	got := s.setting("mode")
	s.Equal(command.String, got.Kind)
	s.Empty(got.EnvVar)
}

func (s *FromStructSuite) TestATextTypeGetsAVariableAndAFlag() {
	got := s.setting("level")
	s.Equal(command.Text, got.Kind)
	s.Equal("APP_LEVEL", got.EnvVar)
	s.Equal("level", got.Flag.Long)
}

func (s *FromStructSuite) TestAStringMapIsFileOnly() {
	got := s.setting("headers")
	s.Equal(command.StringMap, got.Kind)
	s.Empty(got.EnvVar)
}

func (s *FromStructSuite) TestASliceElementIsAddressedByName() {
	got := s.setting("allow.<name>.path")
	s.Equal("Allow.[].Path", got.GoPath)
	s.Empty(got.EnvVar)
	s.Empty(got.Default)
	s.Equal("The path it may read", got.Doc)
}

func (s *FromStructSuite) TestFloorReachesOnlyAText() {
	s.True(s.setting("level").Floor)
	s.False(s.setting("retries").Floor, "an int has no WeakerThan to floor by")
}

func (s *FromStructSuite) TestAYamlDashFieldIsSkipped() { s.absent("secret") }

func (s *FromStructSuite) TestAnEmbeddedFieldIsSkipped() { s.absent("x") }

func (s *FromStructSuite) TestASliceOfScalarsOtherThanStringIsSkipped() { s.absent("ports") }

func (s *FromStructSuite) TestUsageIsSafeForAGoStringLiteral() {
	got := s.setting("long").Usage
	s.NotContains(got, `"`)
	s.NotContains(got, "`")
	s.Len(got, len("APP_LONG: ")+120)
	s.True(strings.HasSuffix(got, "..."), got)
}

func (s *FromStructSuite) TestANonStructIsRefused() {
	_, err := FromStruct(3, "APP", "")
	s.ErrorContains(err, "int is not a struct")
}

func (s *FromStructSuite) TestAMissingSourceIsRefused() {
	_, err := FromStruct(fixtureConfig{}, "APP", "no-such-file.go")
	s.ErrorContains(err, "parse no-such-file.go")
}

func (s *FromStructSuite) TestASourceLackingTheStructIsRefused() {
	_, err := FromStruct(fixtureMissing{}, "APP", "fromstruct_test.go")
	s.ErrorContains(err, "declares no struct fixtureMissing")
}

func (s *FromStructSuite) TestNoSourceLeavesTheDocsEmpty() {
	settings, err := FromStruct(fixtureConfig{}, "APP", "")
	s.Require().NoError(err)
	s.Equal("APP_STATE_DIR", settings[0].Usage)
	s.Empty(settings[0].Doc)
}

func (s *FromStructSuite) TestEnvNameUppercasesAndJoins() {
	s.Equal("APP_CACHE_DIR", EnvName("APP", "cache", "dir"))
	s.Equal("NO_CACHE", EnvName("", "no-cache"))
}

func (s *FromStructSuite) TestFlagNameJoinsWithHyphens() {
	s.Equal("cache-max-size", FlagName("cache", "max_size"))
}
