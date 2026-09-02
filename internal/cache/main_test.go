package cache

import (
	"os"
	"testing"
)

// TestMain clears the cache-policy environment this package READS at Open time
// (cache.go's MAGUS_CACHE_WRITE_ENABLED, eviction.go's MAGUS_CACHE_SIZE_MB) before any
// test runs. A unit test's cache policy must come from the test, not from whatever
// the surrounding process happens to export.
//
// This is not hypothetical hygiene. ci.yaml sets
// `MAGUS_CACHE_WRITE_ENABLED: ${{ github.event_name != 'pull_request' }}`, so it is
// false on every PR run and true on a push to main. A write-disabled cache writes no
// entry, so every test that runs a step twice and asserts the second is a HIT fails on
// a PR and passes everywhere else - including locally, where the variable is unset.
//
// Tests that care about a value still set it themselves with t.Setenv, which runs
// after this and restores afterwards - defaults_test.go does exactly that for
// MAGUS_CONCURRENCY and GITHUB_ACTIONS. Clearing here rather than per-test also keeps
// t.Parallel available, which t.Setenv would forbid.
// MAGUS_LEVEL is cleared for the same reason and it is the sharpest case of it: this
// suite is RUN BY magus, so the test binary is a magus child and inherits level 1. The
// machine gate reads that to mean "nested", and every gate test then took the
// nested-without-ancestry path and refused instead of queueing - a whole feature's
// tests exercising the branch none of them meant to. A test that wants to be nested
// says so with t.Setenv.
func TestMain(m *testing.M) {
	for _, k := range []string{"MAGUS_CACHE_WRITE_ENABLED", "MAGUS_CACHE_SIZE_MB", "MAGUS_LEVEL"} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
