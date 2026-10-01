package testlayout

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const pairingDoc = `check that every test file pairs with a source file of the same name

X_test.go accompanies X.go. It also pairs when X less its build suffixes names a
source file (rawconn_linux_test.go and rawconn.go), or when X names a platform
family (tree_test.go and tree_linux.go with no tree.go). Nothing else pairs: not a
conventional name such as example_test.go or main_test.go, not a benchmark file,
not a marker comment, and there is no allow list.

When some prefix of X names a source file - resolver_edge_cases_test.go against
resolver.go - the message says which file's tests these are.`

// Pairing reports a test file with no source file of the same stem. It has no
// options and reports under the name testpair rather than testlayout, so the one
// directive a tree legitimately writes, //nolint:testlayout for an external test
// package that dodges an import cycle, does not silence it.
var Pairing = &analysis.Analyzer{Name: "testpair", Doc: pairingDoc, Run: runPairing}

// benchmarkNames are the conventional names for a file of benchmarks kept apart
// from the tests of what they measure. They pair like any other name; matching
// them only picks the message.
var benchmarkNames = []string{
	"bench_test.go", "*_bench_test.go",
	"benchmark_test.go", "*_benchmark_test.go",
}

func runPairing(pass *analysis.Pass) (any, error) {
	// A Go package is one directory, so in practice this holds a single entry.
	// Keying by directory rather than reading once per pass avoids assuming that
	// of a driver that positions a file elsewhere.
	listings := map[string]map[string]bool{}

	for path, f := range goFiles(pass) {
		name := filepath.Base(path)
		if !strings.HasSuffix(name, "_test.go") {
			continue
		}

		dir := filepath.Dir(path)

		sources, ok := listings[dir]
		if !ok {
			sources = sourceNames(dir)
			listings[dir] = sources
		}

		if message := pairing(name, sources); message != "" {
			pass.Report(analysis.Diagnostic{Pos: f.Package, Message: message})
		}
	}

	return nil, nil
}

// pairing returns the diagnostic for the test file name, or "" when it pairs.
func pairing(name string, sources map[string]bool) string {
	base := strings.TrimSuffix(name, "_test.go")

	// Exact pair first. Trimming ahead of this lookup hides a source file carrying
	// the same suffix (cipher_gcm_arm64_test.go beside cipher_gcm_arm64.go), and
	// the trimmed name then reaches the narrowing search and matches some shorter
	// name. The crypto packages are full of that shape.
	if sources[base] {
		return ""
	}

	// Then the pair a build suffix hides: rawconn_linux_test.go covers the linux
	// build of rawconn.go. That is a constraint, not a narrowing.
	trimmed := trimBuildSuffixes(base)
	if sources[trimmed] {
		return ""
	}

	// Narrowing before the family: a family is not a file the tests could have been
	// added to, and a named owner is the more useful message.
	if owner := nearestSource(trimmed, sources); owner != "" {
		return fmt.Sprintf("%s narrows %s.go; these tests belong in %s_test.go", name, owner, owner)
	}

	if pairsWithFamily(trimmed, sources) {
		return ""
	}

	if matchesAny(benchmarkNames, name) {
		return fmt.Sprintf("%s keeps benchmarks apart from the tests of the file they measure; "+
			"move them into that file's _test.go", name)
	}

	return fmt.Sprintf("%s has no source file of the same name; move its tests into the _test.go "+
		"of the file they exercise, or move the code it owns into %s.go", name, base)
}

// pairsWithFamily reports whether some source file is base plus build suffixes only,
// so tree_test.go pairs with tree_linux.go.
func pairsWithFamily(base string, sources map[string]bool) bool {
	for source := range sources {
		if source != base && trimBuildSuffixes(source) == base {
			return true
		}
	}

	return false
}

func matchesAny(patterns []string, name string) bool {
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		ok, _ := filepath.Match(pattern, name)
		return ok
	})
}

// sourceNames returns the base names of dir's non-test Go files with the .go
// suffix trimmed, so resolver.go yields "resolver". An unreadable directory
// yields no names: a cgo or overlay path outside the module, or a directory
// removed between package load and analysis, is reported as unpaired rather than
// failing the whole run.
//
// The listing comes off disk rather than out of the pass because the pass does
// not always hold the answer: an external test package is loaded with none of
// the package's source files, and a source file a build constraint excludes is
// in no pass on this platform yet still pairs.
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
