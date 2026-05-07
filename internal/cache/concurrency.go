package cache

import (
	"os"
	"runtime"
	"strconv"
)

// DefaultConcurrency returns the concurrency cap: MAGUS_CONCURRENCY env var,
// then 4 on GitHub-hosted runners (RUNNER_ENVIRONMENT != self-hosted), then min(NumCPU, 8).
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
