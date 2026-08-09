package plugin

import (
	"testing"

	"github.com/golangci/plugin-module-register/register"
)

// TestNewPluginDecodesSettings drives the path production actually runs: a yaml
// settings block arrives as map[string]any and has to reach [testsprawl.Options].
// The transpose this replaced was a hand-written field copy, where a missed field
// compiled clean and silently ignored what the user configured - so the point of
// this test is that the json tags, not a copy, are what carry the values across.
func TestNewPluginDecodesSettings(t *testing.T) {
	p, err := newPlugin(map[string]any{
		"allow":    []any{"*_bench_test.go"},
		"unpaired": true,
	})
	if err != nil {
		t.Fatal(err)
	}

	analyzers, err := p.BuildAnalyzers()
	if err != nil {
		t.Fatal(err)
	}

	if len(analyzers) != 1 {
		t.Fatalf("want 1 analyzer, got %d", len(analyzers))
	}

	// The name is the golangci-lint config key: linters.settings.custom.<name> and
	// the entry in linters.enable both have to match what register.Plugin was given,
	// and a drift between them fails at config load rather than at compile time.
	if analyzers[0].Name != "testsprawl" {
		t.Errorf("analyzer name %q must match the registered plugin name", analyzers[0].Name)
	}
}

func TestNewPluginRejectsMalformedGlob(t *testing.T) {
	if _, err := newPlugin(map[string]any{"allow": []any{"[bad"}}); err == nil {
		t.Fatal("expected a malformed glob to fail at construction")
	}
}

// TestNewPluginRejectsUnknownKey pins register.DecodeSettings's DisallowUnknownFields
// behaviour: a misspelled settings key is a silent no-op in most linters, and here
// it is a load error naming the key.
func TestNewPluginRejectsUnknownKey(t *testing.T) {
	if _, err := newPlugin(map[string]any{"allowed": []any{"*_test.go"}}); err == nil {
		t.Fatal("expected an unknown settings key to fail")
	}
}

func TestNewPluginEmptySettings(t *testing.T) {
	if _, err := newPlugin(nil); err != nil {
		t.Fatalf("a plugin with no settings block must build: %v", err)
	}
}

func TestGetLoadMode(t *testing.T) {
	p, err := newPlugin(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Syntax, not TypesInfo: the analyzer matches file names and never consults
	// type information, and asking for types would make every consumer pay for a
	// full type-check to run a filename heuristic.
	if got := p.GetLoadMode(); got != register.LoadModeSyntax {
		t.Errorf("load mode = %q, want %q", got, register.LoadModeSyntax)
	}
}
