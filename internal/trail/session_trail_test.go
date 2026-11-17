package trail

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

// The lineage join: a loaded transcript and the guard's live observations share the host
// session id, so `show` reports what the trail in this checkout saw the session do, under
// which lease, and whom it spawned.
func TestSessionTrailHereJoinsThisCheckoutsTrail(t *testing.T) {
	cacheDir := t.TempDir()
	AppendAgentCommand(t.Context(), cacheDir, AgentCommand{Session: "s1", Tool: "shell.command", Command: "ls", Decision: verdict.Defer, Lease: "fleet/w1"})
	AppendAgentCommand(t.Context(), cacheDir, AgentCommand{Session: "s1", Tool: "shell.command", Command: "git push", Decision: "deny", Lease: "fleet/w1"})
	AppendAgentSpawn(t.Context(), cacheDir, AgentSpawn{Session: "s1", Child: "Explore", Context: "lease: fleet/w1-child\nlook\n"})

	tr, ok, err := SessionTrailHere(t.Context(), cacheDir, "s1")
	require.NoError(t, err)
	require.True(t, ok)
	var out strings.Builder
	RenderSessionTrail(&out, &tr)

	assert.Contains(t, out.String(), "Guard trail in this checkout: 2 command(s) observed, 1 denied, under lease fleet/w1, fleet/w1-child")
	assert.Contains(t, out.String(), "spawned Explore (lease fleet/w1-child)")
}

func TestSessionTrailHereIsAbsentForASessionThatRanElsewhere(t *testing.T) {
	cacheDir := t.TempDir()
	AppendAgentCommand(t.Context(), cacheDir, AgentCommand{Session: "s1", Tool: "shell.command", Command: "ls", Decision: verdict.Defer})

	_, ok, err := SessionTrailHere(t.Context(), cacheDir, "s2")
	require.NoError(t, err)
	assert.False(t, ok)
}
