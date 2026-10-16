// Package testloops keeps a test from starting a shell loop that outlives it.
//
// A test that puts a child in its own process group relies on its cleanup to
// kill the group. A go test that is cancelled or killed runs no cleanup, and a
// `while :` loop in that group keeps forking with nothing to end it. A test file
// that uses one of the configured markers must therefore tie every endless shell
// loop in its string literals to a liveness check such as `kill -0 "$TEST_PID"`.
package testloops

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/libs/conventions/internal/source"
	"golang.org/x/tools/go/analysis"
)

const message = "shell loop %q never ends, and this test puts its child in a process group (%s): " +
	"a cancelled go test runs no cleanup, so the loop outlives it and forks forever; " +
	"make the loop condition check that the test is alive, as in while kill -0 \"$TEST_PID\""

// Options configures the analyzer returned by [New].
type Options struct {
	// Markers are the identifiers that put a child in its own process group,
	// such as the Setpgid field of syscall.SysProcAttr or a helper wrapping it.
	// A test file that uses none of them is not checked.
	Markers []string `json:"markers"`

	// Hint is appended to every diagnostic: the repository's own remedy.
	Hint string `json:"hint"`
}

// New returns the analyzer configured by opts, erroring on no markers: with
// none the rule would check nothing and say so to nobody.
func New(opts Options) (*analysis.Analyzer, error) {
	if len(opts.Markers) == 0 {
		return nil, errors.New("testloops: markers is required")
	}
	return &analysis.Analyzer{
		Name: "testloops",
		Doc:  "require a test's endless shell loop in its own process group to check that the test is still alive",
		Run:  func(pass *analysis.Pass) (any, error) { return nil, run(pass, opts) },
	}, nil
}

// endless matches the header of a loop that never ends by itself: a condition
// that is always true, closed by a semicolon, a newline or the loop's do.
var endless = regexp.MustCompile(`\b(?:while\s+(?::|true)|until\s+false)(?:\s*;|\s*\n|\s+do\b)|\bfor\s*\(\(\s*;\s*;\s*\)\)`)

// liveness is a check that fails once the test process is gone.
var liveness = regexp.MustCompile(`\bkill\s+-0\b`)

var doOrDone = regexp.MustCompile(`\b(?:do|done)\b`)

func run(pass *analysis.Pass, opts Options) error {
	for _, f := range pass.Files {
		if !source.IsTest(pass, f) {
			continue
		}
		marker := firstMarker(f, opts.Markers)
		if marker == "" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			// Only a literal or a concatenation of them: a name for a constant is
			// reported once, where the constant is declared.
			e, ok := n.(ast.Expr)
			if !ok || !isLiteral(e) {
				return true
			}
			tv := pass.TypesInfo.Types[e]
			if tv.Value == nil || tv.Value.Kind() != constant.String {
				return true
			}
			for _, header := range untied(constant.StringVal(tv.Value)) {
				pass.Reportf(e.Pos(), "%s", source.Hint(fmt.Sprintf(message, header, marker), opts.Hint))
			}
			return false
		})
	}
	return nil
}

func isLiteral(e ast.Expr) bool {
	switch e.(type) {
	case *ast.BasicLit, *ast.BinaryExpr:
		return true
	}
	return false
}

// firstMarker is the first configured identifier f mentions, or "".
func firstMarker(f *ast.File, markers []string) string {
	found := ""
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && found == "" && slices.Contains(markers, id.Name) {
			found = id.Name
		}
		return found == ""
	})
	return found
}

// untied returns the header of each endless loop in script whose text, through
// its matching done, holds no liveness check. A loop with no done runs to the
// end of the script.
func untied(script string) []string {
	var out []string
	for _, m := range endless.FindAllStringIndex(script, -1) {
		end, depth := len(script), 0
		if strings.HasSuffix(script[m[0]:m[1]], "do") {
			depth = 1
		}
		for _, w := range doOrDone.FindAllStringIndex(script[m[1]:], -1) {
			if script[m[1]+w[0]:m[1]+w[1]] == "do" {
				depth++
			} else if depth--; depth <= 0 {
				end = m[1] + w[1]
				break
			}
		}
		if !liveness.MatchString(script[m[0]:end]) {
			header := strings.TrimRight(script[m[0]:m[1]], " ;\n")
			out = append(out, strings.TrimRight(strings.TrimSuffix(header, "do"), " ;\n"))
		}
	}
	return out
}
