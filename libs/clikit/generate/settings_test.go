package generate

import (
	"go/parser"
	"go/token"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/libs/clikit/command"
	"github.com/egladman/magus/libs/clikit/configuration"
)

// fixtureMissing lives outside fromstruct_test.go, the source the docs
// are read from, so that source declares no such struct.
type fixtureMissing struct{ A string }

var fixtureTarget = Target{Import: "example.com/app/config", Type: "config.Config"}

type SettingsSuite struct {
	suite.Suite
	settings []configuration.Setting
}

func TestSettingsSuite(t *testing.T) { suite.Run(t, new(SettingsSuite)) }

func (s *SettingsSuite) SetupTest() {
	defaults := fixtureConfig{Retries: 3, Timeout: 90 * time.Minute}
	var err error
	s.settings, err = FromStruct(defaults, "APP", "fromstruct_test.go")
	s.Require().NoError(err)
}

func (s *SettingsSuite) parses(src []byte, pkg string) string {
	f, err := parser.ParseFile(token.NewFileSet(), "gen.go", src, parser.PackageClauseOnly)
	s.Require().NoError(err)
	s.Equal(pkg, f.Name.Name)
	return string(src)
}

func (s *SettingsSuite) TestTheTableCarriesEverySetting() {
	src, err := SettingsTable(s.settings, "settings")
	s.Require().NoError(err)
	got := s.parses(src, "settings")
	s.Contains(got, `Flag:     configuration.FlagNames{Long: "state-dir", Short: "s"},`)
	s.Contains(got, "Kind:     command.OptionalBool,")
	s.Contains(got, "Kind:     command.StringMap,")
	s.Contains(got, `Default:  "1h30m",`)
	s.Contains(got, "Floor:    true,")
	s.Contains(got, `YamlPath: "allow.<name>.path",`)
}

func (s *SettingsSuite) TestTheTableRefusesAnUnknownKind() {
	_, err := SettingsTable([]configuration.Setting{{YamlPath: "cache.dir", Kind: "mystery"}}, "p")
	s.ErrorContains(err, `cache.dir: kind "mystery" is no setting kind`)
}

func (s *SettingsSuite) TestApplyEnvReadsEachVariableByKind() {
	src, err := ApplyEnv(s.settings, "configgen", fixtureTarget)
	s.Require().NoError(err)
	got := s.parses(src, "configgen")
	s.Contains(got, "func ApplyEnv(cfg *config.Config, getenv func(string) string) error {")
	s.Contains(got, `if n, err := strconv.Atoi(v); err != nil {`)
	s.Contains(got, "cfg.Strict = &b")
	s.Contains(got, `strings.Split(v, ",")`)
	s.Contains(got, "} else if cfg.Level.WeakerThan(floor) {")
	s.Contains(got, "func parseBoolEnv(v string) (bool, error) {")
	s.NotContains(got, "APP_PINNED", "a file-only key has no variable")
}

func (s *SettingsSuite) TestApplyEnvCallsTheEnvCheckFirst() {
	target := fixtureTarget
	target.EnvCheck = "config.RetiredEnv"
	src, err := ApplyEnv(s.settings, "configgen", target)
	s.Require().NoError(err)
	s.Contains(string(src), "errs := []error{config.RetiredEnv(getenv)}")
}

func (s *SettingsSuite) TestApplyEnvRefusesATargetWithNoType() {
	_, err := ApplyEnv(s.settings, "configgen", Target{Import: "example.com/app/config"})
	s.ErrorContains(err, "names no struct")
}

func (s *SettingsSuite) TestApplyEnvRefusesAVariableOnAMap() {
	settings := []configuration.Setting{{GoPath: "Headers", EnvVar: "APP_HEADERS", Kind: command.StringMap}}
	_, err := ApplyEnv(settings, "configgen", fixtureTarget)
	s.ErrorContains(err, "APP_HEADERS: a string-map setting has no variable form")
}

func (s *SettingsSuite) TestBindersAddsBindFlagsForTheSettings() {
	src, err := Binders(command.Command{Name: "app"}, "flaggen", WithSettings(s.settings, fixtureTarget))
	s.Require().NoError(err)
	got := s.parses(src, "flaggen")
	s.Contains(got, `"example.com/app/config"`)
	s.Contains(got, `fs.StringVar(&cfg.StateDir, "s", cfg.StateDir, "Short for --state-dir")`)
	s.Contains(got, `fs.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout, "APP_TIMEOUT")`)
	s.Contains(got, `fs.TextVar(&cfg.Level, "level", cfg.Level, "APP_LEVEL")`)
	s.NotContains(got, "cfg.Strict", "an optional bool has no flag")
}

func (s *SettingsSuite) TestBindersRefusesAFlagOnAMap() {
	settings := []configuration.Setting{{GoPath: "Headers", Flag: configuration.FlagNames{Long: "headers"}, Kind: command.StringMap}}
	_, err := Binders(command.Command{Name: "app"}, "flaggen", WithSettings(settings, fixtureTarget))
	s.ErrorContains(err, "--headers: a string-map setting has no flag form")
}
