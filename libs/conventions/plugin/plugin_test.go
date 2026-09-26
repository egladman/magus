package plugin

import (
	"testing"

	"github.com/golangci/plugin-module-register/register"
)

// TestPluginsRegister builds every plugin from a yaml-shaped settings block, the
// path production runs, and checks the registered name matches the analyzer's:
// golangci-lint's settings key, enable entry and //nolint name all have to agree.
func TestPluginsRegister(t *testing.T) {
	for name, settings := range map[string]any{
		"asciistrings":  map[string]any{"files": []any{"types/*.go"}},
		"hostagnostic":  map[string]any{"skip-dirs": []any{"gen"}},
		"hostvocab":     map[string]any{"files": []any{"internal/guard/*.go"}, "words": []any{"Read"}},
		"importceiling": map[string]any{"rules": []any{map[string]any{"package": "a", "prefix": "b/", "max": 1}}},
		"nameoutput":    map[string]any{"package": "a", "case": "outputName", "emitters": []any{"emitNames"}},
		"ruletext":      map[string]any{"files": []any{"cmd/magus/shell.go"}, "prefix": "magus workspace:"},
		"stutter":       map[string]any{"min-package": 3},
		"testisolation": map[string]any{"package": "a", "calls": []any{"testkit.Main"}},
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
			if len(analyzers) != 1 || analyzers[0].Name != name {
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
// error rather than a silently ignored setting.
func TestUnknownKeyFails(t *testing.T) {
	build, err := register.GetPlugin("stutter")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := build(map[string]any{"min-package": 3, "alow": []any{"x"}}); err == nil {
		t.Fatal("expected an unknown settings key to fail")
	}
}
