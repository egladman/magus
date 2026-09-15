package buzz

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLineCoverageReportAttributesHitsToSourceFile verifies that an entry-file
// coverprofile counts found lines from the stamped chunk and hits from the step
// hook, and that an import compiled with a cleared SourceFile does not appear.
func TestLineCoverageReportAttributesHitsToSourceFile(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	defer s.Close()

	cov := NewLineCoverage()
	s.SetSourceFile("fixture.buzz")
	s.EnableLineCoverage(cov)

	src := "fun add(a: int, b: int) > int {\n" + // 1
		"  return a + b\n" + // 2
		"}\n" + // 3
		"fun unused() > int {\n" + // 4
		"  return 99\n" + // 5
		"}\n" + // 6
		"final x = add(1, 2)\n" // 7
	require.NoError(t, s.Exec(context.Background(), src), "exec")

	report := cov.Report()
	require.Contains(t, report, "SF:fixture.buzz")
	assert.Contains(t, report, "DA:2,", "return line is executable")
	assert.Contains(t, report, "DA:7,", "top-level call is executable")
	// unused()'s body was compiled (found) but never run (0 hits).
	assert.Regexp(t, `(?m)^DA:5,0$`, report, "unused body found but not hit")
	assert.NotContains(t, report, "SF:<buzz>")
	assert.True(t, strings.HasSuffix(strings.TrimSpace(report), "end_of_record"), report)
}

// TestLineCoverageIgnoresImportBodies verifies execImport clears SourceFile so
// an imported module does not dilute the entry file's coverprofile.
func TestLineCoverageIgnoresImportBodies(t *testing.T) {
	s := NewSession(context.Background(), WithEmbedded())
	defer s.Close()

	cov := NewLineCoverage()
	s.SetSourceFile("entry.buzz")
	s.EnableLineCoverage(cov)

	// Simulate what resolveImport does for a flat file import: execImport with
	// the entry stamp still set on the session. The clear inside execImport is
	// what this pins.
	_, err := s.execImport(context.Background(), "export fun helper() > int { return 1; }\n")
	require.NoError(t, err)

	report := cov.Report()
	assert.NotContains(t, report, "SF:entry.buzz", "import must not stamp the entry path")
	assert.Equal(t, "TN:\n", report, "no SourceFile -> empty LCOV body")
}
