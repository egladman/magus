package sessions

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
)

type ActivitySuite struct{ suite.Suite }

func TestActivity(t *testing.T) { suite.Run(t, new(ActivitySuite)) }

// The lineage join: a loaded transcript and the guard's live observations share the host
// session id, so `show` reports what the trail in this checkout saw the session do, under
// which lease, and whom it spawned.
func (s *ActivitySuite) TestActivityHereJoinsThisCheckoutsLog() {
	t := s.T()
	cacheDir := t.TempDir()
	recorder.AppendAgentCommand(t.Context(), cacheDir, recorder.AgentCommand{Session: "s1", Tool: "shell.command", Command: "ls", Decision: verdict.Defer, Lease: "fleet/w1"})
	recorder.AppendAgentCommand(t.Context(), cacheDir, recorder.AgentCommand{Session: "s1", Tool: "shell.command", Command: "git push", Decision: "deny", Lease: "fleet/w1"})
	recorder.AppendAgentSpawn(t.Context(), cacheDir, recorder.AgentSpawn{Session: "s1", Child: "Explore", Context: "lease: fleet/w1-child\nlook\n"})

	tr, ok, err := ActivityHere(t.Context(), cacheDir, "s1")
	require.NoError(t, err)
	require.True(t, ok)
	var out strings.Builder
	RenderActivity(&out, &tr)

	assert.Contains(t, out.String(), "Guard activity in this checkout: 2 command(s) observed, 1 denied, under lease fleet/w1, fleet/w1-child")
	assert.Contains(t, out.String(), "spawned Explore (lease fleet/w1-child)")
}

func (s *ActivitySuite) TestActivityHereIsAbsentForASessionThatRanElsewhere() {
	t := s.T()
	cacheDir := t.TempDir()
	recorder.AppendAgentCommand(t.Context(), cacheDir, recorder.AgentCommand{Session: "s1", Tool: "shell.command", Command: "ls", Decision: verdict.Defer})

	_, ok, err := ActivityHere(t.Context(), cacheDir, "s2")
	require.NoError(t, err)
	assert.False(t, ok)
}
