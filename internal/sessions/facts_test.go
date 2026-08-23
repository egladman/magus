package sessions

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// emitFact pushes one event through h along the real capture path, rather than
// hand-building the slog.Record. The attribute key that carries an Event is private to
// internal/journal, so a hand-built record would be testing a copy of it.
func emitFact(t *testing.T, h slog.Handler, e journal.Event) {
	t.Helper()
	journal.Emit(journal.WithLogger(context.Background(), journal.NewLogger(h)), e)
}

// captureFactWarnings installs a slog handler for the duration of fn and returns what it
// logged. A handler that abandons the store reports it through slog rather than an error
// - Handle returning one would make bookkeeping able to fail a build - so the default
// logger is the only place to observe it.
func captureFactWarnings(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

func TestFactHandlerRecordsOneFactPerTargetResult(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()

	h := NewFactHandler(root, SessionStart{Workspace: root, Command: "run build", Version: "test-version"})
	require.NotNil(t, h)

	emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Project: "api", Status: journal.StatusPass, DurMs: 20, Ref: "out1"})
	emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "test", Project: "api", Status: journal.StatusFail, DurMs: 5})
	emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "lint", Project: "web", Status: journal.StatusCached})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := ReadAll(dir)
	require.NoError(t, err)

	summaries := Summarize(fold)
	require.Len(t, summaries, 1)
	assert.Equal(t, "inv1", summaries[0].Session, "the invocation id is the session id")
	assert.Equal(t, "run build", summaries[0].Command)
	assert.Equal(t, root, summaries[0].Workspace)

	// One session-start plus one fact per result, and the start is always seq 1.
	require.Len(t, fold.Records, 4)
	assert.Equal(t, KindSessionStart, fold.Records[0].Kind)
	assert.Equal(t, uint64(1), fold.Records[0].Seq)
	assert.Equal(t, SchemaVersion, fold.Records[0].V)
}

func TestFactHandlerMapsStatusOntoOutcomeAndReplay(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()

	h := NewFactHandler(root, SessionStart{Workspace: root, Command: "run ci"})
	require.NotNil(t, h)
	emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Project: "api", Status: journal.StatusPass, DurMs: 20, Ref: "out1"})
	emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "test", Project: "api", Status: journal.StatusFail})
	emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "lint", Project: "web", Status: journal.StatusCached})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := ReadAll(dir)
	require.NoError(t, err)
	summaries := Summarize(fold)
	require.Len(t, summaries, 1)

	assert.Equal(t, []TargetResult{
		{Target: "build", Project: "api", Outcome: OutcomePass, DurMs: 20, Ref: "out1"},
		{Target: "test", Project: "api", Outcome: OutcomeFail},
		{Target: "lint", Project: "web", Outcome: OutcomePass, Replayed: true},
	}, summaries[0].Targets)
}

// Everything that is not a target result must leave no trace, or the session store
// becomes a second copy of the execution journal.
func TestFactHandlerIgnoresEverythingButResults(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()

	h := NewFactHandler(root, SessionStart{Workspace: root, Command: "run build"})
	require.NotNil(t, h)
	emitFact(t, h, journal.Event{Kind: journal.KindOutput, Inv: "inv1", Text: "compiling"})
	emitFact(t, h, journal.Event{Kind: journal.KindExec, Inv: "inv1", Target: "build", Text: "go build"})
	emitFact(t, h, journal.Event{Kind: journal.KindStarted, Inv: "inv1"})
	// A result with no invocation id is unattributable, and a result with no target
	// names nothing worth recording.
	emitFact(t, h, journal.Event{Kind: journal.KindResult, Target: "build", Status: journal.StatusPass})
	emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Status: journal.StatusPass})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := ReadAll(dir)
	require.NoError(t, err)
	assert.Empty(t, fold.Records, "a run that produced no target result leaves no session")
}

// Two invocations against two worktrees of one repo must land in one store, which is
// what makes `magus sessions` a cross-worktree view rather than a per-checkout one.
func TestFactHandlerFoldsSessionsFromEveryWorktree(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	main := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git", "worktrees", "feature"), 0o755))
	linked := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(linked, ".git"),
		[]byte("gitdir: "+filepath.Join(main, ".git", "worktrees", "feature")+"\n"), 0o644))

	mainH := NewFactHandler(main, SessionStart{Workspace: main, Command: "run build"})
	require.NotNil(t, mainH)
	linkedH := NewFactHandler(linked, SessionStart{Workspace: linked, Command: "run test"})
	require.NotNil(t, linkedH)

	emitFact(t, mainH, journal.Event{Kind: journal.KindResult, Inv: "invMain", Target: "build", Project: "api", Status: journal.StatusPass})
	emitFact(t, linkedH, journal.Event{Kind: journal.KindResult, Inv: "invLinked", Target: "test", Project: "api", Status: journal.StatusFail})

	dir, err := Dir(main)
	require.NoError(t, err)
	fold, err := ReadAll(dir)
	require.NoError(t, err)

	summaries := Summarize(fold)
	require.Len(t, summaries, 2, "both worktrees write into one store")
	var ids []string
	for _, s := range summaries {
		ids = append(ids, s.Session)
	}
	slices.Sort(ids)
	assert.Equal(t, []string{"invLinked", "invMain"}, ids)
}

// A store that cannot be written is the case where silence lies: nothing is recorded,
// and `magus sessions` then reports in good faith that no session has run.
func TestFactHandlerWarnsOnceWhenTheStoreIsUnwritable(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()

	dir, err := Dir(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	// A regular file where the store directory belongs. It fails the MkdirAll inside
	// Open the way a full disk or a read-only state dir would, without forging a
	// permission bit a test running as root would then ignore.
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o644))

	h := NewFactHandler(root, SessionStart{Workspace: root, Command: "run build"})
	require.NotNil(t, h)

	logged := captureFactWarnings(t, func() {
		emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Status: journal.StatusPass})
		emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "test", Status: journal.StatusPass})
	})

	assert.Equal(t, 1, strings.Count(logged, "not recorded in the session store"),
		"the handler breaks once, so the warning cannot repeat per target")
	assert.Contains(t, logged, dir, "the warning names the store a person has to go look at")
}

// The delegation is stamped from the SessionStart the caller supplied, never re-read per fact:
// a mid-run environment change would otherwise split one session's facts across two
// delegations, which is a history no producer could have meant.
func TestFactHandlerStampsTheSuppliedDelegationOnEveryFact(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	root := t.TempDir()

	h := NewFactHandler(root, SessionStart{Workspace: root, Command: "run ci", Delegation: "fleet/f3"})
	require.NotNil(t, h)
	emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Status: journal.StatusPass})
	emitFact(t, h, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "test", Status: journal.StatusPass})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := ReadAll(dir)
	require.NoError(t, err)

	summaries := Summarize(fold)
	require.Len(t, summaries, 1)
	assert.Equal(t, "fleet/f3", summaries[0].Delegation)
	require.Len(t, summaries[0].Targets, 2)
	for _, target := range summaries[0].Targets {
		assert.Equal(t, "fleet/f3", target.Delegation, "a fact read on its own still says whose work it was")
	}
}

// With no resolvable state directory there is nowhere to journal, and a machine in that
// state must still be able to run builds. The nil is an untyped one, so a caller may
// compare it against nil rather than reaching for reflection.
func TestNewFactHandlerIsNilWithoutAStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("LocalAppData", "")

	assert.Nil(t, NewFactHandler(t.TempDir(), SessionStart{}))
}
