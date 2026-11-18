package sessions

import (
	"testing"
	"time"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/stretchr/testify/assert"
)

func TestPromptCacheForCheckoutReadsThisCheckoutsTrail(t *testing.T) {
	cacheDir := t.TempDir()

	assert.Empty(t, PromptCacheForCheckout(cacheDir, time.Now()).Providers,
		"a checkout no agent has run against has no activity to clock")

	recorder.AppendAgentCommand(t.Context(), cacheDir, recorder.AgentCommand{Session: "s1", Tool: "shell.command", Command: "ls", Decision: verdict.Defer})

	clock := PromptCacheForCheckout(cacheDir, time.Now())
	assert.Equal(t, "s1", clock.Session)
	assert.Len(t, clock.Providers, len(PromptCacheProviders))
}
