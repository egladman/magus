package trail

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/json"
)

func TestRecentGuardFeedbackOnlyProposesRecurringEvidence(t *testing.T) {
	base := t.TempDir()
	recordFeedbackCommand(t, base, "claude-code", "one", "go test ./...")

	feedback, err := RecentGuardFeedback(base, "", 100)
	require.NoError(t, err)
	require.Len(t, feedback, 1)
	assert.Equal(t, GuardFeedback{
		Rule:     "raw-tool",
		Tool:     "shell.command",
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
		Tool:             "shell.command",
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
		Tool:     "shell.command",
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
		Tool:     "shell.command",
		Denied:   1,
		Sessions: 1,
		Latest:   feedback[0].Latest, // wall-clock stamp
		Evidence: []string{"cursor:real (1 denials)"},
	}, feedback[0])
	assert.False(t, feedback[0].NeedsReview(), "one operator cannot manufacture the repeat that promotes a candidate")
}

func TestLeaseDenialsReadsOneLeasesDenialsOldestFirst(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	deny := func(lease, rule, decision string) {
		AppendAgentCommand(t.Context(), base, AgentCommand{
			Host: "claude-code", Session: "one", Tool: "shell.command", Command: "edit " + rule,
			Decision: decision, Reason: rule + " says no", Rule: rule, Lease: lease,
		})
	}
	deny("wave/a", "lease-write", "deny")
	deny("wave/a", "", "pass")
	deny("wave/b", "focus-read", "deny")
	deny("wave/a", "raw-tool", "deny")

	got, known, err := LeaseDenials(base, "wave/a", 0, 64)
	require.NoError(t, err)
	assert.True(t, known, "a trail inside the retained window is read whole")
	require.Len(t, got, 2)
	assert.Equal(t, []string{"lease-write", "raw-tool"}, []string{got[0].Rule, got[1].Rule})
	wantFirst := got[0]
	wantFirst.Reason, wantFirst.Action = "lease-write says no", "shell.command"
	assert.Equal(t, wantFirst, got[0])
	assert.Positive(t, got[0].Ts)

	newest, _, err := LeaseDenials(base, "wave/a", 0, 1)
	require.NoError(t, err)
	require.Len(t, newest, 1)
	assert.Equal(t, "raw-tool", newest[0].Rule, "the cap keeps the newest")

	later, known, err := LeaseDenials(base, "wave/a", got[1].Ts+3600, 64)
	require.NoError(t, err)
	assert.Empty(t, later)
	assert.True(t, known, "nothing since then is none, not unknown, while the trail is whole")
}

// A full window with no agent command at or before since may have rotated the attempt's
// denials away, so an empty answer is unknown.
func TestLeaseDenialsSaysUnknownOnceTheTrailRotatedPastSince(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(base, dir), 0o755))
	line, err := json.Marshal(Event{Ts: 5_000_000, Kind: KindJob, Action: "graph build", Outcome: OutcomeOK})
	require.NoError(t, err)
	body := strings.Repeat(string(line)+"\n", maxEvents)
	require.NoError(t, os.WriteFile(eventsPath(base), []byte(body), 0o644))

	got, known, err := LeaseDenials(base, "wave/a", 1, 64)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.False(t, known)
}

func recordFeedbackCommand(t *testing.T, base, host, session, command string) {
	t.Helper()
	AppendAgentCommand(context.Background(), base, AgentCommand{
		Host: host, Session: session, Tool: "shell.command", Command: command,
		Decision: "deny", Reason: "run through magus", Rule: "raw-tool",
	})
}
