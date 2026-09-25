package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

func TestEnvName_NoPrefix(t *testing.T) {
	assert.Equal(t, "CACHE_DIR", EnvName("", "cache", "dir"))
}

func TestEnvName_WithPrefix(t *testing.T) {
	assert.Equal(t, "MAGUS_CACHE_DIR", EnvName("MAGUS", "cache", "dir"))
}

func TestEnvName_HyphenReplaced(t *testing.T) {
	assert.Equal(t, "MAGUS_DRY_RUN", EnvName("MAGUS", "dry-run"))
}

func TestFlagName_Parts(t *testing.T) {
	assert.Equal(t, "cache-dir", FlagName("cache", "dir"))
}

func TestFlagName_UnderscoreReplaced(t *testing.T) {
	assert.Equal(t, "dry-run", FlagName("dry_run"))
}

// Known is the registry, the per-VCS base-ref pattern, and what an older parent passes
// down; a retired name is not known, so the startup check can say what replaced it.
func TestKnownEnvVar(t *testing.T) {
	for _, name := range []string{"MAGUS_CACHE_DIR", "MAGUS_LEVEL", "MAGUS_VCS_GIT_BASE_REF", "MAGUS_VCS_JJ_BASE_REF", "MAGUS_DAEMON_SOCKET"} {
		assert.True(t, KnownEnvVar(name), name)
	}
	for _, name := range []string{"MAGUS_NO_WAIT", "MAGUS_DAEMON_ADDRESS", "MAGUS_CACHE_DIRR", "MAGUS_VCS__BASE_REF", "MAGUS_VCS_A_B_BASE_REF", "CACHE_DIR"} {
		assert.False(t, KnownEnvVar(name), name)
	}
}

// Only a provable mistake is a problem: a retired name, or one a typo away from a
// registered name. A name a newer magus or a repository's own tooling might read is not,
// including REGISTRY_KEY, three edits from REGISTRY_URL.
func TestEnvVarProblem(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		ok   bool
	}{
		{"MAGUS_NO_WAIT", "MAGUS_NO_WAIT was removed in v0.5.0; delete it; magus never waits on another invocation, a held lock or budget refuses at once with exit 75", true},
		{"MAGUS_DAEMON_ADDRESS", "MAGUS_DAEMON_ADDRESS was renamed to MAGUS_SERVER_ADDRESS in v0.5.0; magus no longer reads it", true},
		{"MAGUS_CACHE_DIRR", "MAGUS_CACHE_DIRR is set, but magus does not read it; did you mean MAGUS_CACHE_DIR?", true},
		{"MAGUS_CAHCE_DIR", "MAGUS_CAHCE_DIR is set, but magus does not read it; did you mean MAGUS_CACHE_DIR?", true},
		{"MAGUS_LEVLE", "", false},
		{"MAGUS_REGISTRY_KEY", "", false},
		{"MAGUS_FROBNICATE", "", false},
		{"MAGUS_CACHE_DIR", "", false},
		{"MAGUS_DAEMON_SOCKET", "", false},
		{"CACHE_DIRR", "", false},
	}
	for _, tc := range cases {
		msg, ok := EnvVarProblem(tc.name)
		assert.Equal(t, tc.ok, ok, tc.name)
		assert.Equal(t, tc.msg, msg, tc.name)
	}
}

// One line per problem, in a stable order; known, unknown, empty and non-MAGUS variables
// pass.
func TestMisconfiguredEnvNamesEachProblem(t *testing.T) {
	err := MisconfiguredEnv([]string{
		"PATH=/usr/bin",
		"MAGUS_CACHE_DIR=/tmp/c",
		"MAGUS_VCS_HG_BASE_REF=default",
		"MAGUS_NO_WAIT=",
		"MAGUS_DAEMON_ADDRESS=unix:///tmp/s.sock",
		"MAGUS_CACHE_DIRR=/tmp/c",
		"MAGUS_FROBNICATE=1",
		"__MAGUS_BIN=./magus",
	})
	require.ErrorIs(t, err, types.MisconfiguredEnvVar)
	want := "MAGUS_CACHE_DIRR is set, but magus does not read it; did you mean MAGUS_CACHE_DIR?\n" +
		"MAGUS_DAEMON_ADDRESS was renamed to MAGUS_SERVER_ADDRESS in v0.5.0; magus no longer reads it"
	assert.Equal(t, types.DiagnosticErrorf(types.MisconfiguredEnvVar, "%s", want).Error(), err.Error())

	require.NoError(t, MisconfiguredEnv([]string{"MAGUS_CACHE_DIR=/tmp/c", "MAGUS_LEVEL=2", "MAGUS_OWN_TOOL=1", "HOME=/h"}))
}

// A retired or inherited-only name is never also registered, or the startup check would
// pass it without saying what happened to it.
func TestRetiredEnvIsNotRegistered(t *testing.T) {
	for old := range retiredEnv {
		assert.False(t, registeredEnv()[old], "%s is retired but still in EnvVarDocs", old)
	}
	for name := range inheritedEnv {
		assert.False(t, registeredEnv()[name], "%s is inherited-only but also in EnvVarDocs", name)
	}
}
