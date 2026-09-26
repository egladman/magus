// Package asciistrings reports a typographic glyph inside a string literal of
// a configured file. User-facing strings are plain ASCII; comments are exempt.
//
// The glyphs are named rather than caught by a blanket >127 check, because a
// blanket check would also flag the deliberate drawing glyphs (spinner frames,
// box borders) some files keep.
package asciistrings

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

var glyphs = map[rune]string{
	'—': "em dash",
	'–': "en dash",
	'‘': "left single quote",
	'’': "right single quote",
	'“': "left double quote",
	'”': "right double quote",
	'→': "right arrow",
	'↔': "left-right arrow",
	'…': "ellipsis",
	'≥': "greater-or-equal sign",
	'≤': "less-or-equal sign",
	'×': "multiplication sign",
	'·': "middle dot",
}

const message = "string literal carries %s: user-facing strings are plain ASCII; " +
	"write -, ', \", ->, <->, ..., >=, <=, x or . instead"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [Options.Files] is relative to.
	Module string `json:"module"`

	// Files are the sources held to the rule.
	Files source.Globs `json:"files"`
}

// New returns the analyzer configured by opts, erroring on a malformed glob.
func New(opts Options) (*analysis.Analyzer, error) {
	if err := opts.Files.Validate("asciistrings"); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "asciistrings",
		Doc:  "report typographic glyphs in user-facing string literals",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		if rel, ok := source.Rel(pass, opts.Module, f); !ok || !opts.Files.Match(rel) {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			var found []string
			for _, r := range lit.Value {
				if name, bad := glyphs[r]; bad {
					found = append(found, name)
				}
			}
			if len(found) > 0 {
				pass.Reportf(lit.Pos(), message, strings.Join(found, ", "))
			}
			return true
		})
	}
	return nil
}
