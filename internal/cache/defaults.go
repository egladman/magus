package cache

import (
	"os"
	"runtime"
	"strconv"
)

// DefaultConcurrency returns the balanced profile's width, for callers that hold no
// config; see ProfileConcurrency.
func DefaultConcurrency() int { return ProfileConcurrency(Balanced) }

// ConcurrencyProfile is a width relative to the machine rather than a number, so one
// committed magus.yaml means the same thing on a laptop and on a build server. Config
// names it as concurrency_profile.
type ConcurrencyProfile string

// The profiles. The zero value is Balanced.
const (
	Conservative ConcurrencyProfile = "conservative" // half the cores: a machine someone is also using
	Balanced     ConcurrencyProfile = "balanced"     // min(cores, 8): the default
	Aggressive   ConcurrencyProfile = "aggressive"   // every core: a dedicated build machine
)

// Width returns the slots p grants on a machine with cores CPUs, never fewer than one.
// An unknown profile gets Balanced's width: config validation refuses a misspelled one,
// so only a caller that skipped validation reaches the default arm with anything but "".
func (p ConcurrencyProfile) Width(cores int) int {
	var n int
	switch p {
	case Conservative:
		n = cores / 2
	case Aggressive:
		n = cores
	default:
		n = min(cores, 8)
	}
	return max(n, 1)
}

// ProfileConcurrency returns the width a configured profile names on this machine.
// MAGUS_CONCURRENCY, when set to a positive int, overrides every profile.
//
// On a GitHub-hosted runner the core count is 4 whatever NumCPU says: the runner reports
// its host's CPUs while giving the job a slice, so NumCPU over-subscribes badly there.
//
// This is the one place magus names a CI provider outside a spell, and it
// is startup ordering that forces it: the limiter is built before the
// magusfile is evaluated (see cmd/magus/main.go), so the CI provider spell
// that would otherwise answer this is not loaded yet. Everything else
// provider-specific lives in a spell; see internal/ci/annotate.
func ProfileConcurrency(profile ConcurrencyProfile) int {
	if v := os.Getenv("MAGUS_CONCURRENCY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	cores := runtime.NumCPU()
	if os.Getenv("GITHUB_ACTIONS") == "true" && os.Getenv("RUNNER_ENVIRONMENT") != "self-hosted" {
		cores = 4
	}
	return profile.Width(cores)
}

// ConfiguredConcurrency is ProfileConcurrency for a profile as config spells it.
func ConfiguredConcurrency(profile string) int {
	return ProfileConcurrency(ConcurrencyProfile(profile))
}

// ResolveConcurrency returns the width a run actually gets: an explicit configured value
// when positive, otherwise the profile's width, clamped to what the machine can run.
//
// It is the resolution the run path applies (cmd/magus/main.go when it builds the
// bootstrap limiter, Magus.limiter per workspace), so a reporter can answer "how many
// slots does this box give a build" without re-deriving it. Those two sites keep their
// own copy because they announce the clamp as it takes effect; this one only reports.
func ResolveConcurrency(configured int, profile string) int {
	if configured <= 0 {
		configured = ConfiguredConcurrency(profile)
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
