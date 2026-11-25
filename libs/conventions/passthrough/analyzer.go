package passthrough

import (
	"errors"
	"go/ast"

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

	// MaxSites is the most use sites a reported function has.
	MaxSites int `json:"max-sites"`

	// MaxRepeatedText is the most runes of string literal the body of a
	// function with two sites or more may hold and still be reported. Inlining
	// it writes the text at each site, and two copies of a message drift.
	MaxRepeatedText int `json:"max-repeated-text"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts. It reports a private function
// whose body is one call, with at most MaxSites use sites, once a change
// touches it. A callback keeps its name. Its exits are the passthrough key of
// each project's analyzers.yaml. New errors on no base or a max-sites below 1.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.Base == "" {
		return nil, errors.New("passthrough: no base revision to measure a change against")
	}
	return newWith(opts, gitdiff.New(opts.Base))
}

func newWith(opts Options, changes thin.Changes) (*analysis.Analyzer, error) {
	if opts.MaxSites < 1 {
		return nil, errors.New("passthrough: max-sites is required and must be at least 1")
	}
	rule := thin.Rule{
		Keep: keep, MaxSites: opts.MaxSites, MaxRepeatedText: opts.MaxRepeatedText, ValueUseExempt: true,
		Changes: changes, Ledgers: deviation.NewLedgers("passthrough", opts.Module), Hint: opts.Hint,
	}
	return &analysis.Analyzer{
		Name: "passthrough",
		Doc:  "report a private function that a change touches, whose body is one call and that has few use sites",
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

func keep(_ *analysis.Pass, h *thin.Helper) (string, bool) {
	if h.Guarded {
		return "", false
	}
	if _, ok := ast.Unparen(h.Expr).(*ast.CallExpr); !ok {
		return "", false
	}
	if h.Returns {
		return "returns one call", true
	}
	return "makes one call", true
}
