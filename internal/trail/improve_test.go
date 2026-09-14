package trail

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRecentGuardFeedbackOnlyProposesRecurringEvidence(t *testing.T) {
	base := t.TempDir()
	recordFeedbackCommand(t, base, "claude-code", "one", "go test ./...")

	feedback, err := RecentGuardFeedback(base, "", 100)
	require.NoError(t, err)
	require.Len(t, feedback, 1)
	assert.Equal(t, "raw-tool", feedback[0].Rule)
	assert.Equal(t, 1, feedback[0].Denied)
	assert.False(t, feedback[0].NeedsReview(), "a one-off guard correction is not an improvement candidate")

	recordFeedbackCommand(t, base, "claude-code", "one", "go test ./...")
	recordFeedbackCommand(t, base, "claude-code", "one", "go test ./...")
	AppendAgentCommand(context.Background(), base, AgentCommand{
		Host: "claude-code", Session: "one", Tool: "shell.command", Command: "magus run test .", Decision: "pass",
	})

	feedback, err = RecentGuardFeedback(base, "one", 100)
	require.NoError(t, err)
	require.Len(t, feedback, 1)
	assert.Equal(t, 3, feedback[0].Denied)
	assert.Equal(t, 1, feedback[0].Sessions)
	assert.Equal(t, 1, feedback[0].FollowedSessions)
	assert.True(t, feedback[0].NeedsReview(), "three same-session repeats are enough to review")
}

func TestRecentGuardFeedbackTreatsTwoSessionsAsRecurringWithoutClaimingSuccess(t *testing.T) {
	base := t.TempDir()
	recordFeedbackCommand(t, base, "codex", "first", "go test ./...")
	recordFeedbackCommand(t, base, "codex", "second", "go test ./...")

	feedback, err := RecentGuardFeedback(base, "", 100)
	require.NoError(t, err)
	require.Len(t, feedback, 1)
	assert.Equal(t, 2, feedback[0].Denied)
	assert.Equal(t, 2, feedback[0].Sessions)
	assert.True(t, feedback[0].NeedsReview())
	assert.Zero(t, feedback[0].FollowedSessions, "the hook observes requests, never target completion")
}

func recordFeedbackCommand(t *testing.T, base, host, session, command string) {
	t.Helper()
	AppendAgentCommand(context.Background(), base, AgentCommand{
		Host: host, Session: session, Tool: "shell.command", Command: command,
		Decision: "deny", Reason: "run through magus", Rule: "raw-tool",
	})
}
