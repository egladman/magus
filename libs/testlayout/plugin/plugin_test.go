package plugin

import (
	"strings"
	"testing"

	"github.com/golangci/plugin-module-register/register"
)

// TestNewPluginDecodesSettings drives the path production actually runs: a yaml
// settings block arrives as map[string]any and has to reach [testlayout.Options].
// The transpose this replaced was a hand-written field copy, where a missed field
// compiled clean and silently ignored what the user configured, so the point of
// this test is that the json tags, not a copy, are what carry the values across.
func TestNewPluginDecodesSettings(t *testing.T) {
	p, err := newPlugin(map[string]any{
		"report-unix-suffix": true,
		"report-main-tests":  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	assertOneAnalyzer(t, p, "testlayout")
}

// TestNewPluginRejectsUnknownKey pins register.DecodeSettings's DisallowUnknownFields
// behaviour: a misspelled settings key is a silent no-op in most linters, and here
// it is a load error naming the linter and the key. The removed pairing options
// are unknown keys now, so a config still carrying one fails to load.
func TestNewPluginRejectsUnknownKey(t *testing.T) {
	for _, key := range []string{"ignore-marker", "allow", "report-unpaired", "honor-marker", "pair-benchmarks"} {
		_, err := newPlugin(map[string]any{key: true})
		if err == nil || !strings.HasPrefix(err.Error(), "decode linters.settings.custom.testlayout.settings: ") {
			t.Errorf("want %q to fail naming the linter, got %v", key, err)
		}
	}
}

func TestNewPluginEmptySettings(t *testing.T) {
	if _, err := newPlugin(nil); err != nil {
		t.Fatalf("a plugin with no settings block must build: %v", err)
	}
}

// TestNewPairingPlugin checks testpair builds with no settings block, and under
// its own name, which is what keeps a //nolint:testlayout from reaching it.
func TestNewPairingPlugin(t *testing.T) {
	for _, raw := range []any{nil, map[string]any{}} {
		p, err := newPairingPlugin(raw)
		if err != nil {
			t.Fatalf("testpair with settings %v must build: %v", raw, err)
		}

		assertOneAnalyzer(t, p, "testpair")
	}
}

// TestNewPairingPluginRefusesSettings pins that testpair has nothing to configure:
// any key, an exemption list above all, fails the load rather than taking effect.
func TestNewPairingPluginRefusesSettings(t *testing.T) {
	for _, key := range []string{"allow", "honor-marker", "exclude"} {
		_, err := newPairingPlugin(map[string]any{key: []any{"conventions_test.go"}})
		if err == nil || !strings.HasPrefix(err.Error(), "testpair: takes no settings: ") {
			t.Errorf("want %q refused, got %v", key, err)
		}
	}
}

func TestGetLoadMode(t *testing.T) {
	p, err := newPlugin(nil)
	if err != nil {
		t.Fatal(err)
	}

	// Syntax, not TypesInfo: the analyzers read names and package clauses and never
	// consult type information, and asking for types would make every consumer pay
	// for a full type-check to run a filename rule.
	if got := p.GetLoadMode(); got != register.LoadModeSyntax {
		t.Errorf("load mode = %q, want %q", got, register.LoadModeSyntax)
	}
}

// assertOneAnalyzer checks p builds exactly one analyzer named name. The name is the
// golangci-lint config key: linters.settings.custom.<name> and the entry in
// linters.enable both have to match what register.Plugin was given, and a drift
// between them fails at config load rather than at compile time.
func assertOneAnalyzer(t *testing.T, p register.LinterPlugin, name string) {
	t.Helper()

	analyzers, err := p.BuildAnalyzers()
	if err != nil {
		t.Fatal(err)
	}

	if len(analyzers) != 1 || analyzers[0].Name != name {
		t.Fatalf("want one analyzer named %q, got %d", name, len(analyzers))
	}
}
