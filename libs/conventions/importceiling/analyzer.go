// Package importceiling holds a package's count of distinct imports under a
// prefix at or below a ceiling. It is a ratchet: the count may fall, and
// raising the ceiling is a deliberate config edit with a reason.
package importceiling

import (
	"errors"
	"fmt"
	"go/ast"
	"slices"
	"strconv"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "%s imports %d packages under %s, over its ceiling of %d: put the new dependency behind " +
	"another package, or raise the ceiling in importceiling's settings deliberately and say why"

// Rule caps one package.
type Rule struct {
	// Package is the import path the rule holds.
	Package string `json:"package"`

	// Prefix selects which of its imports count.
	Prefix string `json:"prefix"`

	// Max is the most distinct counted imports its non-test files may carry.
	Max int `json:"max"`
}

// Options configures the analyzer returned by [New].
type Options struct {
	Rules []Rule `json:"rules"`
}

// New returns the analyzer configured by opts, erroring on a rule missing its
// package or prefix.
func New(opts Options) (*analysis.Analyzer, error) {
	for _, r := range opts.Rules {
		if r.Package == "" || r.Prefix == "" {
			return nil, errors.New("importceiling: every rule needs a package and a prefix")
		}
	}
	return &analysis.Analyzer{
		Name: "importceiling",
		Doc:  "hold a package's imports under a prefix at or below a ceiling",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	i := slices.IndexFunc(opts.Rules, func(r Rule) bool { return r.Package == pass.Pkg.Path() })
	if i < 0 {
		return nil
	}
	rule := opts.Rules[i]
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	var first *ast.File
	seen := map[string]bool{}
	for _, f := range files {
		if source.IsTest(pass, f) {
			continue
		}
		if first == nil || source.Name(pass, f) < source.Name(pass, first) {
			first = f
		}
		for _, imp := range f.Imports {
			if p, err := strconv.Unquote(imp.Path.Value); err == nil && strings.HasPrefix(p, rule.Prefix) {
				seen[p] = true
			}
		}
	}
	if len(seen) > rule.Max {
		pass.Report(analysis.Diagnostic{
			Pos:     first.Name.Pos(),
			Message: fmt.Sprintf(message, rule.Package, len(seen), rule.Prefix, rule.Max),
		})
	}
	return nil
}
