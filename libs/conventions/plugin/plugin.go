// Package plugin adapts the conventions analyzers to golangci-lint's module
// plugin system, one plugin per analyzer so each has its own settings block,
// enable entry and //nolint name.
//
// Nothing calls into this package: `golangci-lint custom` blank-imports it, and
// the init below is the whole contract.
package plugin

import (
	"fmt"

	"github.com/egladman/magus/libs/conventions/asciistrings"
	"github.com/egladman/magus/libs/conventions/hostagnostic"
	"github.com/egladman/magus/libs/conventions/hostvocab"
	"github.com/egladman/magus/libs/conventions/importceiling"
	"github.com/egladman/magus/libs/conventions/nameoutput"
	"github.com/egladman/magus/libs/conventions/ruletext"
	"github.com/egladman/magus/libs/conventions/stutter"
	"github.com/egladman/magus/libs/conventions/testisolation"
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() {
	register.Plugin("asciistrings", plugin(asciistrings.New))
	register.Plugin("hostagnostic", plugin(hostagnostic.New))
	register.Plugin("hostvocab", plugin(hostvocab.New))
	register.Plugin("importceiling", plugin(importceiling.New))
	register.Plugin("nameoutput", plugin(nameoutput.New))
	register.Plugin("ruletext", plugin(ruletext.New))
	register.Plugin("stutter", plugin(stutter.New))
	register.Plugin("testisolation", plugin(testisolation.New))
}

// plugin builds a constructor that decodes a settings block straight into the
// analyzer's own Options, so an option added there cannot be dropped on the
// way in, and an unknown key is a load error naming it.
func plugin[O any](build func(O) (*analysis.Analyzer, error)) register.NewPlugin {
	return func(raw any) (register.LinterPlugin, error) {
		opts, err := register.DecodeSettings[O](raw)
		if err != nil {
			return nil, fmt.Errorf("settings: %w", err)
		}
		analyzer, err := build(opts)
		if err != nil {
			return nil, err
		}
		return &linter{analyzer: analyzer}, nil
	}
}

type linter struct {
	analyzer *analysis.Analyzer
}

func (l *linter) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	return []*analysis.Analyzer{l.analyzer}, nil
}

// GetLoadMode asks for types for every analyzer: under syntax loading
// golangci-lint leaves pass.Pkg nil, and each one reads the package's import
// path. testisolation's facts need types regardless, and the run loads them
// for the stock linters anyway.
func (l *linter) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
