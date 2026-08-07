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

// MachineCeiling is the most concurrent build steps this machine should ever run: one per
// CPU. It is a CEILING, not a default - DefaultConcurrency picks a smaller, gentler number
// when nothing is configured, and this only ever caps a number someone asked for.
func MachineCeiling() int {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	return n
}

// ClampConcurrency caps a configured concurrency at what the machine can actually run,
// reporting whether it had to.
//
// A configured value was previously taken at face value, so `concurrency: 32` in a
// magus.yaml written on a big machine ran 32 parallel steps on a laptop with 10 cores.
// That does not fail - it thrashes, and every target simply takes longer, which is the
// failure mode nothing ever gets attributed to. The number also outlives the machine it
// was chosen on: it travels in the repo, and the person it hurts is whoever has the
// smallest box.
//
// The caller announces the clamp rather than applying it silently. A run that is quietly
// narrower than requested is the same invisible-cause problem in the other direction.
func ClampConcurrency(requested int) (n int, clamped bool) {
	ceiling := MachineCeiling()
	if requested > ceiling {
		return ceiling, true
	}
	return requested, false
}
