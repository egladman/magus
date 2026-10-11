package job

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/types"
)

// CheckVerdict is a [CheckGrader]'s answer. Denied is false for a check the guard lets the
// holder run, and for one the grader could not judge: a grader that cannot load the rules
// says so itself and accepts, because a refusal must come from a verdict, not from a
// failure to reach one.
type CheckVerdict struct {
	Denied bool
	// Rule names the rule that refused, as the guard prints it.
	Rule string
	// Reason is the guard's own wording, quoted back to the person forking.
	Reason string
}

// CheckGrader judges row's check the way the guard will judge row's holder running it.
// The guard lives above this package, so the CLI supplies the implementation.
type CheckGrader func(ctx context.Context, row types.Job) CheckVerdict

var defaultGrader atomic.Pointer[func(root string) CheckGrader]

// UseGrader sets the grader a Store gets when its Location names none, built for the
// Store's workspace root. The composition root calls it once at startup: the doors that
// open their own Store (magus\job.put in std) cannot import the guard, and would otherwise
// fork unchecked. Nil removes it.
func UseGrader(forRoot func(root string) CheckGrader) {
	if forRoot == nil {
		defaultGrader.Store(nil)
		return
	}
	defaultGrader.Store(&forRoot)
}

// RefuseDeniedCheck refuses a job whose declared check the guard would deny its holder. The
// holder runs the check under the lease, so a denied one leaves the job unable to verify
// itself and ends it unverified. A Store with no grader, and a job with no check, refuse
// nothing.
func RefuseDeniedCheck(ctx context.Context, store *Store, row types.Job) error {
	if store == nil || store.grader == nil || row.Check == nil {
		return nil
	}
	verdict := store.grader(ctx, row)
	if !verdict.Denied {
		return nil
	}
	rule := ""
	if verdict.Rule != "" {
		rule = " (" + verdict.Rule + ")"
	}
	return fmt.Errorf("job: %s's check `%s` would be denied to its worker by the guard%s:\n    %s\n"+
		"the holder runs the check under its lease, so a denied one can never verify the job."+
		" Declare a target that compiles without building the binary, such as `go::go-vet .`:"+
		" `magus job fork %s --check 'go::go-vet .'`",
		row.ID, row.Check.String(), rule,
		strings.ReplaceAll(strings.TrimSpace(verdict.Reason), "\n", "\n    "), row.ID)
}

// RefuseSkippedTests refuses a job whose go test check narrows the run with -run or -skip
// to a set that leaves out a Test function defined in the Go packages of the job's write
// paths. A hand-picked pattern passes while a test it never ran fails in the root's gate.
//
// Pure path math: the packages are the directories of the .go write paths (globs
// expanded), the tests are the top-level `func TestXxx(*testing.T)` declarations of their
// _test.go files, and a pattern is read the way go test reads it, by the first element of
// each top-level alternative. A store with no workspace root has no tree to read.
func RefuseSkippedTests(store *Store, id string, candidate types.Job) error {
	if store == nil || store.root == "" || candidate.Check == nil || candidate.Check.Script != "" {
		return nil
	}
	runArg, hasRun := goTestFlag(candidate.Check.Args, "run")
	skipArg, hasSkip := goTestFlag(candidate.Check.Args, "skip")
	if !hasRun && !hasSkip {
		return nil
	}
	tests := testsInWritePaths(store.root, candidate.WritePaths)
	if len(tests) == 0 {
		return nil
	}
	var run, skip *testFilter
	if hasRun {
		run = parseTestFilter(runArg)
	}
	if hasSkip {
		skip = parseTestFilter(skipArg)
	}
	var (
		notRun, skipped []string
		lines           []string
	)
	for _, dir := range sortedKeys(tests) {
		var missed []string
		for _, name := range tests[dir] {
			switch {
			case run != nil && !run.runs(name):
				notRun = append(notRun, name)
			case skip != nil && skip.skips(name):
				skipped = append(skipped, name)
			default:
				continue
			}
			missed = append(missed, name)
		}
		if len(missed) > 0 {
			lines = append(lines, "    "+dir+": "+strings.Join(missed, ", "))
		}
	}
	if len(lines) == 0 {
		return nil
	}
	var fixes []string
	if len(notRun) > 0 {
		fixes = append(fixes, fmt.Sprintf("widen -run to `%s|%s`, or drop -run", runArg, exactTests(notRun)))
	}
	if len(skipped) > 0 {
		fixes = append(fixes, fmt.Sprintf("narrow -skip %q so it matches none of `%s`, or drop -skip", skipArg, exactTests(skipped)))
	}
	return fmt.Errorf("job: %s's check `%s` leaves out tests defined in the packages of its write paths,"+
		" so a break they would catch passes unchecked:\n%s\n%s",
		id, candidate.Check.String(), strings.Join(lines, "\n"), strings.Join(fixes, "; "))
}

// exactTests is a pattern matching exactly the named tests.
func exactTests(names []string) string {
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	return "^(" + strings.Join(names, "|") + ")$"
}

func sortedKeys(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// goTestFlag reads the last -name, --name, -test.name or --test.name among args, in either
// the `-name value` or the `-name=value` spelling, as flag does. The value loses one pair of
// shell quotes: a check is rendered into a shell line, which removes them before go test
// reads the pattern.
func goTestFlag(args []string, name string) (string, bool) {
	var (
		value string
		found bool
	)
	for i := 0; i < len(args); i++ {
		flag := strings.TrimLeft(args[i], "-")
		if len(flag) == len(args[i]) || len(args[i])-len(flag) > 2 {
			continue
		}
		flag = strings.TrimPrefix(flag, "test.")
		switch {
		case flag == name && i+1 < len(args):
			value, found = unquote(args[i+1]), true
			i++
		case strings.HasPrefix(flag, name+"="):
			value, found = unquote(strings.TrimPrefix(flag, name+"=")), true
		}
	}
	return value, found
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return s
}

// testFilter is a go test -run or -skip pattern split the way testing splits it: into
// top-level alternatives, each into slash-separated elements.
type testFilter struct {
	alts [][]*regexp.Regexp
}

// parseTestFilter splits pattern by the rules of testing's splitRegexp: a `/` or `|`
// inside brackets or parentheses, or after a backslash, does not split. An element that
// does not compile is left out, since go test itself refuses the pattern.
func parseTestFilter(pattern string) *testFilter {
	var (
		f          testFilter
		cur        []string
		cs, cp, at int
	)
	s := pattern
	flush := func(elem string) {
		cur = append(cur, elem)
		var alt []*regexp.Regexp
		for _, e := range cur {
			if re, err := regexp.Compile(e); err == nil {
				alt = append(alt, re)
			}
		}
		f.alts = append(f.alts, alt)
		cur = nil
	}
	for i := 0; i < len(s); {
		switch s[i] {
		case '[':
			cs++
		case ']':
			if cs--; cs < 0 {
				cs = 0
			}
		case '(':
			if cs == 0 {
				cp++
			}
		case ')':
			if cs == 0 {
				cp--
			}
		case '\\':
			i++
		case '/':
			if cs == 0 && cp == 0 {
				cur = append(cur, s[at:i])
				at = i + 1
			}
		case '|':
			if cs == 0 && cp == 0 {
				flush(s[at:i])
				at = i + 1
			}
		}
		i++
	}
	flush(s[min(at, len(s)):])
	return &f
}

// runs reports whether -run keeps the top-level test name: some alternative's first
// element matches it. Later elements select subtests and never exclude the test itself.
func (f *testFilter) runs(name string) bool {
	return slices.ContainsFunc(f.alts, func(alt []*regexp.Regexp) bool {
		return len(alt) == 0 || alt[0].MatchString(name)
	})
}

// skips reports whether -skip drops the top-level test name whole: an alternative of one
// element that matches it. A pattern with a second element skips only subtests.
func (f *testFilter) skips(name string) bool {
	return slices.ContainsFunc(f.alts, func(alt []*regexp.Regexp) bool {
		return len(alt) == 1 && alt[0].MatchString(name)
	})
}

// testsInWritePaths returns, per workspace-relative package directory, the sorted names of
// the Test functions defined in the _test.go files beside the .go files the paths declare.
func testsInWritePaths(root string, writePaths []string) map[string][]string {
	out := map[string][]string{}
	for _, decl := range writePaths {
		for _, dir := range goPackageDirs(root, decl) {
			if _, seen := out[dir]; !seen {
				if names := testFuncs(filepath.Join(root, filepath.FromSlash(dir))); len(names) > 0 {
					out[dir] = names
				}
			}
		}
	}
	return out
}

// goPackageDirs is the existing directories holding the Go files decl names: none for a
// path that is not a .go file or a pattern for them.
func goPackageDirs(root, decl string) []string {
	decl, _ = types.SplitClaim(decl)
	decl = path.Clean(strings.TrimSpace(decl))
	if !strings.HasSuffix(path.Base(decl), ".go") {
		return nil
	}
	dir := path.Dir(decl)
	if !strings.ContainsAny(dir, "*?[{") {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err == nil && info.IsDir() {
			return []string{dir}
		}
		return nil
	}
	matches, err := doublestar.Glob(os.DirFS(root), dir)
	if err != nil {
		return nil
	}
	return slices.DeleteFunc(matches, func(m string) bool {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(m)))
		return err != nil || !info.IsDir()
	})
}

// testFuncs lists the Test functions the _test.go files of dir declare, sorted.
func testFuncs(dir string) []string {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil
	}
	var names []string
	fset := token.NewFileSet()
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		for _, d := range parsed.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && isTestFunc(fn) {
				names = append(names, fn.Name.Name)
			}
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// isTestFunc applies go test's definition: a plain function named Test followed by anything
// but a lower-case letter, taking one *testing.T.
func isTestFunc(fn *ast.FuncDecl) bool {
	name := fn.Name.Name
	if fn.Recv != nil || !strings.HasPrefix(name, "Test") || fn.Type.Params == nil || len(fn.Type.Params.List) != 1 {
		return false
	}
	if rest := name[len("Test"):]; rest != "" {
		if r, _ := utf8.DecodeRuneInString(rest); unicode.IsLower(r) {
			return false
		}
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == "T"
}
