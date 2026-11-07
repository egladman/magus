package plugin

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"github.com/golangci/plugin-module-register/register"
	"github.com/stretchr/testify/suite"
	"gopkg.in/yaml.v3"
)

// TestPluginsRegister builds every plugin from a yaml-shaped settings block, the
// path production runs, and checks the analyzer name starts with the registered name (the filenames
// analyzer appends a vocabulary digest). The settings key, enable entry and
// //nolint name all use the registered name.
func TestPluginsRegister(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "cmd/app/main.go", "cmd/app/gen/gen.go")
	for name, settings := range map[string]any{
		"asciistrings": map[string]any{"module": "", "files": []any{"types/*.go"}},
		"benchbeside":  map[string]any{"module": "example.com/m"},
		"diagmsg": map[string]any{
			"max-runes": 160, "prefixes": []any{"magus workspace:"},
			"calls":  []any{map[string]any{"func": "a.Errorf", "arg": 1, "format": true}},
			"fields": []any{map[string]any{"type": "a.Verdict", "field": "Deny"}},
			"allow":  []any{map[string]any{"file": "cmd/app/main.go", "rule": "message-length", "reason": "staged"}},
		},
		"errmsg": map[string]any{
			"module": "example.com/m", "files": []any{"cmd/app/*.go"}, "rules": []any{"error-join"},
			"allow": []any{map[string]any{"file": "cmd/app/main.go", "rule": "error-join", "reason": "staged"}},
		},
		"fieldwise":     map[string]any{"report-partial": true},
		"filenames":     map[string]any{"module": "example.com/m", "skip-dirs": []any{"gen"}, "allow": []any{"runtime"}},
		"globalrestore": map[string]any{"module": "", "package": "a", "vars": []any{"global"}, "hint": "see docs"},
		"hostagnostic":  map[string]any{"module": "example.com/m", "skip-dirs": []any{"gen"}, "hosts": []any{"acme"}, "hint": "see docs"},
		"hostvocab":     map[string]any{"module": "", "files": []any{"internal/guard/*.go"}, "words": []any{"Read"}, "hint": "see docs"},
		"nameoutput":    map[string]any{"module": "", "package": "a", "case-ident": "outputName", "emitters": []any{"emitNames"}, "hint": "see docs"},
		"namewords":     map[string]any{"suffixes": []any{"Of"}, "short-forms": []any{"Cfg"}},
		"providerio":    map[string]any{"module": "example.com/m", "dirs": []any{"cmd/app"}, "hint": "see docs"},
		"ruletext":      map[string]any{"module": "", "files": []any{"cmd/magus/shell.go"}, "prefix": "magus workspace:", "hint": "see docs"},
		"secretfields":  map[string]any{"words": []any{"Token"}, "hint": "see docs"},
		"stderrprint": map[string]any{
			"module": "example.com/m", "usage-pattern": "(?i)usage$", "usage-fields": []any{"Usage"},
			"callee-packages": []any{"example.com/m"}, "display": []any{"example.com/m/tty"},
			"allow": []any{map[string]any{"file": "cmd/app/main.go", "reason": "the display"}},
		},
		"stringjoin":    map[string]any{"min-operands": 3, "hint": "see docs"},
		"stutter":       map[string]any{"min-package-len": 3},
		"testisolation": map[string]any{"module": "", "package": "a", "calls": []any{"testkit.Main"}, "hint": "see docs"},
		"testloops":     map[string]any{"markers": []any{"Setpgid"}, "hint": "see docs"},
	} {
		t.Run(name, func(t *testing.T) {
			build, err := register.GetPlugin(name)
			if err != nil {
				t.Fatal(err)
			}
			p, err := build(settings)
			if err != nil {
				t.Fatal(err)
			}
			analyzers, err := p.BuildAnalyzers()
			if err != nil {
				t.Fatal(err)
			}
			if len(analyzers) != 1 || !strings.HasPrefix(analyzers[0].Name, name) {
				t.Fatalf("plugin %q built %v", name, analyzers)
			}
			// Syntax loading leaves pass.Pkg nil, which every analyzer reads.
			if got := p.GetLoadMode(); got != register.LoadModeTypesInfo {
				t.Errorf("load mode = %q, want %q", got, register.LoadModeTypesInfo)
			}
		})
	}
}

// TestUnknownKeyFails pins DisallowUnknownFields: a misspelled key is a load
// error naming the linter rather than a silently ignored setting.
func TestUnknownKeyFails(t *testing.T) {
	build, err := register.GetPlugin("stutter")
	if err != nil {
		t.Fatal(err)
	}
	_, err = build(map[string]any{"min-package-len": 3, "alow": []any{"x"}})
	if err == nil || !strings.HasPrefix(err.Error(), "stutter: settings: ") {
		t.Fatalf("want an unknown settings key to fail naming the linter, got %v", err)
	}
}

type ScopeSuite struct {
	root string
	suite.Suite
}

func TestScopeSuite(t *testing.T) { suite.Run(t, new(ScopeSuite)) }

func (s *ScopeSuite) build(name string, settings any) (register.LinterPlugin, error) {
	newPlugin, err := register.GetPlugin(name)
	s.Require().NoError(err)
	return newPlugin(settings)
}

func (s *ScopeSuite) analyzerCount(p register.LinterPlugin) int {
	analyzers, err := p.BuildAnalyzers()
	s.Require().NoError(err)
	return len(analyzers)
}

func (s *ScopeSuite) TestModuleComesFromTheTreesGoMod() {
	sourcetest.Module(s.T(), "example.com/m", "cmd/app/main.go")
	p, err := s.build("errmsg", map[string]any{"files": []any{"cmd/app/*.go"}})
	s.Require().NoError(err)
	s.Equal(1, s.analyzerCount(p))
}

func (s *ScopeSuite) TestPathRuleNamingNoFileInTheTreeFailsLoudly() {
	sourcetest.Module(s.T(), "example.com/m", "cmd/app/main.go")
	_, err := s.build("errmsg", map[string]any{"files": []any{"cmd/gone/*.go"}})
	s.ErrorContains(err, `files pattern "cmd/gone/*.go" matches no file`)
}

func (s *ScopeSuite) TestTreeWithoutGoModIsAnError() {
	s.T().Chdir(s.T().TempDir())
	_, err := s.build("errmsg", map[string]any{})
	s.ErrorContains(err, "no go.mod at or above")
}

func (s *ScopeSuite) TestNoSettingsStillReadsTheTree() {
	s.T().Chdir(s.T().TempDir())
	_, err := s.build("filenames", nil)
	s.ErrorContains(err, "no go.mod at or above")
}

func (s *ScopeSuite) TestExplicitModuleOverridesTheTree() {
	sourcetest.Module(s.T(), "example.com/m", "cmd/app/main.go")
	_, err := s.build("errmsg", map[string]any{"module": "example.com/other"})
	s.ErrorContains(err, `module "example.com/other": no go.mod at or above`)
}

func (s *ScopeSuite) TestInModuleBlockAppliesToItsOwnModule() {
	sourcetest.Module(s.T(), "example.com/m", "cmd/app/main.go")
	_, err := s.build("errmsg", map[string]any{"in-module": map[string]any{
		"example.com/m": map[string]any{"allow": []any{map[string]any{"file": "cmd/gone/x.go", "reason": "staged"}}},
	}})
	s.ErrorContains(err, `allow pattern "cmd/gone/x.go" matches no file`)
}

func (s *ScopeSuite) TestInModuleBlockOfAnotherModuleIsIgnored() {
	sourcetest.Module(s.T(), "example.com/m", "cmd/app/main.go")
	p, err := s.build("errmsg", map[string]any{"notice-attrs": []any{"example.com/m/attr.Notice"}, "in-module": map[string]any{
		"example.com/other": map[string]any{"allow": []any{map[string]any{"file": "cmd/gone/x.go", "reason": "staged"}}},
	}})
	s.Require().NoError(err)
	s.Equal(1, s.analyzerCount(p))
}

func (s *ScopeSuite) TestInModuleBlockReachesANestedModule() {
	root := sourcetest.Module(s.T(), "example.com/m", "cmd/app/main.go")
	inner := filepath.Join(root, "libs", "inner")
	s.Require().NoError(os.MkdirAll(inner, 0o755))
	s.Require().NoError(os.WriteFile(filepath.Join(inner, "go.mod"), []byte("module example.com/m/libs/inner\n"), 0o644))
	s.T().Chdir(inner)

	p, err := s.build("errmsg", map[string]any{"in-module": map[string]any{
		"example.com/m": map[string]any{"allow": []any{map[string]any{"file": "cmd/app/main.go", "reason": "staged"}}},
	}})
	s.Require().NoError(err)
	s.Equal(1, s.analyzerCount(p))
}

func (s *ScopeSuite) TestLinterWithOnlyAnotherModulesSettingsIsInert() {
	sourcetest.Module(s.T(), "example.com/m", "cmd/app/main.go")
	p, err := s.build("ruletext", map[string]any{"in-module": map[string]any{
		"example.com/other": map[string]any{"files": []any{"cmd/magus/shell.go"}, "prefix": "magus workspace:"},
	}})
	s.Require().NoError(err)
	s.Zero(s.analyzerCount(p))
}

func (s *ScopeSuite) TestInModuleThatIsNotAMapIsAnError() {
	sourcetest.Module(s.T(), "example.com/m", "cmd/app/main.go")
	_, err := s.build("errmsg", map[string]any{"in-module": []any{"example.com/m"}})
	s.ErrorContains(err, "in-module maps a module path to its settings")
}

type repoConfig struct {
	Linters struct {
		Settings struct {
			Custom map[string]struct {
				Settings any `yaml:"settings"`
			} `yaml:"custom"`
		} `yaml:"settings"`
	} `yaml:"linters"`
}

func (s *ScopeSuite) buildRepoConfig() map[string]int {
	data, err := os.ReadFile(filepath.Join(s.root, ".golangci.yml"))
	s.Require().NoError(err)
	var cfg repoConfig
	s.Require().NoError(yaml.Unmarshal(data, &cfg))
	built := map[string]int{}
	for name, linter := range cfg.Linters.Settings.Custom {
		if _, err := register.GetPlugin(name); err != nil {
			continue
		}
		p, err := s.build(name, linter.Settings)
		s.Require().NoError(err, name)
		built[name] = s.analyzerCount(p)
	}
	return built
}

func (s *ScopeSuite) TestRepoConfigBuildsInsideItsOwnTree() {
	wd, err := os.Getwd()
	s.Require().NoError(err)
	s.root = filepath.Join(wd, "..", "..", "..")
	built := s.buildRepoConfig()
	s.Equal(1, built["ruletext"])
	s.Equal(1, built["errmsg"])
	s.Equal(1, built["diagmsg"])
}

func (s *ScopeSuite) TestRepoConfigBuildsInAnotherModule() {
	wd, err := os.Getwd()
	s.Require().NoError(err)
	s.root = filepath.Join(wd, "..", "..", "..")
	sourcetest.Module(s.T(), "example.com/scratch", "cmd/app/main.go")
	built := s.buildRepoConfig()
	s.Equal(1, built["errmsg"])
	s.Zero(built["ruletext"])
	s.Zero(built["providerio"])
}
