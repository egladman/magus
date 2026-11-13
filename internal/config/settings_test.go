package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/config/gen/settings"
	"github.com/egladman/magus/libs/clikit/command"
)

// SettingsSuite pins the generated settings table against config.go.
type SettingsSuite struct{ suite.Suite }

func TestSettingsSuite(t *testing.T) { suite.Run(t, new(SettingsSuite)) }

func (s *SettingsSuite) TestTableIsNotEmpty() {
	s.NotEmpty(settings.Settings)
}

func (s *SettingsSuite) TestEveryEnvVarStartsWithMAGUS() {
	for _, f := range settings.Settings {
		s.Truef(strings.HasPrefix(f.EnvVar, "MAGUS_"), "%s: EnvVar %q", f.GoPath, f.EnvVar)
	}
}

func (s *SettingsSuite) TestNoEnvVarRepeats() {
	seen := make(map[string]string, len(settings.Settings))
	for _, f := range settings.Settings {
		prev, ok := seen[f.EnvVar]
		s.Falsef(ok, "%s names both %s and %s", f.EnvVar, prev, f.GoPath)
		seen[f.EnvVar] = f.GoPath
	}
}

func (s *SettingsSuite) TestNoGoPathRepeats() {
	seen := make(map[string]bool, len(settings.Settings))
	for _, f := range settings.Settings {
		s.Falsef(seen[f.GoPath], "%s repeats", f.GoPath)
		seen[f.GoPath] = true
	}
}

func (s *SettingsSuite) TestOptionalBoolHasNoFlag() {
	for _, f := range settings.Settings {
		if f.Kind == command.OptionalBool {
			s.Emptyf(f.Flag.Long, "%s is an optional bool", f.GoPath)
		}
	}
}

func (s *SettingsSuite) TestADurationSettingIsFound() {
	found := false
	for _, f := range settings.Settings {
		found = found || f.Kind == command.Duration
	}
	s.True(found, "no duration setting: the generator lost time.Duration")
}
