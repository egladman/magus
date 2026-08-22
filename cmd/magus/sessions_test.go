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
	handlers := withSessionJournal(nil, root, verb, args)
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

// sessionsUnitCell returns the UNIT cell of the row for session, which is the column the console
// drawer's join is waiting on. Reading it positionally rather than searching the whole listing is
// what makes the "-" case assertable: a dash appears in every unattributed column.
func sessionsUnitCell(t *testing.T, out, session string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		// SESSION, date, time, HOST, UNIT, FACTS, TARGETS...
		if len(fields) > 4 && fields[0] == session {
			return fields[4]
		}
	}
	t.Fatalf("no row for session %q in:\n%s", session, out)
	return ""
}

func TestSessionsRendersTheUnitColumnAttributedAndNot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root := t.TempDir()

	t.Setenv(trail.EnvUnit, "fleet/f3")
	recordSession(t, root, "run", []string{"build"}, "invUnit")

	// A person at a keyboard carries no unit, and the same store has to render both.
	t.Setenv(trail.EnvUnit, "")
	recordSession(t, root, "run", []string{"test"}, "invBare")

	out := captureStdout(t, func() {
		require.NoError(t, sessionsCmd(context.Background(), root, nil))
	})

	assert.Contains(t, out, "UNIT")
	assert.Equal(t, "fleet/f3", sessionsUnitCell(t, out, "invUnit"))
	assert.Equal(t, "-", sessionsUnitCell(t, out, "invBare"), "an unattributed session reads like an unknown HOST, not like a blank")
}

func TestSessionsRejectsPositionalArguments(t *testing.T) {
	err := sessionsCmd(context.Background(), t.TempDir(), []string{"yesterday"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--limit")
	assert.Contains(t, err.Error(), "--since", "the rejected word is a time, so the error names the flag that takes one")
}

func TestSessionsRejectsANegativeLimit(t *testing.T) {
	err := sessionsCmd(context.Background(), t.TempDir(), []string{"--limit=-1"})
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
		require.NoError(t, sessionsCmd(context.Background(), root, []string{"--since", "2099-01-01T00:00:00Z"}))
	})
	assert.Contains(t, out, "no sessions in that window")
	assert.NotContains(t, out, "no sessions recorded yet")

	out = captureStdout(t, func() {
		require.NoError(t, sessionsCmd(context.Background(), root, []string{"--since", "24h"}))
	})
	assert.Contains(t, out, "invOld", "a session inside the window is still listed")
}
