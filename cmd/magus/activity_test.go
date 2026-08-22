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
