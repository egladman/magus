package inlineliteral

import (
	"errors"
	"go/ast"
	"go/types"

	"github.com/egladman/magus/libs/conventions/internal/deviation"
	"github.com/egladman/magus/libs/conventions/internal/gitdiff"
	"github.com/egladman/magus/libs/conventions/internal/thin"
	"golang.org/x/tools/go/analysis"
)

// Options configures the analyzer returned by [New].
type Options struct {
	// Module limits the check to packages inside it, and makes each file answer
	// to the analyzers.yaml of the nearest module at or above it. Empty reads
	// the one analyzers.yaml in the working directory.
	Module string `json:"module"`

	// Base is the revision a change is measured against, such as origin/main.
	// The comparison point is its merge base with HEAD.
	Base string `json:"base"`

	// MaxSites is the most use sites a reported function has. A function
	// used more often than this has earned its name.
	MaxSites int `json:"max-sites"`

	// MaxRepeatedText is the most runes of string literal the body of a
	// function with two sites or more may hold and still be reported. Inlining
	// it writes the text at each site, and two copies of a message drift.
	MaxRepeatedText int `json:"max-repeated-text"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts. It reports a private function
// returning one literal value, with at most MaxSites use sites, once a change
// touches it. Its exits are the inlineliteral key of each project's
// analyzers.yaml. New errors on no base or a max-sites below 1.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Base == "" {
		return nil, errors.New("inlineliteral: no base revision to measure a change against")
	}
	return newWith(opts, gitdiff.New(opts.Base))
}

func newWith(opts Options, changes thin.Changes) (*analysis.Analyzer, error) {
	if opts.MaxSites < 1 {
		return nil, errors.New("inlineliteral: max-sites is required and must be at least 1")
	}
	rule := thin.Rule{
		Keep: keep, MaxSites: opts.MaxSites, MaxRepeatedText: opts.MaxRepeatedText,
		Changes: changes, Ledgers: deviation.NewLedgers("inlineliteral", opts.Module), Hint: opts.Hint,
	}
	return &analysis.Analyzer{
		Name: "inlineliteral",
		Doc:  "report a private function that a change touches, that returns one literal value and has few use sites",
		Run: func(pass *analysis.Pass) (any, error) {
			departures, err := thin.Judge(pass, rule)
			if err != nil {
				return nil, err
			}
			for _, d := range departures {
				thin.Report(pass, d, opts.Hint)
			}
			return nil, nil
		},
	}, nil
}

func keep(pass *analysis.Pass, h *thin.Helper) (string, bool) {
	if !h.Returns {
		return "", false
	}
	e := ast.Unparen(h.Expr)
	if u, ok := e.(*ast.UnaryExpr); ok {
		if _, lit := ast.Unparen(u.X).(*ast.CompositeLit); lit {
			return "returns one composite literal", true
		}
	}
	if h.Guarded {
		return "", false
	}
	switch e := e.(type) {
	case *ast.CompositeLit:
		return "returns one composite literal", true
	case *ast.BasicLit:
		return "returns one literal", true
	case *ast.Ident:
		if _, isConst := pass.TypesInfo.Uses[e].(*types.Const); isConst || e.Name == "nil" {
			return "returns one constant", true
		}
		return "returns one name", true
	case *ast.SelectorExpr:
		if fieldRead(pass, e) {
			return "returns one field", true
		}
	}
	return "", false
}

// fieldRead reports whether e reads a field or a package-level value through
// names alone, with no call or method value on the way.
func fieldRead(pass *analysis.Pass, e *ast.SelectorExpr) bool {
	switch x := ast.Unparen(e.X).(type) {
	case *ast.Ident:
	case *ast.SelectorExpr:
		if !fieldRead(pass, x) {
			return false
		}
	default:
		return false
	}
	if sel, ok := pass.TypesInfo.Selections[e]; ok {
		return sel.Kind() == types.FieldVal
	}
	switch pass.TypesInfo.Uses[e.Sel].(type) {
	case *types.Var, *types.Const:
		return true
	}
	return false
}
