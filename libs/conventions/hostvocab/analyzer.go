// Package hostvocab reports a string literal in guard code that spells an agent
// host's name for one of its tools.
//
// A switch or lookup table over "Read" or "Bash" is a per-host branch with the
// host's name filed off: no host name appears in it, so hostagnostic passes it,
// and the next time a host renames a tool it costs a magus release. The literal
// is the whole signal; comments that quote the words are not reported.
package hostvocab

import (
	"errors"
	"go/ast"
	"go/token"
	"slices"
	"strconv"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "guard code spells the host tool name %q: record magus's own label " +
	"(hookToolCommand, hookToolWrite, hookToolRead) and let the wrapper in the reader's config map to it " +
	"by which flag it passes; if the literal must stay, add //nolint:hostvocab naming where that decision is written down"

// Options configures the analyzer returned by [New].
type Options struct {
	// Module is the import path [Options.Files] is relative to.
	Module string `json:"module"`

	// Files are the guard's own sources, the only code that sees a host's hook
	// payload. Elsewhere "Read" and "Task" are ordinary words.
	Files source.Globs `json:"files"`

	// Words are what agent hosts call their tools.
	Words []string `json:"words"`
}

// New returns the analyzer configured by opts, erroring on a malformed glob or
// an empty word list.
func New(opts Options) (*analysis.Analyzer, error) {
	if err := opts.Files.Validate("hostvocab"); err != nil {
		return nil, err
	}
	if len(opts.Words) == 0 {
		return nil, errors.New("hostvocab: words is empty, so nothing would be reported")
	}
	if err := source.InModule("hostvocab", opts.Module, func(root string) error {
		return opts.Files.Check("hostvocab", "files", root)
	}); err != nil {
		return nil, err
	}
	return &analysis.Analyzer{
		Name: "hostvocab",
		Doc:  "report a host's tool name spelled as a string literal in guard code",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	files, err := source.Files(pass)
	if err != nil {
		return err
	}
	for _, f := range files {
		// A test names a host's tool to describe a real payload, not to judge one.
		if source.IsTest(pass, f) {
			continue
		}
		if rel, ok := source.Rel(pass, opts.Module, f); !ok || !opts.Files.Match(rel) {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && slices.Contains(opts.Words, v) {
				pass.Reportf(lit.Pos(), message, v)
			}
			return true
		})
	}
	return nil
}
