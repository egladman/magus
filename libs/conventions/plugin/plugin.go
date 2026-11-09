// Package plugin adapts the conventions analyzers to golangci-lint's module
// plugin system, one plugin per analyzer so each has its own settings block,
// enable entry and //nolint name.
//
// Nothing calls into this package: `golangci-lint custom` blank-imports it, and
// the init below is the whole contract.
package plugin

import (
	"fmt"
	"maps"
	"reflect"
	"strings"

	"github.com/egladman/magus/libs/conventions/asciistrings"
	"github.com/egladman/magus/libs/conventions/benchbeside"
	"github.com/egladman/magus/libs/conventions/blankerror"
	"github.com/egladman/magus/libs/conventions/diagmsg"
	"github.com/egladman/magus/libs/conventions/errmsg"
	"github.com/egladman/magus/libs/conventions/fieldwise"
	"github.com/egladman/magus/libs/conventions/filenames"
	"github.com/egladman/magus/libs/conventions/globalrestore"
	"github.com/egladman/magus/libs/conventions/hostagnostic"
	"github.com/egladman/magus/libs/conventions/hostvocab"
	"github.com/egladman/magus/libs/conventions/internal/source"
	"github.com/egladman/magus/libs/conventions/jsonlstate"
	"github.com/egladman/magus/libs/conventions/nameoutput"
	"github.com/egladman/magus/libs/conventions/namewords"
	"github.com/egladman/magus/libs/conventions/providerio"
	"github.com/egladman/magus/libs/conventions/ruletext"
	"github.com/egladman/magus/libs/conventions/secretfields"
	"github.com/egladman/magus/libs/conventions/stderrprint"
	"github.com/egladman/magus/libs/conventions/stringjoin"
	"github.com/egladman/magus/libs/conventions/stutter"
	"github.com/egladman/magus/libs/conventions/testisolation"
	"github.com/egladman/magus/libs/conventions/testloops"
	"github.com/golangci/plugin-module-register/register"
	"golang.org/x/tools/go/analysis"
)

func init() { //nolint:gochecknoinits // golangci-lint's plugin-module-register API registers only from init
	register.Plugin("asciistrings", plugin("asciistrings", asciistrings.New))
	register.Plugin("benchbeside", plugin("benchbeside", benchbeside.New))
	register.Plugin("blankerror", plugin("blankerror", blankerror.New))
	register.Plugin("diagmsg", plugin("diagmsg", diagmsg.New))
	register.Plugin("errmsg", plugin("errmsg", errmsg.New))
	register.Plugin("fieldwise", plugin("fieldwise", fieldwise.New))
	register.Plugin("filenames", plugin("filenames", filenames.New))
	register.Plugin("globalrestore", plugin("globalrestore", globalrestore.New))
	register.Plugin("hostagnostic", plugin("hostagnostic", hostagnostic.New))
	register.Plugin("hostvocab", plugin("hostvocab", hostvocab.New))
	register.Plugin("jsonlstate", plugin("jsonlstate", jsonlstate.New))
	register.Plugin("nameoutput", plugin("nameoutput", nameoutput.New))
	register.Plugin("namewords", plugin("namewords", namewords.New))
	register.Plugin("providerio", plugin("providerio", providerio.New))
	register.Plugin("ruletext", plugin("ruletext", ruletext.New))
	register.Plugin("secretfields", plugin("secretfields", secretfields.New))
	register.Plugin("stderrprint", plugin("stderrprint", stderrprint.New))
	register.Plugin("stringjoin", plugin("stringjoin", stringjoin.New))
	register.Plugin("stutter", plugin("stutter", stutter.New))
	register.Plugin("testisolation", plugin("testisolation", testisolation.New))
	register.Plugin("testloops", plugin("testloops", testloops.New))
}

// plugin builds a constructor that decodes a settings block straight into the
// analyzer's own Options, so an option added there cannot be dropped on the
// way in, and an unknown key is a load error naming the linter.
//
// An analyzer with a module option reads the module from the linted tree's
// go.mod unless the settings name one, and a tree with no go.mod is an error.
func plugin[O any](name string, build func(O) (*analysis.Analyzer, error)) register.NewPlugin {
	return func(raw any) (register.LinterPlugin, error) {
		settings, err := withTreeModule[O](name, raw)
		if err != nil {
			return nil, err
		}
		opts, err := register.DecodeSettings[O](settings)
		if err != nil {
			return nil, fmt.Errorf("%s: settings: %w", name, err)
		}
		analyzer, err := build(opts)
		if err != nil {
			return nil, err
		}
		return &linter{analyzer: analyzer}, nil
	}
}

// withTreeModule fills the module option from the linted tree's go.mod unless
// the settings already name one.
func withTreeModule[O any](name string, raw any) (any, error) {
	if !hasModuleOption[O]() {
		return raw, nil
	}
	settings, isMap := raw.(map[string]any)
	if raw == nil {
		settings, isMap = map[string]any{}, true
	}
	if !isMap {
		return raw, nil
	}
	if _, set := settings["module"]; set {
		return raw, nil
	}
	tree, err := source.TreeModule(name)
	if err != nil {
		return nil, err
	}
	merged := maps.Clone(settings)
	merged["module"] = tree
	return merged, nil
}

func hasModuleOption[O any]() bool {
	t := reflect.TypeFor[O]()
	if t.Kind() != reflect.Struct {
		return false
	}
	for f := range t.Fields() {
		if name, _, _ := strings.Cut(f.Tag.Get("json"), ","); name == "module" {
			return true
		}
	}
	return false
}

type linter struct {
	analyzer *analysis.Analyzer
}

// BuildAnalyzers returns the one analyzer the settings configured.
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
