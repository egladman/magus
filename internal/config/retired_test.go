package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A retired variable is an error naming its replacement, one line per variable set, in
// a stable order; an environment that sets none of them passes.
func TestRetiredEnvNamesEachReplacement(t *testing.T) {
	env := map[string]string{
		"MAGUS_DAEMON_WORKSPACES": "/a:/b",
		"MAGUS_DAEMON_ADDRESS":    "unix:///tmp/s.sock",
		"MAGUS_SERVER_ADDRESS":    "unix:///tmp/s.sock",
	}
	err := RetiredEnv(func(k string) string { return env[k] })
	require.EqualError(t, err,
		"MAGUS_DAEMON_ADDRESS was renamed to MAGUS_SERVER_ADDRESS in v0.5.0; magus no longer reads it\n"+
			"MAGUS_DAEMON_WORKSPACES was renamed to MAGUS_SERVER_WORKSPACES in v0.5.0; magus no longer reads it")

	assert.NoError(t, RetiredEnv(func(string) string { return "" }))
}

// Every replacement a retired variable names is one magus reads, so the error never
// sends somebody from one dead name to another.
func TestRetiredEnvReplacementsAreKnown(t *testing.T) {
	known := map[string]bool{}
	for _, key := range KnownKeys() {
		known[EnvName("MAGUS", strings.Split(key, ".")...)] = true
	}
	for old, r := range retiredEnv {
		if r.replacement == "" {
			assert.NotEmpty(t, r.instead, "%s names neither a replacement nor what to do", old)
			continue
		}
		assert.True(t, known[r.replacement], "%s names %s, which magus does not read", old, r.replacement)
	}
	for old, repl := range retiredKeys {
		assert.NotContains(t, knownKeysIn(configTypeName), old, "%s is retired but still a key", old)
		assert.Contains(t, knownKeysIn(configTypeName), repl, "%s names %s, which is not a key", old, repl)
	}
}
