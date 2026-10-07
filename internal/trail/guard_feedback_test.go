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
	assert.Equal(t, GuardFeedback{
		Rule:     "raw-tool",
		Surface:  "shell.command",
		Denied:   1,
		Sessions: 1,
		Latest:   feedback[0].Latest, // wall-clock stamp
		Evidence: []string{"claude-code:one (1 denials)"},
	}, feedback[0])
	assert.False(t, feedback[0].NeedsReview(), "a one-off guard correction is not a recurring candidate")

	recordFeedbackCommand(t, base, "claude-code", "one", "go test ./...")
	recordFeedbackCommand(t, base, "claude-code", "one", "go test ./...")
	AppendAgentCommand(context.Background(), base, AgentCommand{
		Host: "claude-code", Session: "one", Tool: "shell.command", Command: "magus run test .", Decision: "pass",
	})

	feedback, err = RecentGuardFeedback(base, "one", 100)
	require.NoError(t, err)
	require.Len(t, feedback, 1)
	assert.Equal(t, GuardFeedback{
		Rule:             "raw-tool",
		Surface:          "shell.command",
		Denied:           3,
		Sessions:         1,
		FollowedSessions: 1,
		Latest:           feedback[0].Latest, // wall-clock stamp
		Evidence:         []string{"claude-code:one (3 denials)"},
	}, feedback[0])
	assert.True(t, feedback[0].NeedsReview(), "three same-session repeats are enough to review")
}

func TestRecentGuardFeedbackTreatsTwoSessionsAsRecurringWithoutClaimingSuccess(t *testing.T) {
	base := t.TempDir()
	recordFeedbackCommand(t, base, "codex", "first", "go test ./...")
	recordFeedbackCommand(t, base, "codex", "second", "go test ./...")

	feedback, err := RecentGuardFeedback(base, "", 100)
	require.NoError(t, err)
	require.Len(t, feedback, 1)
	// FollowedSessions stays zero: the hook observes requests, never target completion.
	assert.Equal(t, GuardFeedback{
		Rule:     "raw-tool",
		Surface:  "shell.command",
		Denied:   2,
		Sessions: 2,
		Latest:   feedback[0].Latest, // wall-clock stamp
		Evidence: []string{"codex:first (1 denials)", "codex:second (1 denials)"},
	}, feedback[0])
	assert.True(t, feedback[0].NeedsReview())
}

func TestRecentGuardFeedbackIgnoresDenialsNoHostClaims(t *testing.T) {
	base := t.TempDir()
	recordFeedbackCommand(t, base, "", "operator-probe", "go test ./...")
	recordFeedbackCommand(t, base, "", "operator-probe", "go test ./...")
	recordFeedbackCommand(t, base, "", "operator-probe", "go test ./...")

	feedback, err := RecentGuardFeedback(base, "", 100)
	require.NoError(t, err)
	assert.Empty(t, feedback, "a probe nobody wired is not evidence that a host integration should change")

	recordFeedbackCommand(t, base, "cursor", "real", "go test ./...")

	feedback, err = RecentGuardFeedback(base, "", 100)
	require.NoError(t, err)
	require.Len(t, feedback, 1)
	// The host-less denials must not inflate the count.
	assert.Equal(t, GuardFeedback{
		Rule:     "raw-tool",
		Surface:  "shell.command",
		Denied:   1,
		Sessions: 1,
		Latest:   feedback[0].Latest, // wall-clock stamp
		Evidence: []string{"cursor:real (1 denials)"},
	}, feedback[0])
	assert.False(t, feedback[0].NeedsReview(), "one operator cannot manufacture the repeat that promotes a candidate")
}

func recordFeedbackCommand(t *testing.T, base, host, session, command string) {
	t.Helper()
	AppendAgentCommand(context.Background(), base, AgentCommand{
		Host: host, Session: session, Tool: "shell.command", Command: command,
		Decision: "deny", Reason: "run through magus", Rule: "raw-tool",
	})
}
