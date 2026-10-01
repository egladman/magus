// Package plugin adapts testlayout to golangci-lint's module plugin system.
//
// Separate from the analyzers so that testlayout itself carries no golangci-lint
// dependency and stays usable under go vet or singlechecker. Nothing calls into
// this package: `golangci-lint custom` blank-imports it, and the init below is
// the whole contract.
//
// It registers two linters. testpair is a plugin of its own, not a second analyzer
// inside testlayout, because golangci-lint reports every analyzer a plugin builds
// under the plugin's name, and a //nolint:testlayout would then silence pairing too.
package plugin

import (
	"fmt"

	"github.com/egladman/magus/libs/testlayout"
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("testlayout", newPlugin)
	register.Plugin("testpair", newPairingPlugin)
}

// newPlugin builds the plugin from its linters.settings.custom.testlayout.settings
// block. It decodes into [testlayout.Options] directly rather than into a copy
// declared here: a parallel struct plus a field-by-field transpose is a place
// where adding an option compiles clean while silently ignoring the user's yaml.
func newPlugin(raw any) (register.LinterPlugin, error) {
	opts, err := register.DecodeSettings[testlayout.Options](raw)
	if err != nil {
		return nil, fmt.Errorf("testlayout: settings: %w", err)
	}

	return &linter{analyzer: testlayout.New(opts)}, nil
}

// newPairingPlugin builds testpair, which takes no settings. Decoding into an
// empty struct is what refuses one: DecodeSettings rejects unknown keys, so an
// allow list or a marker switch in the yaml fails the config load instead of
// becoming an exemption.
func newPairingPlugin(raw any) (register.LinterPlugin, error) {
	if _, err := register.DecodeSettings[struct{}](raw); err != nil {
		return nil, fmt.Errorf("testpair: takes no settings: %w", err)
	}

	return &linter{analyzer: testlayout.Pairing}, nil
}

type linter struct {
	analyzer *analysis.Analyzer
}

// BuildAnalyzers returns the one analyzer the plugin wraps.
func (l *linter) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{l.analyzer}, nil
}

// GetLoadMode reports that syntax is enough: the analyzers read file names and
// package clauses and never consult type information.
func (l *linter) GetLoadMode() string {
	return register.LoadModeSyntax
}
