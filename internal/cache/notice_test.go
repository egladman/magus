package cache

import (
	"fmt"
	"strings"
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

// TestFailureExcerptPinsRealTestFailureAgainstLaterNoise: this package's own suite
// exercises magus's failure reporting, so it prints many EXPECTED "[fail]"/"cause:"
// lines as passing output, each matching diagnosticLine as well as a real failure
// does and each outnumbering the display budget. Without a structural tier the
// incidental matches that came LAST evicted the one real failure entirely.
func TestFailureExcerptPinsRealTestFailureAgainstLaterNoise(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	b.WriteString("--- FAIL: TestRunAllDependencyFailureCancelsDependents (0.00s)\n")
	b.WriteString("    barrier_test.go:283: Error Trace: B's fn ran even though its dependency A failed\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "[fail] fixture%d (ran, 1ms)\n  cause: fixture%d boom\n", i, i)
	}
	b.WriteString("FAIL\n")

	excerpt, omitted := failureExcerpt([]byte(b.String()), maxFailureExcerptLines)

	assert.Positive(t, omitted, "the fixture noise alone is well over budget")
	assert.Contains(t, string(excerpt), "TestRunAllDependencyFailureCancelsDependents",
		"the real failure must survive even though 40 later matches would otherwise evict it")
	assert.Contains(t, string(excerpt), "B's fn ran even though its dependency A failed")
}

// TestFailureExcerptHonoursLimitWhenEveryLineIsStructural is the other half of the
// rule above, and guards the mistake made while implementing it: ranking a structural
// marker above keyword noise must not become an EXEMPTION from the budget. A broadly
// failing `go test ./...` is nothing but structural markers, and exempting them turned
// the excerpt back into the full log dump failureExcerpt exists to prevent - while
// reporting omitted=0, so nothing on screen said the output had been let through.
func TestFailureExcerptHonoursLimitWhenEveryLineIsStructural(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 0; i < 30; i++ {
		fmt.Fprintf(&b, "--- FAIL: TestThing%d (0.01s)\n", i)
		fmt.Fprintf(&b, "    thing_test.go:%d: assertion failed\n", i)
		fmt.Fprintf(&b, "FAIL\tgithub.com/egladman/magus/pkg%d\t0.5s\n", i)
	}
	total := 90

	excerpt, omitted := failureExcerpt([]byte(b.String()), maxFailureExcerptLines)

	got := strings.Count(strings.TrimSuffix(string(excerpt), "\n"), "\n") + 1
	assert.LessOrEqual(t, got, maxFailureExcerptLines, "the budget is a cap, not a suggestion")
	assert.Equal(t, total-got, omitted, "omitted must account for every line actually withheld")
}
