package stringjoin

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const (
	sprintfMessage   = "string built from %d operands joined with +: format it with fmt.Sprintf"
	pathMessage      = "path built from %d operands joined with +: join it with filepath.Join"
	multilineMessage = "multi-line text built from %d operands joined with +: write it as a raw string or a text/template"
)

// Options configures the analyzer returned by [New].
type Options struct {
	// MinOperands is the fewest operands a + chain needs to be reported. Two
	// is a plain join, which reads fine.
	MinOperands int `json:"min-operands"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts, erroring when MinOperands is
// below 2: every + chain has at least two operands, so a lower floor would
// report each one.
func New(opts Options) (*analysis.Analyzer, error) {
	if opts.MinOperands < 2 {
		return nil, errors.New("stringjoin: min-operands is required and must be at least 2")
	}
	return &analysis.Analyzer{
		Name: "stringjoin",
		Doc:  "report a string built from a chain of + with a non-constant operand",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

func run(pass *analysis.Pass, opts Options) error {
	for _, f := range pass.Files {
		// Inspect visits a chain's root before its inner + nodes, so marking the
		// root's operands keeps each chain to one report.
		inner := map[ast.Expr]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			e, ok := n.(*ast.BinaryExpr)
			if !ok || !isStringAdd(pass, e) || inner[e] {
				return true
			}
			var operands []ast.Expr
			flatten(pass, e, inner, &operands)
			if len(operands) < opts.MinOperands || pass.TypesInfo.Types[e].Value != nil {
				return true
			}
			msg := fmt.Sprintf(message(pass, operands), len(operands))
			pass.Reportf(e.Pos(), "%s", source.Hint(msg, opts.Hint))
			return true
		})
	}
	return nil
}

func isStringAdd(pass *analysis.Pass, e *ast.BinaryExpr) bool {
	if e.Op != token.ADD {
		return false
	}
	b, ok := pass.TypesInfo.TypeOf(e).Underlying().(*types.Basic)
	return ok && b.Info()&types.IsString != 0
}

// flatten appends e's operands left to right, descending through the nested
// string + nodes of one chain and marking each as inner.
func flatten(pass *analysis.Pass, e ast.Expr, inner map[ast.Expr]bool, out *[]ast.Expr) {
	if b, ok := e.(*ast.BinaryExpr); ok && isStringAdd(pass, b) {
		inner[b] = true
		flatten(pass, b.X, inner, out)
		flatten(pass, b.Y, inner, out)
		return
	}
	*out = append(*out, e)
}

func message(pass *analysis.Pass, operands []ast.Expr) string {
	var literals []string
	for _, op := range operands {
		tv := pass.TypesInfo.Types[op]
		if tv.Value != nil && tv.Value.Kind() == constant.String {
			literals = append(literals, constant.StringVal(tv.Value))
		}
	}
	for _, s := range literals {
		if strings.Contains(s, "\n") {
			return multilineMessage
		}
	}
	if slices.ContainsFunc(literals, isPathPart) && !slices.ContainsFunc(literals, isURL) {
		return pathMessage
	}
	return sprintfMessage
}

// isPathPart reports whether s looks like a piece of a filesystem path: it
// starts or ends with a slash and holds no whitespace, which prose would.
func isPathPart(s string) bool {
	return (strings.HasPrefix(s, "/") || strings.HasSuffix(s, "/")) && !strings.ContainsAny(s, " \t")
}

// isURL reports whether s holds a scheme, whose slashes filepath.Join would
// collapse.
func isURL(s string) bool {
	return strings.Contains(s, "://")
}
