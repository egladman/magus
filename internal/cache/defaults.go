package cache

import (
	"os"
	"runtime"
	"strconv"

	"github.com/egladman/magus/types"
)

// DefaultConcurrency returns the balanced profile's width, for callers that hold no
// config; see ProfileConcurrency.
func DefaultConcurrency() int { return ProfileConcurrency(types.ProfileBalanced) }

// ProfileConcurrency returns the width a configured profile names on this machine.
// MAGUS_CONCURRENCY, when set to a positive int, overrides every profile. An unset
// profile (the zero value) sizes to balanced, the same everywhere: magus reads no
// environment variable to guess where it is running, so a magusfile behaves
// identically on a laptop and on a CI runner unless something explicitly asked
// otherwise. A CI runner that wants every core passes concurrency_profile: aggressive
// (a flag, env var, or magus.yaml) like any other caller would.
func ProfileConcurrency(profile types.ConcurrencyProfile) int {
	if v := os.Getenv("MAGUS_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return profile.Width(runtime.NumCPU())
}

// ResolveConcurrency returns the width a run actually gets: an explicit configured value
// when positive, otherwise the profile's width, clamped to what the machine can run.
//
// It is the resolution the run path applies (cmd/magus/main.go when it builds the
// bootstrap limiter, Magus.limiter per workspace), so a reporter can answer "how many
// slots does this box give a build" without re-deriving it. Those two sites keep their
// own copy because they announce the clamp as it takes effect; this one only reports.
func ResolveConcurrency(configured int, profile types.ConcurrencyProfile) int {
	if configured <= 0 {
		configured = ProfileConcurrency(profile)
	}
	n, _ := ClampConcurrency(configured)
	return n
}

// MachineCeiling is the most concurrent build steps this machine should ever run: one per
// CPU. It is a CEILING, not a default: DefaultConcurrency picks a smaller, gentler number
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
// That does not fail; it thrashes, and every target simply takes longer, which is the
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
