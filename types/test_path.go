package types

import "strings"

// testSuffixes and testPrefixes are the naming conventions that mark a file as a test in
// the languages a SCIP indexer covers. By NAME, because the graph is language-agnostic and
// a name is the one thing every language's convention agrees can carry it.
var (
	testSuffixes = []string{"_test.go", ".test.ts", ".test.tsx", ".test.js", ".spec.ts", ".spec.tsx", ".spec.js", "_test.py"}
	testPrefixes = []string{"test_"}
)

// IsTestPath reports whether a path (or a symbol Source, "<path>:<line>") is a test file.
// It recognizes Go (_test.go), TypeScript and JavaScript (.test and .spec), and Python
// (test_*.py, *_test.py) by file name alone, which is what the symbol indexes cover. One
// predicate for the graph lenses, the reading order and job terms, so they cannot drift
// apart.
func IsTestPath(source string) bool {
	path, _, _ := strings.Cut(source, ":")
	base := path[strings.LastIndex(path, "/")+1:]
	for _, s := range testSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	for _, p := range testPrefixes {
		if strings.HasPrefix(base, p) && strings.HasSuffix(base, ".py") {
			return true
		}
	}
	return false
}
