package plugin

import (
	"testing"

	"github.com/golangci/plugin-module-register/register"
)

// TestNewPluginDecodesSettings drives the path production actually runs: a yaml
// settings block arrives as map[string]any and has to reach [commentdash.Options].
// The json tags, not a hand-written field copy, are what carry the values across,
// and a copy is where a new option compiles clean while ignoring the user's yaml.
func TestNewPluginDecodesSettings(t *testing.T) {
	p, err := newPlugin(map[string]any{
		"allow":   []any{"*_gen.go"},
		"wrapped": true,
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
	if analyzers[0].Name != "commentdash" {
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
	if _, err := newPlugin(map[string]any{"allowed": []any{"*_gen.go"}}); err == nil {
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

	// Syntax, not TypesInfo: the analyzer reads comment text and never consults
	// type information, and asking for types would make every consumer pay for a
	// full type-check to scan prose.
	if got := p.GetLoadMode(); got != register.LoadModeSyntax {
		t.Errorf("load mode = %q, want %q", got, register.LoadModeSyntax)
	}
}
