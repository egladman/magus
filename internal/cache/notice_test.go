package cache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFailureExcerptFindsEarlyDiagnostic(t *testing.T) {
	t.Parallel()
	data := []byte("preparing suite\n# example.test\nlink: fingerprint mismatch: got A, want B\nok package/one\nok package/two\nFAIL\n")

	excerpt, omitted := failureExcerpt(data, maxFailureExcerptLines)

	assert.Positive(t, omitted)
	assert.Contains(t, string(excerpt), "# example.test")
	assert.Contains(t, string(excerpt), "link: fingerprint mismatch: got A, want B")
	assert.Contains(t, string(excerpt), "FAIL")
}

func TestFailureExcerptFallsBackToTail(t *testing.T) {
	t.Parallel()
	data := []byte("one\ntwo\nthree\nfour\n")

	excerpt, omitted := failureExcerpt(data, 2)

	assert.Equal(t, 2, omitted)
	assert.Equal(t, "three\nfour\n", string(excerpt))
}

// TestFailureExcerptIgnoresDependencyPathsNamedError is the real regression, taken
// verbatim from this repo's own failing lint runs: `go mod edit -json` prints the
// github.com/pkg/errors dependency, a substring test for "error" matched its import
// path, and the surrounding JSON came along as context. Every failing run showed
// three lines of go.mod above the diagnostic the reader actually wanted.
func TestFailureExcerptIgnoresDependencyPathsNamedError(t *testing.T) {
	t.Parallel()
	data := []byte(`		{
			"Path": "github.com/pkg/errors",
			"Version": "v0.9.1",
		},
	]
}
docs/concepts/cache.md:340 error MD032/blanks-around-lists Lists should be surrounded by blank lines
`)

	excerpt, _ := failureExcerpt(data, maxFailureExcerptLines)

	assert.NotContains(t, string(excerpt), "pkg/errors", "a dependency path is not a diagnostic")
	assert.NotContains(t, string(excerpt), `"Version"`, "and neither is the JSON around it")
	assert.Contains(t, string(excerpt), "MD032", "the actual diagnostic must survive")
}

// TestFailureExcerptKeepsTheKeywordsThatMatter guards the other direction: tightening
// the match must not stop recognizing an ordinary diagnostic.
func TestFailureExcerptKeepsTheKeywordsThatMatter(t *testing.T) {
	t.Parallel()
	for _, line := range []string{
		"error: undefined variable x",
		"2 errors occurred",
		"--- FAIL: TestThing",
		"panic: runtime error: index out of range",
		"cannot find module",
		"fatal: not a git repository",
	} {
		assert.True(t, diagnosticLine.MatchString(line), "should be diagnostic: %q", line)
	}
	for _, line := range []string{
		`	"Path": "github.com/pkg/errors",`,
		"ok  \tgithub.com/egladman/magus/internal/errors\t0.2s",
		"compiling internal/failure/handler.go",
	} {
		assert.False(t, diagnosticLine.MatchString(line), "should NOT be diagnostic: %q", line)
	}
}

// TestFailureExcerptPrefersTheLastMatches pins the budget rule: a tool prints its
// setup before it prints what went wrong, so filling the limit from the top drops
// the actual failure.
func TestFailureExcerptPrefersTheLastMatches(t *testing.T) {
	t.Parallel()
	data := []byte("error: early noise\nfiller\nfiller\nerror: the real failure\n")

	excerpt, _ := failureExcerpt(data, 2)

	assert.Contains(t, string(excerpt), "the real failure")
	assert.NotContains(t, string(excerpt), "early noise")
}
