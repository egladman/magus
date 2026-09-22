package doctor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

// TestCheckRecurringGuardDenialsReportsNoTrailAsClean covers the common case: a
// workspace with no activity trail yet, or one with only one-off denials that
// never crossed NeedsReview's threshold.
func TestCheckRecurringGuardDenialsReportsNoTrailAsClean(t *testing.T) {
	root := t.TempDir()

	got := (&runner{root: root}).checkRecurringGuardDenials()

	assert.Equal(t, types.DoctorCheck{
		Name:    "recurring-guard-denials",
		Status:  types.DoctorOK,
		Message: "no guard denial has recurred across sessions",
	}, got)
}

// TestCheckRecurringGuardDenialsIgnoresAOneOffDenial pins that a single denial, well
// under NeedsReview's threshold, is not reported: a one-off is a correction in
// progress, not a recurring pattern.
func TestCheckRecurringGuardDenialsIgnoresAOneOffDenial(t *testing.T) {
	root := t.TempDir()
	recordGuardDenial(t, filepath.Join(root, ".magus"), "claude-code", "session-1")

	got := (&runner{root: root}).checkRecurringGuardDenials()

	assert.Equal(t, types.DoctorCheck{
		Name:    "recurring-guard-denials",
		Status:  types.DoctorOK,
		Message: "no guard denial has recurred across sessions",
	}, got)
}

// TestCheckRecurringGuardDenialsReportsFactsOnly pins the shape this check reports
// after the agent-improve advice layer was removed: rule, surface, denial count,
// session count, and followed rate, nothing more. No destination, no confidence, no
// advice text; a human reads the facts and decides.
func TestCheckRecurringGuardDenialsReportsFactsOnly(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".magus")
	recordGuardDenial(t, base, "claude-code", "session-1")
	recordGuardDenial(t, base, "claude-code", "session-1")
	recordGuardDenial(t, base, "claude-code", "session-1")
	trail.AppendAgentCommand(context.Background(), base, trail.AgentCommand{
		Host: "claude-code", Session: "session-1", Tool: "shell.command",
		Command: "magus run test .", Decision: "pass",
	})

	got := (&runner{root: root}).checkRecurringGuardDenials()

	assert.Equal(t, types.DoctorCheck{
		Name:     "recurring-guard-denials",
		Status:   types.DoctorAdvice,
		Evidence: types.EvidenceInferred,
		Message:  "1 guard rule(s) denied the same shape more than once; the friction is real whether the rule is right or the reader is",
		Details:  []string{"raw-tool on shell.command: 3 denial(s) across 1 session(s), followed 1/1"},
	}, got)
}

// TestCheckRecurringGuardDenialsCountsTwoSessionsWithoutClaimingFollowUp covers the
// other NeedsReview path (two sessions rather than three same-session repeats), and
// that a followed rate of 0 is reported honestly rather than omitted.
func TestCheckRecurringGuardDenialsCountsTwoSessionsWithoutClaimingFollowUp(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, ".magus")
	recordGuardDenial(t, base, "codex", "first")
	recordGuardDenial(t, base, "codex", "second")

	got := (&runner{root: root}).checkRecurringGuardDenials()

	assert.Equal(t, types.DoctorCheck{
		Name:     "recurring-guard-denials",
		Status:   types.DoctorAdvice,
		Evidence: types.EvidenceInferred,
		Message:  "1 guard rule(s) denied the same shape more than once; the friction is real whether the rule is right or the reader is",
		Details:  []string{"raw-tool on shell.command: 2 denial(s) across 2 session(s), followed 0/2"},
	}, got)
}

// recordGuardDenial appends one raw-tool denial for host/session, matching the shape
// the guard writes to the activity trail on a real deny.
func recordGuardDenial(t *testing.T, base, host, session string) {
	t.Helper()
	trail.AppendAgentCommand(context.Background(), base, trail.AgentCommand{
		Host: host, Session: session, Tool: "shell.command", Command: "go test ./...",
		Decision: "deny", Reason: "run through magus", Rule: "raw-tool",
	})
}
