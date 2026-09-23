// Package plugin adapts coldread to golangci-lint's module plugin system.
//
// Separate from the analyzer so that coldread itself carries no golangci-lint
// dependency and stays usable under go vet or singlechecker. Nothing calls into
// this package: `golangci-lint custom` blank-imports it, and the init below is
// the whole contract.
package plugin

import (
	"fmt"

	"github.com/egladman/magus/libs/coldread"
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("coldread", newPlugin)
}

// newPlugin builds the plugin from its linters.settings.custom.coldread.settings
// block. It decodes into [coldread.Options] directly rather than into a copy
// declared here: a parallel struct plus a field-by-field transpose is a place
// where adding an option compiles clean while silently ignoring the user's yaml.
func newPlugin(raw any) (register.LinterPlugin, error) {
	opts, err := register.DecodeSettings[coldread.Options](raw)
	if err != nil {
		return nil, fmt.Errorf("coldread: settings: %w", err)
	}

	analyzer, err := coldread.New(opts)
	if err != nil {
		return nil, err
	}

	return &linter{analyzer: analyzer}, nil
}

type linter struct {
	analyzer *analysis.Analyzer
}

func (l *linter) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{l.analyzer}, nil
}

// GetLoadMode reports that syntax is enough: the analyzer reads comment text and
// never consults type information.
func (l *linter) GetLoadMode() string {
	return register.LoadModeSyntax
}
