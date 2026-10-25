package job

import (
	"fmt"
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/egladman/magus/types"
)

// narrowedTestProblems is every go test check of row, its own or a check goal's, that
// narrows the run with -run or -skip to a set that leaves out a Test function defined in the
// Go packages of the job's write paths, as JobCheckUnrunnable. A hand-picked pattern passes
// while a test it never ran fails in the root's gate.
//
// Pure path math: the packages are the directories of the .go write paths (globs
// expanded), the tests are the top-level `func TestXxx(*testing.T)` declarations of the
// _test.go files go test builds there (see [testNames]), and a pattern is read the way go
// test reads it, by the first element of each top-level alternative. Only the go-test and
// test targets are read (see [runsGoTest]): another tool's -run says nothing about Go
// tests. A pattern that does not
// compile is left to go test, which refuses it itself. The paths are relative to the
// store's workspace root; a nil store, or one with no root, has no tree to read.
//
// A check naming exactly the packages of the write paths ([scopedToWritePackages]) may
// narrow their tests too: the integration that merges the job runs the full go-test.
func narrowedTestProblems(store *Store, row types.Job) types.JobProblems {
	if store == nil || store.root == "" {
		return nil
	}
	var ps types.JobProblems
	for _, g := range row.EffectiveGoals() {
		if g.Kind != types.GoalKindCheck || scopedToWritePackages(store.root, g.Check, row.WritePaths) {
			continue
		}
		named := fmt.Sprintf("%s's check", row.ID)
		if g.ID != types.PrimaryGoalID {
			named = fmt.Sprintf("%s's goal %q", row.ID, g.ID)
		}
		if p, ok := narrowedCheckProblem(store.root, named, g.Check, row.WritePaths); ok {
			p.Field = goalCheckField(row, g)
			ps = append(ps, p)
		}
	}
	return ps
}

// narrowedCheckProblem is [narrowedTestProblems] for one check, named as the problem names
// it, and false when the check leaves no test out.
func narrowedCheckProblem(root, named string, check types.LeaseCheck, writePaths []string) (types.JobProblem, bool) {
	if !runsGoTest(check) {
		return types.JobProblem{}, false
	}
	runArg, hasRun := goTestFlag(check.Args, "run")
	skipArg, hasSkip := goTestFlag(check.Args, "skip")
	if !hasRun && !hasSkip {
		return types.JobProblem{}, false
	}
	var run, skip *testFilter
	if hasRun {
		var ok bool
		if run, ok = parseTestFilter(runArg); !ok {
			return types.JobProblem{}, false
		}
	}
	if hasSkip {
		var ok bool
		if skip, ok = parseTestFilter(skipArg); !ok {
			return types.JobProblem{}, false
		}
	}
	tags, _ := goTestFlag(check.Args, "tags")
	tests := testsInWritePaths(root, writePaths, strings.FieldsFunc(tags, func(r rune) bool { return r == ',' || r == ' ' }))
	var (
		notRun, skipped []string
		packages        []string
	)
	for _, dir := range slices.Sorted(maps.Keys(tests)) {
		var missed []string
		for _, name := range tests[dir] {
			switch {
			case run != nil && !run.selects(name):
				notRun = append(notRun, name)
			case skip != nil && skip.excludes(name):
				skipped = append(skipped, name)
			default:
				continue
			}
			missed = append(missed, name)
		}
		if len(missed) > 0 {
			packages = append(packages, fmt.Sprintf("%s (%s)", dir, summarizeTests(missed)))
		}
	}
	if len(packages) == 0 {
		return types.JobProblem{}, false
	}
	var fixes []string
	if len(notRun) > 0 {
		fixes = append(fixes, widenRunFix(runArg, notRun))
	}
	if len(skipped) > 0 {
		fixes = append(fixes, narrowSkipFix(skipArg, skipped))
	}
	return types.JobProblem{
		Code: types.JobCheckUnrunnable,
		Message: fmt.Sprintf("%s `%s` leaves out tests defined in the packages of its write paths,"+
			" so a break they would catch passes unchecked: %s", named, check.String(), strings.Join(packages, " and ")),
		Fix: strings.Join(fixes, " and "),
	}, true
}

// runsGoTest reports whether check's -run and -skip select Go tests: the go spell's go-test
// op, or the workspace's test target, which forwards its `--` arguments to the go-test it
// composes. Both are read so the same narrowing gets one answer whichever name the check
// uses; a charm suffix does not change what the run selects.
func runsGoTest(check types.LeaseCheck) bool {
	if check.Script != "" {
		return false
	}
	spell, target, qualified := strings.Cut(check.Target, "::")
	if !qualified {
		spell, target = "", check.Target
	}
	name, _, _ := strings.Cut(target, ":")
	switch types.Normalize(name) {
	case "go-test":
		return spell == "" || spell == "go"
	case "test":
		return spell == ""
	}
	return false
}

// maxListedTests is how many test names the refusal prints per package, and maxPastedPattern
// the longest covering pattern it prints: past either the line stops being something a person
// reads or pastes.
const (
	maxListedTests   = 5
	maxPastedPattern = 120
)

// summarizeTests names the first maxListedTests of names and counts the rest.
func summarizeTests(names []string) string {
	if len(names) <= maxListedTests {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:maxListedTests], ", "), len(names)-maxListedTests)
}

// widenRunFix is the advice for tests -run leaves out: the pattern that covers them when it
// is short enough to paste, else dropping -run, since a package argument forwarded to
// go-test does not scope the run.
func widenRunFix(runArg string, notRun []string) string {
	if covering := fmt.Sprintf("%s|%s", runArg, exactTestPattern(notRun)); len(covering) <= maxPastedPattern {
		return fmt.Sprintf("widen -run to `%s` or drop -run", covering)
	}
	return "drop -run, since the tests it leaves out are too many for one pattern and a package argument does not scope go-test"
}

// narrowSkipFix is widenRunFix for the tests -skip drops.
func narrowSkipFix(skipArg string, skipped []string) string {
	if exact := exactTestPattern(skipped); len(skipArg)+len(exact) <= maxPastedPattern {
		return fmt.Sprintf("narrow -skip %q so it matches none of `%s` or drop -skip", skipArg, exact)
	}
	return "drop -skip, since the tests it drops are too many for one pattern and a package argument does not scope go-test"
}

// exactTestPattern is a pattern matching exactly the named tests.
func exactTestPattern(names []string) string {
	names = slices.Compact(slices.Sorted(slices.Values(names)))
	return fmt.Sprintf("^(%s)$", strings.Join(names, "|"))
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
			value, found = stripShellQuotes(args[i+1]), true
			i++
		case strings.HasPrefix(flag, name+"="):
			value, found = stripShellQuotes(strings.TrimPrefix(flag, name+"=")), true
		}
	}
	return value, found
}

func stripShellQuotes(s string) string {
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
// inside brackets or parentheses, or after a backslash, does not split. It reports false
// when an element does not compile, since go test itself refuses such a pattern and
// dropping the element would shift the ones after it into the wrong position.
func parseTestFilter(pattern string) (*testFilter, bool) {
	var (
		filter                  testFilter
		elems                   []string
		brackets, parens, start int
	)
	flush := func(elem string) bool {
		elems = append(elems, elem)
		alt := make([]*regexp.Regexp, 0, len(elems))
		for _, e := range elems {
			re, err := regexp.Compile(e)
			if err != nil {
				return false
			}
			alt = append(alt, re)
		}
		filter.alts = append(filter.alts, alt)
		elems = nil
		return true
	}
	for i := 0; i < len(pattern); {
		switch pattern[i] {
		case '[':
			brackets++
		case ']':
			if brackets--; brackets < 0 {
				brackets = 0
			}
		case '(':
			if brackets == 0 {
				parens++
			}
		case ')':
			if brackets == 0 {
				parens--
			}
		case '\\':
			i++
		case '/':
			if brackets == 0 && parens == 0 {
				elems = append(elems, pattern[start:i])
				start = i + 1
			}
		case '|':
			if brackets == 0 && parens == 0 {
				if !flush(pattern[start:i]) {
					return nil, false
				}
				start = i + 1
			}
		}
		i++
	}
	if !flush(pattern[min(start, len(pattern)):]) {
		return nil, false
	}
	return &filter, true
}

// selects reports whether -run keeps the top-level test name: some alternative's first
// element matches it. Later elements select subtests and never exclude the test itself.
func (f *testFilter) selects(name string) bool {
	return slices.ContainsFunc(f.alts, func(alt []*regexp.Regexp) bool {
		return alt[0].MatchString(name)
	})
}

// excludes reports whether -skip drops the top-level test name whole. Like testing's
// alternationMatch, the first alternative whose first element matches decides: it drops the
// test only when it has no second element, which would skip subtests alone.
func (f *testFilter) excludes(name string) bool {
	for _, alt := range f.alts {
		if alt[0].MatchString(name) {
			return len(alt) == 1
		}
	}
	return false
}

// testsInWritePaths returns, per workspace-relative package directory, the sorted names of
// the Test functions defined in the _test.go files beside the .go files the paths declare,
// built with tags.
func testsInWritePaths(root string, writePaths, tags []string) map[string][]string {
	out := map[string][]string{}
	for _, entry := range writePaths {
		for _, dir := range writePathDirs(root, entry) {
			if _, seen := out[dir]; !seen {
				if names := testNames(filepath.Join(root, filepath.FromSlash(dir)), tags); len(names) > 0 {
					out[dir] = names
				}
			}
		}
	}
	return out
}

// writePathDirs is the existing directories holding the Go files a write path names: none
// for a path that is not a .go file or a pattern for them.
//
// A pattern is walked from its literal prefix and skips what go list skips: testdata and
// vendor directories and any whose name starts with `_` or `.`. It also skips a nested
// module, which `go list ./...` does not enter. Without that a `**/*.go` path would walk
// every worktree and node_modules under the root and count fixtures as packages.
func writePathDirs(root, entry string) []string {
	entry, _ = types.SplitClaim(entry)
	entry = path.Clean(strings.TrimSpace(entry))
	if !strings.HasSuffix(path.Base(entry), ".go") {
		return nil
	}
	dir := path.Dir(entry)
	if !strings.ContainsAny(dir, globMeta) {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir))); err == nil && info.IsDir() {
			return []string{dir}
		}
		return nil
	}
	start := types.LiteralPrefix(dir)
	if start == "" {
		start = "."
	}
	var dirs []string
	_ = fs.WalkDir(os.DirFS(root), start, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return fs.SkipDir
		case !d.IsDir():
			return nil
		case p != start && ignoredDir(root, p):
			return fs.SkipDir
		}
		if ok, _ := doublestar.Match(dir, p); ok {
			dirs = append(dirs, p)
		}
		return nil
	})
	return dirs
}

// ignoredDir reports whether go list leaves out the workspace-relative directory rel, or
// it is the root of a nested module.
func ignoredDir(root, rel string) bool {
	name := path.Base(rel)
	if name == "testdata" || name == "vendor" || strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") {
		return true
	}
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel), "go.mod"))
	return err == nil
}

// testNames lists the Test functions declared by the _test.go files of dir that `go test
// -tags` with tags builds here, sorted. A file go ignores (named with a leading `_` or `.`)
// and one whose build constraints or GOOS/GOARCH suffix exclude it on this platform with
// those tags declare nothing: a test the check could never run is not one it leaves out. A
// file whose constraints do not read counts as built, so it cannot hide a test.
func testNames(dir string, tags []string) []string {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil
	}
	ctxt := build.Default
	ctxt.BuildTags = tags
	var names []string
	fset := token.NewFileSet()
	for _, file := range files {
		if built, err := ctxt.MatchFile(dir, filepath.Base(file)); err == nil && !built {
			continue
		}
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

// scopedToWritePackages reports whether check is a go test naming exactly the packages of
// writePaths: every package argument names one of them, `./dir` or `./dir/...`, and every
// one of them is named.
func scopedToWritePackages(root string, check types.LeaseCheck, writePaths []string) bool {
	if root == "" || !runsGoTest(check) {
		return false
	}
	patterns := goTestPackages(check.Args)
	var dirs []string
	for _, p := range writePaths {
		dirs = append(dirs, writePathDirs(root, p)...)
	}
	if len(patterns) == 0 || len(dirs) == 0 {
		return false
	}
	names := func(pattern, dir string) bool {
		pattern = path.Clean(pattern)
		if tree, ok := strings.CutSuffix(pattern, "/..."); ok {
			return coversPath(tree, dir) || tree == "."
		}
		return pattern == dir
	}
	for _, pattern := range patterns {
		if !slices.ContainsFunc(dirs, func(d string) bool { return names(pattern, d) }) {
			return false
		}
	}
	for _, d := range dirs {
		if !slices.ContainsFunc(patterns, func(p string) bool { return names(p, d) }) {
			return false
		}
	}
	return true
}

// goTestBoolFlags are the go test and go build flags that take no value, so the argument
// after one can be a package.
var goTestBoolFlags = map[string]bool{
	"a": true, "asan": true, "benchmem": true, "cover": true, "failfast": true, "fullpath": true, "json": true,
	"msan": true, "n": true, "race": true, "short": true, "trimpath": true, "v": true, "work": true, "x": true,
}

// goTestPackages is the package arguments among go test args: the relative patterns, `.`
// and those starting `./`, a workspace's check names. The value of a flag is never one.
func goTestPackages(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if flag, ok := strings.CutPrefix(arg, "-"); ok {
			flag = strings.TrimPrefix(strings.TrimPrefix(flag, "-"), "test.")
			if !strings.Contains(flag, "=") && !goTestBoolFlags[flag] {
				i++
			}
			continue
		}
		if arg == "." || strings.HasPrefix(arg, "./") {
			out = append(out, arg)
		}
	}
	return out
}
