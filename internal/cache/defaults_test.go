package cache

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// TestProfileConcurrency_CIDefaultsAggressive verifies the generic CI environment
// variable (set by GitHub Actions, GitLab CI, CircleCI, Buildkite and most other
// providers) makes an unconfigured profile resolve to aggressive: every core, not
// the balanced default a laptop gets.
func TestProfileConcurrency_CIDefaultsAggressive(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("CI", "true")
	assert.Equal(t, runtime.NumCPU(), ProfileConcurrency(""), "CI=true with no profile configured should claim every core")
}

// TestProfileConcurrency_ExplicitProfileBeatsCI verifies an explicitly configured
// profile is never second-guessed by the CI default.
func TestProfileConcurrency_ExplicitProfileBeatsCI(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("CI", "true")
	cores := runtime.NumCPU()
	assert.Equal(t, min(cores, 8), ProfileConcurrency(types.ProfileBalanced), "an explicit balanced profile stays balanced under CI")
	assert.Equal(t, max(cores/2, 1), ProfileConcurrency(types.ProfileConservative), "an explicit conservative profile stays conservative under CI")
}

// TestProfileConcurrency_ConcurrencyBeatsCI verifies MAGUS_CONCURRENCY outranks the
// CI default, matching the documented precedence: MAGUS_CONCURRENCY, then
// concurrency, then concurrency_profile.
func TestProfileConcurrency_ConcurrencyBeatsCI(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "3")
	t.Setenv("CI", "true")
	assert.Equal(t, 3, ProfileConcurrency(""), "MAGUS_CONCURRENCY should override the CI default")
}

// TestProfileConcurrency_GitHubHostedClampIsGone pins the retired GitHub-hosted 4-core
// hard-code as gone: GITHUB_ACTIONS alone, without the generic CI variable, neither
// clamps cores nor selects the aggressive profile.
func TestProfileConcurrency_GitHubHostedClampIsGone(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("RUNNER_ENVIRONMENT", "github-hosted")
	want := runtime.NumCPU()
	if want > 8 {
		want = 8
	}
	assert.Equal(t, want, ProfileConcurrency(""), "GITHUB_ACTIONS alone must not clamp to 4 or pick aggressive")
}

// TestEffectiveProfile pins the resolution mem.BudgetMB relies on to claim memory
// under the same rule ProfileConcurrency claims cores under: unset resolves to
// aggressive only under CI, and an explicit profile is never second-guessed.
func TestEffectiveProfile(t *testing.T) {
	t.Setenv("CI", "true")
	assert.Equal(t, types.ProfileAggressive, EffectiveProfile(""), "unset resolves to aggressive under CI")
	assert.Equal(t, types.ProfileBalanced, EffectiveProfile(types.ProfileBalanced), "an explicit profile passes through under CI")
	assert.Equal(t, types.ProfileConservative, EffectiveProfile(types.ProfileConservative), "an explicit profile passes through under CI")

	t.Setenv("CI", "")
	assert.Equal(t, types.ConcurrencyProfile(""), EffectiveProfile(""), "unset stays unset outside CI")
}

// TestDefaultConcurrency_LocalDefault verifies the no-CI fallback.
func TestDefaultConcurrency_LocalDefault(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("CI", "")
	want := runtime.NumCPU()
	if want > 8 {
		want = 8
	}
	if want < 1 {
		want = 1
	}
	assert.Equal(t, want, DefaultConcurrency())
}

// TestClampConcurrency pins the ceiling. A configured concurrency travels in the repo, so
// a number chosen on a big machine lands on a small one and thrashes it, which does not
// fail, it just makes everything slower, so nothing gets attributed to it.
func TestClampConcurrency(t *testing.T) {
	ceiling := MachineCeiling()
	assert.GreaterOrEqual(t, ceiling, 1, "a machine always has at least one usable cpu")

	n, clamped := ClampConcurrency(ceiling * 4)
	assert.True(t, clamped)
	assert.Equal(t, ceiling, n, "a request above the machine is capped to it")

	n, clamped = ClampConcurrency(1)
	assert.False(t, clamped, "a request the machine can serve is left alone")
	assert.Equal(t, 1, n)

	n, clamped = ClampConcurrency(ceiling)
	assert.False(t, clamped, "exactly the ceiling is not over it")
	assert.Equal(t, ceiling, n)
}

// An explicit width overrides the profile; the profile applies only when none is set.
func TestResolveConcurrency_ExplicitOverridesProfile(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("CI", "")
	assert.Equal(t, 1, ResolveConcurrency(1, types.ProfileAggressive))
	assert.Equal(t, MachineCeiling(), ResolveConcurrency(0, types.ProfileAggressive))
}
