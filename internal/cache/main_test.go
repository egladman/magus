package cache

import (
	"os"
	"testing"
)

// TestMain clears the cache-policy environment this package READS at Open time
// (cache.go's MAGUS_CACHE_IMMUTABLE, eviction.go's MAGUS_CACHE_SIZE_MB) before any
// test runs. A unit test's cache policy must come from the test, not from whatever
// the surrounding process happens to export.
//
// This is not hypothetical hygiene. ci.yaml sets
// `MAGUS_CACHE_IMMUTABLE: ${{ github.event_name == 'pull_request' }}`, so it is true
// on every PR run and false on a push to main. An immutable cache writes no entry,
// so every test that runs a step twice and asserts the second is a HIT fails on a PR
// and passes everywhere else - including locally, where the variable is unset. Worse,
// it silently overrides a test that already declared its intent: the eviction tests
// set MAGUS_CACHE_MODE=write and were overruled anyway.
//
// Tests that care about a value still set it themselves with t.Setenv, which runs
// after this and restores afterwards - defaults_test.go does exactly that for
// MAGUS_CONCURRENCY and GITHUB_ACTIONS. Clearing here rather than per-test also keeps
// t.Parallel available, which t.Setenv would forbid.
func TestMain(m *testing.M) {
	for _, k := range []string{"MAGUS_CACHE_IMMUTABLE", "MAGUS_CACHE_SIZE_MB"} {
		_ = os.Unsetenv(k)
	}
	os.Exit(m.Run())
}
