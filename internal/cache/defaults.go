package cache

import (
	"os"
	"runtime"
	"strconv"
)

// DefaultConcurrency returns the concurrency cap: MAGUS_CONCURRENCY env var,
// then 4 on GitHub-hosted runners (RUNNER_ENVIRONMENT != self-hosted), then
// min(NumCPU, 8). A hosted runner reports its host's CPU count while giving
// the job a small slice of it, so NumCPU over-subscribes badly there.
//
// This is the one place magus names a CI provider outside a spell, and it
// is startup ordering that forces it: the limiter is built before the
// magusfile is evaluated (see cmd/magus/main.go), so the CI provider spell
// that would otherwise answer this is not loaded yet. Everything else
// provider-specific lives in a spell; see internal/ci/annotate.
func DefaultConcurrency() int {
	if v := os.Getenv("MAGUS_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("RUNNER_ENVIRONMENT") != "self-hosted" {
		return 4
	}
	n := runtime.NumCPU()
	if n > 8 {
		return 8
	}
	if n < 1 {
		return 1
	}
	return n
}
