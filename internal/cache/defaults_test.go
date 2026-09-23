package cache

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/types"
)

// TestProfileConcurrency_UnsetIsBalancedEverywhere pins the rule this package must not
// break: an unconfigured profile sizes to balanced the same way regardless of the
// process environment. magus reads no variable to guess where it is running, so the
// same command behaves the same on a laptop and on any CI provider; a caller that
// wants every core asks for concurrency_profile: aggressive explicitly.
func TestProfileConcurrency_UnsetIsBalancedEverywhere(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	want := runtime.NumCPU()
	if want > 8 {
		want = 8
	}
	assert.Equal(t, want, ProfileConcurrency(""))
}

// TestProfileConcurrency_ConcurrencyBeatsExplicitProfile verifies MAGUS_CONCURRENCY
// outranks every profile, explicit or not.
func TestProfileConcurrency_ConcurrencyBeatsExplicitProfile(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "3")
	assert.Equal(t, 3, ProfileConcurrency(""))
	assert.Equal(t, 3, ProfileConcurrency(types.ProfileAggressive))
}

// TestProfileConcurrency_GitHubHostedClampIsGone pins the retired GitHub-hosted 4-core
// hard-code as gone: GITHUB_ACTIONS and RUNNER_ENVIRONMENT, like every other
// environment variable naming where magus runs, no longer change the width at all.
func TestProfileConcurrency_GitHubHostedClampIsGone(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("RUNNER_ENVIRONMENT", "github-hosted")
	want := runtime.NumCPU()
	if want > 8 {
		want = 8
	}
	assert.Equal(t, want, ProfileConcurrency(""), "GITHUB_ACTIONS and RUNNER_ENVIRONMENT must not clamp to 4 or pick aggressive")
}

// TestDefaultConcurrency_LocalDefault verifies the balanced fallback.
func TestDefaultConcurrency_LocalDefault(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
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
	assert.Equal(t, 1, ResolveConcurrency(1, types.ProfileAggressive))
	assert.Equal(t, MachineCeiling(), ResolveConcurrency(0, types.ProfileAggressive))
}
