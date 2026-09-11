package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRenderPromptCacheShowsEveryProvider(t *testing.T) {
	t.Parallel()

	last := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	now := last.Add(7 * time.Minute)
	var out strings.Builder
	renderPromptCache(&out, sessions.PromptCacheAt("s1", last, now), now)
	text := out.String()

	assert.Contains(t, text, "last tool call here 7m ago, session s1")
	for _, want := range []string{"Anthropic", "OpenAI", "Google Gemini"} {
		assert.Containsf(t, text, want, "a reader cannot tell which provider they are on, so every row has to be here")
	}
	assert.Contains(t, text, "closed 2m ago", "the 5m window is behind a 7m gap")
	assert.Contains(t, text, "closes in 53m")
	// The word magus has no standing to use: it sees a clock, never the provider's cache.
	assert.NotContains(t, text, "expire")
}

func TestPromptCacheForCheckoutReadsThisCheckoutsTrail(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(trail.EnvBaggage, "")
	previous := global
	t.Cleanup(func() { global = previous })
	global = globalFlags{}
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus.yaml"), []byte(""), 0o644))

	assert.Empty(t, promptCacheForCheckout(root, time.Now()).Providers,
		"a checkout no agent has run against has no activity to clock")

	cacheDir, err := magus.ResolveCacheDir(root, magus.WithLoadedConfig(globalCfg))
	require.NoError(t, err)
	trail.AppendAgentCommand(t.Context(), cacheDir, trail.AgentCommand{Session: "s1", Tool: "shell.command", Command: "ls", Decision: "pass"})

	clock := promptCacheForCheckout(root, time.Now())
	assert.Equal(t, "s1", clock.Session)
	assert.Len(t, clock.Providers, len(sessions.PromptCacheProviders))
}
