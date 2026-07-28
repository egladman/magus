package archtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// baselinePath holds the _test.go files that predate the pairing rule.
// It is a ratchet, not a permanent exemption: the test fails on any file
// not in the baseline, and removing entries as they are paired up is the
// intended direction of travel. Adding an entry needs a reason.
const baselinePath = "testdata/unpaired_baseline.txt"

// repoRoot walks up from the package directory to the module root, so
// the test works from any working directory the go tool picks.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../..")
	require.NoError(t, err, "resolve repo root")
	_, err = os.Stat(filepath.Join(dir, "go.mod"))
	require.NoError(t, err, "go.mod should sit at %s", dir)
	return dir
}

// loadBaseline reads the recorded violations, ignoring blank lines and
// # comments so the file can explain itself.
func loadBaseline(t *testing.T) map[string]bool {
	t.Helper()
	raw, err := os.ReadFile(baselinePath)
	require.NoError(t, err, "read %s", baselinePath)

	got := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		got[line] = true
	}
	return got
}

// TestEveryTestFileHasSourceCounterpart enforces the repo convention
// that foo_test.go sits beside the foo.go it tests, so neither can be
// renamed or moved without the other coming along.
func TestEveryTestFileHasSourceCounterpart(t *testing.T) {
	t.Parallel()

	unpaired, err := FindUnpairedTests(t.Context(), repoRoot(t))
	require.NoError(t, err, "walk repo for unpaired test files")

	baseline := loadBaseline(t)
	var fresh []string
	for _, f := range unpaired {
		if !baseline[f] {
			fresh = append(fresh, f)
		}
	}

	assert.Empty(t, fresh,
		"these _test.go files have no matching .go beside them.\n"+
			"Rename the test to match the source file it covers (or the source to match the test).\n"+
			"If the file genuinely tests no single source file, add an exempt suffix in archtest.go.\n"+
			"Files: %s", strings.Join(fresh, "\n       "))
}

// TestBaselineHasNoStaleEntries keeps the ratchet honest: once a file is
// paired up (or deleted), its baseline entry must go too, or the baseline
// silently re-permits a name that could come back.
func TestBaselineHasNoStaleEntries(t *testing.T) {
	t.Parallel()

	unpaired, err := FindUnpairedTests(t.Context(), repoRoot(t))
	require.NoError(t, err, "walk repo for unpaired test files")

	live := map[string]bool{}
	for _, f := range unpaired {
		live[f] = true
	}

	var stale []string
	for f := range loadBaseline(t) {
		if !live[f] {
			stale = append(stale, f)
		}
	}

	assert.Empty(t, stale,
		"these baseline entries no longer describe an unpaired file; delete them from %s: %s",
		baselinePath, strings.Join(stale, ", "))
}

// TestIsExemptRecognisesConventionalNames pins the carve-outs so a future
// edit to the exempt lists cannot quietly widen them.
func TestIsExemptRecognisesConventionalNames(t *testing.T) {
	t.Parallel()

	for _, stem := range []string{
		"example", "examples", "bench", "fuzz", "internal",
		"hash_bench", "manifest_fuzz", "forecast_internal",
		"resolve_windows", "signals_linux", "mermaid_drift",
	} {
		assert.True(t, isExempt(stem), "%q should be exempt from the pairing rule", stem)
	}

	for _, stem := range []string{
		"log", "region", "probe", "cors", "tracer", "benchmark",
	} {
		assert.False(t, isExempt(stem), "%q should require a source counterpart", stem)
	}
}
