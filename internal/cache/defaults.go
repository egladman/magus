package cache

import (
	"io"
	"os"
	"runtime"
	"strconv"

	"github.com/egladman/magus/internal/ci/annotate"
)

// DefaultConcurrency returns the concurrency cap: the MAGUS_CONCURRENCY env
// var, then whatever the CI provider running this job suggests, then
// min(NumCPU, 8).
//
// The provider gets a say because a hosted runner reports its host's CPU
// count while giving the job a small slice of it, so NumCPU over-subscribes
// badly there. Which provider, and what it suggests, is decided in
// internal/ci/annotate - this function names none of them.
func DefaultConcurrency() int {
	if v := os.Getenv("MAGUS_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	if n := annotate.Detect(io.Discard).Concurrency(); n > 0 {
		return n
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
