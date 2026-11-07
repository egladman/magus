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
	"github.com/egladman/magus/libs/conventions/diagmsg"
	"github.com/egladman/magus/libs/conventions/errmsg"
	"github.com/egladman/magus/libs/conventions/fieldwise"
	"github.com/egladman/magus/libs/conventions/filenames"
	"github.com/egladman/magus/libs/conventions/globalrestore"
	"github.com/egladman/magus/libs/conventions/hostagnostic"
	"github.com/egladman/magus/libs/conventions/hostvocab"
	"github.com/egladman/magus/libs/conventions/internal/source"
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
	register.Plugin("diagmsg", plugin("diagmsg", diagmsg.New))
	register.Plugin("errmsg", plugin("errmsg", errmsg.New))
	register.Plugin("fieldwise", plugin("fieldwise", fieldwise.New))
	register.Plugin("filenames", plugin("filenames", filenames.New))
	register.Plugin("globalrestore", plugin("globalrestore", globalrestore.New))
	register.Plugin("hostagnostic", plugin("hostagnostic", hostagnostic.New))
	register.Plugin("hostvocab", plugin("hostvocab", hostvocab.New))
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

// inModuleKey holds settings that name paths or packages of one module, listed
// by that module's path. A block applies when the linted tree is that module or
// nested in it; elsewhere it is ignored, so a rule naming cmd/magus/shell.go
// cannot fail a tree that has no such file.
const inModuleKey = "in-module"

// plugin builds a constructor that decodes a settings block straight into the
// analyzer's own Options, so an option added there cannot be dropped on the
// way in, and an unknown key is a load error naming the linter.
//
// An analyzer with a module option reads the module from the linted tree's
// go.mod unless the settings name one, and a tree with no go.mod is an error.
// A linter whose settings are only an in-module block is inert in any other
// module.
func plugin[O any](name string, build func(O) (*analysis.Analyzer, error)) register.NewPlugin {
	return func(raw any) (register.LinterPlugin, error) {
		settings, applies, err := scope[O](name, raw)
		if err != nil {
			return nil, err
		}
		if !applies {
			return &linter{}, nil
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

// scope resolves raw against the linted tree: it merges the in-module block
// that owns the tree and defaults the module option. It reports false when the
// settings are only in-module blocks and none owns the tree.
func scope[O any](name string, raw any) (settings any, applies bool, err error) {
	takesModule := hasModuleOption[O]()
	base, isMap := raw.(map[string]any)
	if raw == nil {
		base, isMap = map[string]any{}, true
	}
	scoped, hasScoped := base[inModuleKey]
	if !isMap || (!takesModule && !hasScoped) {
		return raw, true, nil
	}
	tree, err := source.TreeModule(name)
	if err != nil {
		return nil, false, err
	}
	merged := maps.Clone(base)
	delete(merged, inModuleKey)
	module := tree
	if hasScoped {
		list, ok := scoped.([]any)
		if !ok {
			return nil, false, fmt.Errorf("%s: settings: %s lists blocks, each naming its module", name, inModuleKey)
		}
		blocks, err := moduleBlocks(name, list)
		if err != nil {
			return nil, false, err
		}
		if owner, found := owningModule(tree, blocks); found {
			maps.Copy(merged, blocks[owner])
			module = owner
		} else if len(merged) == 0 {
			return nil, false, nil
		}
	}
	if _, set := merged["module"]; takesModule && !set {
		merged["module"] = module
	}
	return merged, true, nil
}

// moduleBlocks keys the in-module list by module path. A list rather than a
// map, because golangci-lint's viper loader splits map keys at their dots.
func moduleBlocks(name string, list []any) (map[string]map[string]any, error) {
	blocks := map[string]map[string]any{}
	for i, item := range list {
		block, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: settings: %s entry %d is not a block", name, inModuleKey, i)
		}
		module, _ := block["module"].(string)
		if module == "" {
			return nil, fmt.Errorf("%s: settings: %s entry %d names no module", name, inModuleKey, i)
		}
		if _, dup := blocks[module]; dup {
			return nil, fmt.Errorf("%s: settings: %s names module %q twice", name, inModuleKey, module)
		}
		settings := maps.Clone(block)
		delete(settings, "module")
		blocks[module] = settings
	}
	return blocks, nil
}

// owningModule returns the longest key that is tree or a module path tree is
// nested under, which is how a lib module's run reaches its enclosing
// module's rules.
func owningModule(tree string, blocks map[string]map[string]any) (string, bool) {
	owner := ""
	for key := range blocks {
		if (key == tree || strings.HasPrefix(tree, key+"/")) && len(key) > len(owner) {
			owner = key
		}
	}
	return owner, owner != ""
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

// BuildAnalyzers returns the one analyzer the settings configured, or none
// when the linter is inert in this module.
func (l *linter) BuildAnalyzers() ([]*analysis.Analyzer, error) {
	if l.analyzer == nil {
		return nil, nil
	}
	return []*analysis.Analyzer{l.analyzer}, nil
}

// GetLoadMode asks for types for every analyzer: under syntax loading
// golangci-lint leaves pass.Pkg nil, and each one reads the package's import
// path. testisolation's facts need types regardless, and the run loads them
// for the stock linters anyway.
func (l *linter) GetLoadMode() string {
	return register.LoadModeTypesInfo
}
