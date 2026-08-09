// Package testsprawl defines an analyzer that reports a Go test file whose name
// narrows the name of a source file in the same directory.
//
// It catches test sprawl: rather than adding cases to resolver_test.go, someone
// writes resolver_edge_cases_test.go, and the package fills with test files no
// source file accounts for. The Go toolchain does not object, so only a reviewer
// catches it.
//
// Narrowing is the reported case, not a missing sibling. 34% of the standard
// library's test files are named after a cross-cutting concern no source file
// owns, which makes reporting those a house rule. See Options.Unpaired.
//
// The analyzer depends on no linter runner. The golangci-lint plugin lives in
// the plugin subpackage.
//
// It is not hermetic: sourceNames reads the directory, so a driver caching
// results against declared package inputs (go vet's unitchecker) can replay a
// stale verdict after you add or remove a sibling file.
package testsprawl

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const doc = `check that a test file's name does not narrow a source file's name

X_test.go accompanies X.go. When there is no X.go but some prefix of X names a
source file - resolver_edge_cases_test.go against resolver.go - the test file
has narrowed an existing name, and its cases belong in that file's test file.`

// conventional lists test filenames that have no source counterpart by design,
// with the number of uses each has in the Go standard library. Every entry is
// measured rather than assumed, because an exemption is a hole punched in the
// rule and a hole nobody can point at a corpus for is a guess.
var conventional = []string{
	"export_test.go", "*_export_test.go", // 43 + 9: the hatch to an external test package
	"example_test.go", "*_example_test.go", // 105: godoc renders examples from it
	"main_test.go",                           // 6: the TestMain entry point
	"internal_test.go", "*_internal_test.go", // 3: white-box companion to an external test package
	"all_test.go",                      // 5: package-wide suite
	"bench_test.go", "*_bench_test.go", // 9
	"benchmark_test.go", "*_benchmark_test.go", // 6
	"fuzz_test.go", "*_fuzz_test.go", // 15
}

// buildSuffixes are trailing segments the Go build system reads as a constraint
// rather than part of the name, so rawconn_unix_test.go still pairs with
// rawconn.go.
//
// Every entry is a GOOS or GOARCH from `go tool dist list` on Go 1.26, plus
// `unix`, which go/build honours without being a platform. An earlier version
// also carried `generic`, `other`, `stub`, `posix`, and `asm` because they read
// like build tags. None is one, and each made resolver_generic_test.go beside
// resolver.go silently exempt. Nobody reports a linter for staying quiet, so a
// suffix earns its place only when the toolchain acts on it.
var buildSuffixes = []string{
	"aix", "android", "darwin", "dragonfly", "freebsd", "illumos", "ios", "js",
	"linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows",
	"386", "amd64", "arm", "arm64", "loong64", "mips", "mips64", "mips64le",
	"mipsle", "ppc64", "ppc64le", "riscv64", "s390x", "wasm",
	"unix",
}

// Options configures the analyzer returned by [New]. The json tags are golangci-lint's
// settings block: the plugin decodes straight into this struct rather than keeping a
// parallel copy, so adding an option here cannot be silently dropped on the way in.
type Options struct {
	// Allow lists globs in [path/filepath.Match] syntax, matched against the base
	// name of a test file, that are exempt from the rule.
	Allow []string `json:"allow"`

	// Unpaired extends the rule to every test file with no source file of the same
	// name, not only one that narrows an existing name. See the package comment for
	// what that costs.
	Unpaired bool `json:"unpaired"`
}

// New returns an analyzer configured by opts, erroring on a malformed Allow glob.
//
// Checked here rather than at the point of use, which runs once per test file per
// package: a config typo would lint clean until it reached a package holding a
// non-exempt test file, then fail from somewhere unrelated to the mistake.
func New(opts Options) (*analysis.Analyzer, error) {
	for _, pattern := range opts.Allow {
		if _, err := filepath.Match(pattern, "probe"); err != nil {
			return nil, fmt.Errorf("testsprawl: allow pattern %q: %w", pattern, err)
		}
	}

	return newAnalyzer(opts), nil
}

// Analyzer is the analyzer with no exemptions beyond the conventional names and
// narrowing-only reporting. It takes its configuration at construction, so this
// one runs with the defaults; use [New] to change them.
var Analyzer = newAnalyzer(Options{})

func newAnalyzer(opts Options) *analysis.Analyzer {
	l := linter{
		unpaired: opts.Unpaired,
		// Combined once. Per test file, this list is walked but never rebuilt.
		exempt: slices.Concat(conventional, opts.Allow),
	}

	return &analysis.Analyzer{Name: "testsprawl", Doc: doc, Run: l.run}
}

type linter struct {
	exempt   []string
	unpaired bool
}

func (l linter) run(pass *analysis.Pass) (any, error) {
	// A Go package is one directory, so in practice this holds a single entry.
	// Keying by directory rather than reading once per pass avoids assuming that
	// of a driver that positions a file elsewhere.
	listings := map[string]map[string]bool{}

	for _, f := range pass.Files {
		// A file with no position - a synthesized or overlay-sourced AST - has no
		// name to reason about and no directory to look beside it. Skipping beats
		// dereferencing nil and panicking the driver.
		tf := pass.Fset.File(f.Pos())
		if tf == nil {
			continue
		}

		path := tf.Name()

		name := filepath.Base(path)
		if !strings.HasSuffix(name, "_test.go") || l.exempted(name) {
			continue
		}

		dir := filepath.Dir(path)

		sources, ok := listings[dir]
		if !ok {
			// An unlistable directory is one this analyzer has nothing to say about:
			// a cgo or overlay path outside the module, or a directory removed
			// between package load and analysis. Returning the error would fail the
			// whole lint run over a filename heuristic.
			sources = sourceNames(dir)
			listings[dir] = sources
		}

		if message := l.check(name, sources); message != "" {
			pass.Report(analysis.Diagnostic{Pos: f.Package, Message: message})
		}
	}

	return nil, nil
}

// check returns the diagnostic for the test file name, or "" when it is fine.
func (l linter) check(name string, sources map[string]bool) string {
	base := strings.TrimSuffix(name, "_test.go")

	// Exact pair first. Trimming ahead of this lookup hides a source file carrying
	// the same suffix (cipher_gcm_arm64_test.go beside cipher_gcm_arm64.go), and
	// the trimmed name then reaches the narrowing search and matches some shorter
	// name. The crypto packages are full of that shape.
	if sources[base] {
		return ""
	}

	// Then the pair a build suffix hides: rawconn_unix_test.go covers the unix
	// build of rawconn.go. That is a constraint, not a narrowing.
	trimmed := trimBuildSuffixes(base)
	if sources[trimmed] {
		return ""
	}

	if owner := nearestSource(trimmed, sources); owner != "" {
		return fmt.Sprintf("%s narrows %s.go; these tests belong in %s_test.go", name, owner, owner)
	}

	if l.unpaired {
		return fmt.Sprintf("%s has no source file of the same name", name)
	}

	return ""
}

// exempted reports whether the test file name matches a conventional name or a
// configured glob. The patterns were validated by [New], so a match error here
// would mean a pattern that changed after construction, and there is none.
func (l linter) exempted(name string) bool {
	for _, pattern := range l.exempt {
		if ok, _ := filepath.Match(pattern, name); ok {
			return true
		}
	}

	return false
}

// trimBuildSuffixes removes every trailing segment the Go build system reads as a
// constraint, so rawconn_unix and bytes_js_wasm both come back as the name the
// source file carries.
func trimBuildSuffixes(base string) string {
	for {
		i := strings.LastIndex(base, "_")
		if i < 0 || !slices.Contains(buildSuffixes, base[i+1:]) {
			return base
		}

		base = base[:i]
	}
}

// sourceNames returns the base names of dir's non-test Go files with the .go
// suffix trimmed, so resolver.go yields "resolver". An unreadable directory
// yields no names, which reports nothing rather than failing the run.
//
// The listing comes off disk rather than out of the pass because the pass does
// not always hold the answer: an external test package - the _test suffixed one
// a file declares as package foo_test - is loaded on its own, with none of the
// package's source files in it.
func sourceNames(dir string) map[string]bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	names := make(map[string]bool, len(entries))

	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		names[strings.TrimSuffix(name, ".go")] = true
	}

	return names
}

// nearestSource trims trailing underscore-separated segments off base and returns
// the longest remaining prefix that names a source file, or "" when none does.
func nearestSource(base string, sources map[string]bool) string {
	for {
		i := strings.LastIndex(base, "_")
		if i < 0 {
			return ""
		}

		base = base[:i]
		if sources[base] {
			return base
		}
	}
}
