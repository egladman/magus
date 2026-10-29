package benchtasks

import (
	"bufio"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/egladman/magus/internal/json"
)

// testFuncs maps the `go test -v` name of every test in one directory's test files to its
// source text: TestX for a top-level test, Runner/TestM for a testify suite method, where
// Runner is the top-level test that passes the suite to suite.Run. A suite method no
// runner reaches is not a test and is left out.
func testFuncs(files map[string][]byte) (map[string]string, error) {
	fset := token.NewFileSet()
	tops := map[string]string{}
	methods := map[string]map[string]string{}
	runners := map[string][]string{}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		src := files[name]
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Body == nil {
				continue
			}
			text := string(src[fset.Position(fn.Pos()).Offset:fset.Position(fn.End()).Offset])
			if fn.Recv == nil {
				tops[fn.Name.Name] = text
				for _, s := range suitesRun(fn.Body) {
					runners[s] = append(runners[s], fn.Name.Name)
				}
				continue
			}
			recv := receiverType(fn.Recv)
			if methods[recv] == nil {
				methods[recv] = map[string]string{}
			}
			methods[recv][fn.Name.Name] = text
		}
	}
	out := maps.Clone(tops)
	for recv, ms := range methods {
		for _, runner := range runners[recv] {
			for m, text := range ms {
				out[runner+"/"+m] = text
			}
		}
	}
	return out, nil
}

func receiverType(recv *ast.FieldList) string {
	if len(recv.List) == 0 {
		return ""
	}
	t := recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return id.Name
	}
	return ""
}

// suitesRun names the types a test body hands to suite.Run as new(S) or &S{...}.
func suitesRun(body *ast.BlockStmt) []string {
	var out []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Run" {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "suite" {
			return true
		}
		switch arg := call.Args[1].(type) {
		case *ast.CallExpr:
			if id, ok := arg.Fun.(*ast.Ident); ok && id.Name == "new" && len(arg.Args) == 1 {
				if t, ok := arg.Args[0].(*ast.Ident); ok {
					out = append(out, t.Name)
				}
			}
		case *ast.UnaryExpr:
			if lit, ok := arg.X.(*ast.CompositeLit); ok && arg.Op == token.AND {
				if t, ok := lit.Type.(*ast.Ident); ok {
					out = append(out, t.Name)
				}
			}
		}
		return true
	})
	return out
}

// changedTests returns, sorted, the tests whose text at the fix differs from the parent's
// or that the parent lacks.
func changedTests(parent, fix map[string]string) []string {
	var out []string
	for name, text := range fix {
		if old, ok := parent[name]; !ok || old != text {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// runPattern is the -run pattern that selects every named test's top-level test.
func runPattern(names []string) string {
	var tops []string
	for _, n := range names {
		top, _, _ := strings.Cut(n, "/")
		tops = append(tops, regexp.QuoteMeta(top))
	}
	slices.Sort(tops)
	return "^(" + strings.Join(slices.Compact(tops), "|") + ")$"
}

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
}

// testRun is what one `go test -json` run reported: the last pass, fail or skip of each
// test by name, and the packages that failed as a whole, a build failure included.
type testRun struct {
	outcomes       map[string]string
	failedPackages []string
}

func parseTestJSON(r io.Reader) (testRun, error) {
	run := testRun{outcomes: map[string]string{}}
	failed := map[string]bool{}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<26)
	for sc.Scan() {
		line := sc.Bytes()
		// go test prints a build failure's compiler output as plain text before the
		// package's fail event.
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var ev testEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			return testRun{}, fmt.Errorf("go test -json: %w", err)
		}
		switch ev.Action {
		case "pass", "fail", "skip":
		default:
			continue
		}
		if ev.Test == "" {
			if ev.Action == "fail" {
				failed[ev.Package] = true
			}
			continue
		}
		run.outcomes[ev.Test] = ev.Action
	}
	if err := sc.Err(); err != nil {
		return testRun{}, err
	}
	run.failedPackages = slices.Sorted(maps.Keys(failed))
	return run, nil
}

// failToPass returns the tests among changed that pass at the fix and do not pass at the
// parent with the held-out tests applied, where a test that never ran did not pass.
func failToPass(changed []string, parent, fix testRun) []string {
	var out []string
	for _, name := range changed {
		if fix.outcomes[name] == "pass" && parent.outcomes[name] != "pass" {
			out = append(out, name)
		}
	}
	return out
}

var errNoTests = errors.New("the fix changes no test function")
