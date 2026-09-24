package cache

import (
	"os"
	"testing"
)

// TestMain clears MAGUS_LEVEL and MAGUS_INVOCATION_ANCESTORS before any test runs. This
// suite is RUN BY magus, so the test binary is a magus child and inherits both. The
// machine gate reads them to decide whether a run is nested and which claims are its
// parent's, so leaving them meant every gate test judged itself against the harness's
// invocation rather than the one the test set up.
//
// Both, not one. Clearing only the level left the ancestry behind, and the test for a
// run that has LOST its ancestry then found one, so it queued instead of refusing and
// hung the package for ten minutes. A test that wants either says so with t.Setenv.
//
// Cache policy needs no scrub: Open reads no environment, so a CI job's exports cannot
// reach a unit test's cache.
func TestMain(m *testing.M) {
	for _, k := range []string{"MAGUS_LEVEL", "MAGUS_INVOCATION_ANCESTORS"} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
