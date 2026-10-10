package plugin

import (
	"strings"
	"testing"

	"github.com/egladman/magus/libs/conventions/internal/sourcetest"
	"github.com/golangci/plugin-module-register/register"
)

// TestPluginsRegister builds every plugin from a yaml-shaped settings block, the
// path production runs, and checks the registered name matches the analyzer's:
// golangci-lint's settings key, enable entry and //nolint name all have to agree.
func TestPluginsRegister(t *testing.T) {
	sourcetest.Module(t, "example.com/m", "cmd/app/main.go", "cmd/app/gen/gen.go")
	for name, settings := range map[string]any{
		"asciistrings": map[string]any{"files": []any{"types/*.go"}},
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
		"hostagnostic":  map[string]any{"module": "example.com/m", "skip-dirs": []any{"gen"}, "hosts": []any{"acme"}, "hint": "see docs"},
		"hostvocab":     map[string]any{"files": []any{"internal/guard/*.go"}, "words": []any{"Read"}, "hint": "see docs"},
		"importceiling": map[string]any{"rules": []any{map[string]any{"package": "a", "prefix": "b/", "max": 1}}},
		"nameoutput":    map[string]any{"package": "a", "case-ident": "outputName", "emitters": []any{"emitNames"}, "hint": "see docs"},
		"providerio":    map[string]any{"module": "example.com/m", "dirs": []any{"cmd/app"}, "hint": "see docs"},
		"ruletext":      map[string]any{"files": []any{"cmd/magus/shell.go"}, "prefix": "magus workspace:", "hint": "see docs"},
		"stutter":       map[string]any{"min-package-len": 3},
		"testisolation": map[string]any{"package": "a", "calls": []any{"testkit.Main"}, "hint": "see docs"},
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
