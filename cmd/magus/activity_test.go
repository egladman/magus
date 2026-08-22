package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sessionjournal"
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

func TestSessionFactHandlerRecordsOneFactPerTargetResult(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	root := t.TempDir()

	h := beginSessionJournal(root, []string{"run", "build"}, "test-version")
	require.NotNil(t, h)

	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Project: "api", Status: journal.StatusPass, DurMs: 20, Ref: "out1"})
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "test", Project: "api", Status: journal.StatusFail, DurMs: 5})
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "lint", Project: "web", Status: journal.StatusCached})

	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)
	fold, err := sessionjournal.Read(dir)
	require.NoError(t, err)

	sessions := sessionjournal.Summarize(fold)
	require.Len(t, sessions, 1)
	assert.Equal(t, "inv1", sessions[0].Session, "the invocation id is the session id")
	assert.Equal(t, "run build", sessions[0].Command)
	assert.Equal(t, root, sessions[0].Workspace)
	assert.Equal(t, 3, len(sessions[0].Targets))

	// One session-start plus one fact per result, and the start is always seq 1.
	require.Len(t, fold.Records, 4)
	assert.Equal(t, sessionjournal.KindSessionStart, fold.Records[0].Kind)
	assert.Equal(t, uint64(1), fold.Records[0].Seq)
	assert.Equal(t, sessionjournal.SchemaVersion, fold.Records[0].V)
}

func TestSessionFactHandlerMapsStatusOntoOutcomeAndReplay(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// The whole-struct compare below is only meaningful if the environment cannot contribute a
	// field: a developer running with MAGUS_UNIT set would otherwise fail a test about outcomes.
	t.Setenv(trail.EnvUnit, "")
	root := t.TempDir()

	h := beginSessionJournal(root, []string{"run", "ci"}, "v0")
	require.NotNil(t, h)
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Project: "api", Status: journal.StatusPass, DurMs: 20, Ref: "out1"})
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "test", Project: "api", Status: journal.StatusFail})
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "lint", Project: "web", Status: journal.StatusCached})

	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)
	fold, err := sessionjournal.Read(dir)
	require.NoError(t, err)
	sessions := sessionjournal.Summarize(fold)
	require.Len(t, sessions, 1)

	assert.Equal(t, []sessionjournal.TargetResult{
		{Target: "build", Project: "api", Outcome: sessionjournal.OutcomePass, DurMs: 20, Ref: "out1"},
		{Target: "test", Project: "api", Outcome: sessionjournal.OutcomeFail},
		{Target: "lint", Project: "web", Outcome: sessionjournal.OutcomePass, Replayed: true},
	}, sessions[0].Targets)
}

// Everything that is not a target result must leave no trace, or the session journal
// becomes a second copy of the execution journal.
func TestSessionFactHandlerIgnoresEverythingButResults(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()

	h := beginSessionJournal(root, []string{"run", "build"}, "v0")
	require.NotNil(t, h)
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindOutput, Inv: "inv1", Text: "compiling"})
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindExec, Inv: "inv1", Target: "build", Text: "go build"})
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindStarted, Inv: "inv1"})
	// A result with no invocation id is unattributable, and a result with no target
	// names nothing worth recording.
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Target: "build", Status: journal.StatusPass})
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Status: journal.StatusPass})

	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)
	fold, err := sessionjournal.Read(dir)
	require.NoError(t, err)
	assert.Empty(t, fold.Records, "a run that produced no target result leaves no session")
}

// Two invocations against two worktrees of one repo must land in one store, which is
// what makes `magus activity` a cross-worktree view rather than a per-checkout one.
func TestActivityFoldsSessionsFromEveryWorktree(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	main := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git", "worktrees", "feature"), 0o755))
	linked := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(linked, ".git"),
		[]byte("gitdir: "+filepath.Join(main, ".git", "worktrees", "feature")+"\n"), 0o644))

	mainH := beginSessionJournal(main, []string{"run", "build"}, "v0")
	require.NotNil(t, mainH)
	linkedH := beginSessionJournal(linked, []string{"run", "test"}, "v0")
	require.NotNil(t, linkedH)

	emitJournalEvent(t, mainH, journal.Event{Kind: journal.KindResult, Inv: "invMain", Target: "build", Project: "api", Status: journal.StatusPass})
	emitJournalEvent(t, linkedH, journal.Event{Kind: journal.KindResult, Inv: "invLinked", Target: "test", Project: "api", Status: journal.StatusFail})

	dir, err := sessionjournal.Dir(main)
	require.NoError(t, err)
	fold, err := sessionjournal.Read(dir)
	require.NoError(t, err)

	sessions := sessionjournal.Summarize(fold)
	require.Len(t, sessions, 2, "both worktrees write into one store")
	var ids []string
	for _, s := range sessions {
		ids = append(ids, s.Session)
	}
	slices.Sort(ids)
	assert.Equal(t, []string{"invLinked", "invMain"}, ids)
}

func TestSummarizeTargetsCollapsesRepeats(t *testing.T) {
	t.Parallel()
	got := summarizeTargets([]sessionjournal.TargetResult{
		{Target: "build", Project: "api", Outcome: sessionjournal.OutcomePass},
		{Target: "build", Project: "api", Outcome: sessionjournal.OutcomePass},
		{Target: "build", Project: "web", Outcome: sessionjournal.OutcomeFail},
		{Target: "lint", Project: "web", Outcome: sessionjournal.OutcomePass, Replayed: true},
	})
	assert.Equal(t, "build api (pass), build web (fail), lint web (pass, cached)", got)
}

// A store that cannot be written is the case where silence lies: nothing is recorded,
// and `magus activity` then reports in good faith that no session has run.
func TestSessionFactHandlerWarnsOnceWhenTheStoreIsUnwritable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()

	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	// A regular file where the store directory belongs. It fails the MkdirAll inside
	// Open the way a full disk or a read-only state dir would, without forging a
	// permission bit a test running as root would then ignore.
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o644))

	h := beginSessionJournal(root, []string{"run", "build"}, "v0")
	require.NotNil(t, h)

	logged := captureWarnings(t, func() {
		emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Status: journal.StatusPass})
		emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "test", Status: journal.StatusPass})
	})

	assert.Equal(t, 1, strings.Count(logged, "not recorded in the session journal"),
		"the handler breaks once, so the warning cannot repeat per target")
	assert.Contains(t, logged, dir, "the warning names the store a person has to go look at")
}

// activityUnitCell returns the UNIT cell of the row for session, which is the column the console
// drawer's join is waiting on. Reading it positionally rather than searching the whole listing is
// what makes the "-" case assertable: a dash appears in every unattributed column.
func activityUnitCell(t *testing.T, out, session string) string {
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

func TestActivityRendersTheUnitColumnAttributedAndNot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	global = globalFlags{}
	root := t.TempDir()

	t.Setenv(trail.EnvUnit, "fleet/f3")
	attributed := beginSessionJournal(root, []string{"run", "build"}, "v0")
	require.NotNil(t, attributed)
	emitJournalEvent(t, attributed, journal.Event{Kind: journal.KindResult, Inv: "invUnit", Target: "build", Status: journal.StatusPass})

	// A person at a keyboard carries no unit, and the same store has to render both.
	t.Setenv(trail.EnvUnit, "")
	bare := beginSessionJournal(root, []string{"run", "test"}, "v0")
	require.NotNil(t, bare)
	emitJournalEvent(t, bare, journal.Event{Kind: journal.KindResult, Inv: "invBare", Target: "test", Status: journal.StatusPass})

	out := captureStdout(t, func() {
		require.NoError(t, activityCmd(context.Background(), root, nil))
	})

	assert.Contains(t, out, "UNIT")
	assert.Equal(t, "fleet/f3", activityUnitCell(t, out, "invUnit"))
	assert.Equal(t, "-", activityUnitCell(t, out, "invBare"), "an unattributed session reads like an unknown HOST, not like a blank")
}

func TestSessionFactHandlerStampsTheUnitOnEveryFact(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(trail.EnvUnit, "fleet/f3")
	root := t.TempDir()

	h := beginSessionJournal(root, []string{"run", "ci"}, "v0")
	require.NotNil(t, h)
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Status: journal.StatusPass})
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "test", Status: journal.StatusPass})

	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)
	fold, err := sessionjournal.Read(dir)
	require.NoError(t, err)

	sessions := sessionjournal.Summarize(fold)
	require.Len(t, sessions, 1)
	assert.Equal(t, "fleet/f3", sessions[0].Unit)
	require.Len(t, sessions[0].Targets, 2)
	for _, target := range sessions[0].Targets {
		assert.Equal(t, "fleet/f3", target.Unit, "a fact read on its own still says whose work it was")
	}
}

// A unit that fails the id rule is dropped rather than stamped, which is what keeps the trail's
// redaction exemption honest. The note explaining the drop is asserted in internal/trail, where
// the one-time gate can be reset; here the observable fact is that nothing was attributed.
func TestSessionFactHandlerDropsAnInvalidUnit(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv(trail.EnvUnit, "not a unit id")
	root := t.TempDir()

	h := beginSessionJournal(root, []string{"run", "build"}, "v0")
	require.NotNil(t, h)
	emitJournalEvent(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Status: journal.StatusPass})

	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)
	fold, err := sessionjournal.Read(dir)
	require.NoError(t, err)

	sessions := sessionjournal.Summarize(fold)
	require.Len(t, sessions, 1)
	assert.Empty(t, sessions[0].Unit)
	require.Len(t, sessions[0].Targets, 1)
	assert.Empty(t, sessions[0].Targets[0].Unit)
}

func TestActivityRejectsPositionalArguments(t *testing.T) {
	err := activityCmd(context.Background(), t.TempDir(), []string{"yesterday"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--limit")
}

func TestActivityRejectsANegativeLimit(t *testing.T) {
	err := activityCmd(context.Background(), t.TempDir(), []string{"--limit=-1"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "zero or more")
	assert.Contains(t, err.Error(), "0 lists every session", "the error names the value that means what -1 was reaching for")
}
