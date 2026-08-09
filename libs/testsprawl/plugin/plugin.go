// Package plugin adapts testsprawl to golangci-lint's module plugin system.
//
// Separate from the analyzer so that testsprawl itself carries no golangci-lint
// dependency and stays usable under go vet or singlechecker. Nothing calls into
// this package: `golangci-lint custom` blank-imports it, and the init below is
// the whole contract.
package plugin

import (
	"github.com/egladman/magus/libs/testsprawl"
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("testsprawl", newPlugin)
}

// newPlugin builds the plugin from its linters.settings.custom.testsprawl.settings
// block. It decodes into [testsprawl.Options] directly rather than into a copy
// declared here: a parallel struct plus a field-by-field transpose is a place
// where adding an option compiles clean while silently ignoring the user's yaml.
func newPlugin(raw any) (register.LinterPlugin, error) {
	opts, err := register.DecodeSettings[testsprawl.Options](raw)
	if err != nil {
		return nil, err
	}

	analyzer, err := testsprawl.New(opts)
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

// GetLoadMode reports that syntax is enough: the analyzer matches file names and
// never consults type information.
func (l *linter) GetLoadMode() string {
	return register.LoadModeSyntax
}
