package cache

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDefaultConcurrency_GitHubActions verifies that a GitHub-hosted
// runner forces a 4-CPU cap when MAGUS_CONCURRENCY is unset. Hosted
// runners over-report NumCPU because the container's CPU limit isn't
// reflected, so the cap is essential to avoid OOM / throttling on
// standard runners.
func TestDefaultConcurrency_GitHubActions(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("RUNNER_ENVIRONMENT", "github-hosted")
	assert.Equal(t, 4, DefaultConcurrency(), "DefaultConcurrency should cap at 4 under GitHub-hosted runner")
}

// TestDefaultConcurrency_SelfHostedRunner verifies that a self-hosted
// runner is exempt from the 4-CPU clamp and uses its real CPU count:
// the over-report problem is specific to shared hosted runners.
func TestDefaultConcurrency_SelfHostedRunner(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("RUNNER_ENVIRONMENT", "self-hosted")
	want := runtime.NumCPU()
	if want > 8 {
		want = 8
	}
	if want < 1 {
		want = 1
	}
	assert.Equal(t, want, DefaultConcurrency(), "self-hosted should not clamp")
}

// TestDefaultConcurrency_EnvOverridesGitHubActions verifies that an
// explicit MAGUS_CONCURRENCY wins over the GitHub Actions auto-cap.
func TestDefaultConcurrency_EnvOverridesGitHubActions(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "12")
	t.Setenv("GITHUB_ACTIONS", "true")
	assert.Equal(t, 12, DefaultConcurrency(), "env should override")
}

// TestDefaultConcurrency_LocalDefault verifies the no-CI fallback.
func TestDefaultConcurrency_LocalDefault(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("GITHUB_ACTIONS", "")
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

// Each profile is a width relative to the machine, never below one slot. balanced is
// the pre-profile default exactly, so an unset profile changes nothing on upgrade.
func TestConcurrencyProfileWidth(t *testing.T) {
	cases := []struct {
		profile ConcurrencyProfile
		cores   int
		want    int
	}{
		{"", 10, 8}, {Balanced, 10, 8}, {Balanced, 4, 4},
		{Conservative, 10, 5}, {Conservative, 1, 1},
		{Aggressive, 10, 10}, {Aggressive, 32, 32},
		{Balanced, 0, 1}, {"turbo", 10, 8},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, tc.profile.Width(tc.cores), "%q on %d cores", tc.profile, tc.cores)
	}
}

// A hosted runner's core count is 4 for every profile, and MAGUS_CONCURRENCY beats them all.
func TestProfileConcurrency_HostedRunnerAndEnv(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("GITHUB_ACTIONS", "true")
	t.Setenv("RUNNER_ENVIRONMENT", "github-hosted")
	assert.Equal(t, 2, ProfileConcurrency(Conservative))
	assert.Equal(t, 4, ProfileConcurrency(Aggressive))

	t.Setenv("MAGUS_CONCURRENCY", "3")
	assert.Equal(t, 3, ProfileConcurrency(Aggressive))
}

// An explicit width overrides the profile; the profile applies only when none is set.
func TestResolveConcurrency_ExplicitOverridesProfile(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "")
	t.Setenv("GITHUB_ACTIONS", "")
	assert.Equal(t, 1, ResolveConcurrency(1, "aggressive"))
	assert.Equal(t, MachineCeiling(), ResolveConcurrency(0, "aggressive"))
}
