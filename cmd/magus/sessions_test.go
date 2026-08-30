package main

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emitJournalEvent pushes one event through h along the real capture path, rather than
// hand-building the slog.Record. The attribute key that carries an Event is private
// to internal/journal, so a hand-built record would be testing a copy of it.
func emitJournalEvent(t *testing.T, h slog.Handler, e journal.Event) {
	t.Helper()
	journal.Emit(journal.WithLogger(context.Background(), journal.NewLogger(h)), e)
}

// recordSession writes one session's worth of facts through the shared wiring, which is
// the only producer the CLI has. It returns the session id so a row can be found by it.
func recordSession(t *testing.T, root, verb string, args []string, inv string) string {
	t.Helper()
	handlers := withSessionJournal(context.Background(), nil, root, verb, args)
	require.Len(t, handlers, 1)
	emitJournalEvent(t, handlers[0], journal.Event{Kind: journal.KindResult, Inv: inv, Target: "build", Status: journal.StatusPass})
	return inv
}

func TestSummarizeTargetsCollapsesRepeats(t *testing.T) {
	t.Parallel()
	got := summarizeTargets([]sessions.TargetResult{
		{Target: "build", Project: "api", Outcome: sessions.OutcomePass},
		{Target: "build", Project: "api", Outcome: sessions.OutcomePass},
		{Target: "build", Project: "web", Outcome: sessions.OutcomeFail},
		{Target: "lint", Project: "web", Outcome: sessions.OutcomePass, Replayed: true},
	})
	assert.Equal(t, "build api (pass), build web (fail), lint web (pass, cached)", got)
}

// sessionsLeaseCell returns the LEASE cell of the row for session, which is the column the console
// drawer's join is waiting on. Reading it positionally rather than searching the whole listing is
// what makes the "-" case assertable: a dash appears in every unattributed column.
func sessionsLeaseCell(t *testing.T, out, session string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// SESSION, date, time, HOST, LEASE, SPAWNER, PARENT, FACTS, TARGETS...
		if len(fields) > 4 && fields[0] == session {
			return fields[4]
		}
	}
	t.Fatalf("no row for session %q in:\n%s", session, out)
	return ""
}

func TestSessionsRendersTheLeaseColumnAttributedAndNot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root := t.TempDir()

	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=fleet/f3")
	recordSession(t, root, "run", []string{"build"}, "invLease")

	// A person at a keyboard carries no lease, and the same store has to render both.
	t.Setenv(trail.EnvBaggage, "")
	recordSession(t, root, "run", []string{"test"}, "invBare")

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, nil))
	})

	assert.Contains(t, out, "LEASE")
	assert.Equal(t, "fleet/f3", sessionsLeaseCell(t, out, "invLease"))
	assert.Equal(t, "-", sessionsLeaseCell(t, out, "invBare"), "an unattributed session reads like an unknown HOST, not like a blank")
}

// What the environment CLAIMED reaches the listing verbatim: the spawner label a person reads,
// and the parent span a later session can be joined to.
func TestSessionsRendersTheSpawnerAndParentFromTheEnvironment(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root := t.TempDir()

	t.Setenv(trail.EnvTraceparent, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=fleet/f3,"+trail.BaggageSpawner+"=claude%20code")
	recordSession(t, root, "run", []string{"build"}, "invSpawned")

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, nil))
	})

	assert.Contains(t, out, "SPAWNER")
	assert.Contains(t, out, "PARENT")
	assert.Contains(t, out, "claude code", "the label is percent-decoded and shown as claimed")
	assert.Contains(t, out, "00f067aa0ba902b7", "an unknown parent shows as the span id rather than as a blank")
	assert.Equal(t, "fleet/f3", sessionsLeaseCell(t, out, "invSpawned"))
}

// A parent span this store has a record of reads as that session's name; one it does not is
// still shown, because "magus has no record of it" and "spawned by nobody" are different facts.
func TestSessionParentResolvesOnlyWhatTheStoreHolds(t *testing.T) {
	t.Parallel()
	bySpan := map[string]string{"00f067aa0ba902b7": "invParent"}

	assert.Equal(t, "invParent", sessionParent(bySpan, "00f067aa0ba902b7"))
	assert.Equal(t, "a1b2c3d4e5f60718", sessionParent(bySpan, "a1b2c3d4e5f60718"))
	assert.Empty(t, sessionParent(bySpan, ""), "a session that claimed no parent has nothing to resolve")
}

// The listing answers "what happened", but its reader is often looking for "what needs
// me" - so an open queue gets one cross-reference line, and a quiet queue gets silence
// rather than a reassurance nobody asked for.
func TestSessionsCrossReferencesAnOpenAttentionQueue(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root := t.TempDir()
	recordSession(t, root, "run", []string{"build"}, "invQuiet")

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, nil))
	})
	assert.NotContains(t, out, "attention request", "a quiet queue earns no footer")

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	_, opened, err := sessions.OpenRequest(dir, "agent-1", sessions.AttentionOpen{
		Outcome: "waiting",
		Source:  "claude/Notification",
		Where:   root,
		Message: "needs the deploy key",
	}, sessions.SessionStart{Workspace: root})
	require.NoError(t, err)
	require.True(t, opened)

	out = captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, nil))
	})
	assert.Contains(t, out, "1 attention request(s) open; `magus session attention` lists them")
}

func TestSessionsRejectsPositionalArguments(t *testing.T) {
	err := sessionCmd(context.Background(), t.TempDir(), []string{"yesterday"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--limit")
	assert.Contains(t, err.Error(), "--since", "the rejected word is a time, so the error names the flag that takes one")
}

func TestSessionsRejectsANegativeLimit(t *testing.T) {
	err := sessionCmd(context.Background(), t.TempDir(), []string{"--limit=-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero or more")
	assert.Contains(t, err.Error(), "0 lists every session", "the error names the value that means what -1 was reaching for")
}

func TestParseSinceAcceptsBothSpellings(t *testing.T) {
	t.Parallel()

	empty, err := parseSince("")
	require.NoError(t, err)
	assert.True(t, empty.IsZero(), "no cutoff admits every session")

	back, err := parseSince("2h")
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().Add(-2*time.Hour), back, time.Minute)

	// A caller who writes the minus sign means the same window, not the future.
	negated, err := parseSince("-2h")
	require.NoError(t, err)
	assert.WithinDuration(t, back, negated, time.Minute)

	instant, err := parseSince("2026-08-20T09:00:00Z")
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 8, 20, 9, 0, 0, 0, time.UTC), instant.UTC())
}

func TestParseSinceRejectsAValueThatIsNeitherForm(t *testing.T) {
	t.Parallel()

	_, err := parseSince("last tuesday")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "neither a duration nor an RFC3339 timestamp")
	assert.Contains(t, err.Error(), "2h", "the error shows a value that would work")
}

// The filter turns on the LAST fact rather than the first, so a session that began
// before the window and is still working stays listed. Hiding it is exactly the wrong
// answer for the question --since asks.
func TestSessionsSinceKeepsALongSessionStillWorking(t *testing.T) {
	t.Parallel()

	now := time.Now()
	old := sessions.Summary{Session: "stale", StartedMs: now.Add(-48 * time.Hour).UnixMilli(), LastMs: now.Add(-47 * time.Hour).UnixMilli()}
	long := sessions.Summary{Session: "long", StartedMs: now.Add(-48 * time.Hour).UnixMilli(), LastMs: now.Add(-time.Minute).UnixMilli()}

	got := sessionsSince([]sessions.Summary{old, long}, now.Add(-2*time.Hour))
	require.Len(t, got, 1)
	assert.Equal(t, "long", got[0].Session)
}

func TestSessionsSinceWithNoCutoffKeepsEverything(t *testing.T) {
	t.Parallel()

	all := []sessions.Summary{{Session: "a"}, {Session: "b"}}
	assert.Equal(t, all, sessionsSince(all, time.Time{}))
}

// A store with sessions in it, all older than the window, must not read as a store
// nothing has ever written to: the second sends a person looking for a broken producer.
func TestSessionsSaysWhenTheWINDOWIsEmptyRatherThanTheStore(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root := t.TempDir()
	recordSession(t, root, "run", []string{"build"}, "invOld")

	// A cutoff ahead of the recorded fact is the deterministic way to empty the window;
	// a short duration would race the millisecond the fact was stamped in.
	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"--since", "2099-01-01T00:00:00Z"}))
	})
	assert.Contains(t, out, "no sessions in that window")
	assert.NotContains(t, out, "no sessions recorded yet")

	out = captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"--since", "24h"}))
	})
	assert.Contains(t, out, "invOld", "a session inside the window is still listed")
}
