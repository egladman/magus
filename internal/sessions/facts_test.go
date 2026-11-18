package sessions

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// factOutcomeSuite pins the axis the push gate and owed checks compare on.
type factOutcomeSuite struct {
	suite.Suite
}

func TestFactOutcome(t *testing.T) {
	suite.Run(t, new(factOutcomeSuite))
}

func (s *factOutcomeSuite) TestAPermitIsGreen() {
	s.Equal(session.OutcomePass, factOutcome(journal.StatusPermitted))
}

func (s *factOutcomeSuite) TestAFailureIsRed() {
	s.Equal(session.OutcomeFail, factOutcome(journal.StatusFail))
}

// emitFact hands one record for inv to the sink h.
func emitFact[B journal.Body](t *testing.T, h func(context.Context, journal.Record), inv string, body B) {
	t.Helper()
	h(context.Background(), journal.NewRecord(time.Now(), inv, body))
}

// captureFactWarnings installs a slog handler for the duration of fn and returns what it
// logged. A handler that abandons the store reports it through slog rather than an error
// (Handle returning one would make bookkeeping able to fail a build), so the default
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
	testkit.Isolate(t)
	root := t.TempDir()

	h := NewFactHandler(root, session.InvocationStart{Workspace: root, Command: "run build", Version: "test-version"})
	require.NotNil(t, h)

	emitFact(t, h, "inv1", journal.Run{Target: "build", Project: "api", Status: journal.StatusPass, Duration: 20 * time.Millisecond, Ref: "out1"})
	emitFact(t, h, "inv1", journal.Run{Target: "test", Project: "api", Status: journal.StatusFail, Duration: 5 * time.Millisecond})
	emitFact(t, h, "inv1", journal.Run{Target: "lint", Project: "web", Status: journal.StatusCached})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)

	summaries := session.Summarize(fold)
	require.Len(t, summaries, 1)
	// The journal's invocation id files the facts. Who ran it, when, and the span the
	// invocation minted are the process's own, read back rather than set here.
	assert.Equal(t, session.Summary{
		Invocation: "inv1",
		Origin:     summaries[0].Origin,
		SpanID:     summaries[0].SpanID,
		Workspace:  root,
		Command:    "run build",
		StartedMs:  summaries[0].StartedMs,
		LastMs:     summaries[0].LastMs,
		Facts:      4,
		Targets: []session.TargetResult{
			{Target: "build", Project: "api", Outcome: session.OutcomePass, DurationMs: 20, Ref: "out1"},
			{Target: "test", Project: "api", Outcome: session.OutcomeFail, DurationMs: 5},
			{Target: "lint", Project: "web", Outcome: session.OutcomePass, Replayed: true},
		},
	}, summaries[0])

	// One session-start plus one fact per result, and the start is always seq 1.
	require.Len(t, fold.Records, 4)
	assert.Equal(t, session.Record{
		V:          session.APIVersion,
		Invocation: "inv1",
		Seq:        1,
		Kind:       session.KindInvocationStart,
		Ts:         fold.Records[0].Ts,
		Payload:    fold.Records[0].Payload,
	}, fold.Records[0])
}

func TestFactHandlerMapsStatusOntoOutcomeAndReplay(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	h := NewFactHandler(root, session.InvocationStart{Workspace: root, Command: "run ci"})
	require.NotNil(t, h)
	emitFact(t, h, "inv1", journal.Run{Target: "build", Project: "api", Status: journal.StatusPass, Duration: 20 * time.Millisecond, Ref: "out1"})
	emitFact(t, h, "inv1", journal.Run{Target: "test", Project: "api", Status: journal.StatusFail})
	emitFact(t, h, "inv1", journal.Run{Target: "lint", Project: "web", Status: journal.StatusCached})
	emitFact(t, h, "inv1", journal.Run{Target: "ci", Project: "api", Status: journal.StatusFail, DiagnosticCode: "MGS3009"})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	summaries := session.Summarize(fold)
	require.Len(t, summaries, 1)

	assert.Equal(t, []session.TargetResult{
		{Target: "build", Project: "api", Outcome: session.OutcomePass, DurationMs: 20, Ref: "out1"},
		{Target: "test", Project: "api", Outcome: session.OutcomeFail},
		{Target: "lint", Project: "web", Outcome: session.OutcomePass, Replayed: true},
		{Target: "ci", Project: "api", Outcome: session.OutcomeFail, DiagnosticCode: "MGS3009"},
	}, summaries[0].Targets)
}

func TestFactHandlerCopiesTheClaimAndCost(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	h := NewFactHandler(root, session.InvocationStart{Workspace: root, Command: "run ci"})
	require.NotNil(t, h)
	emitFact(t, h, "inv1", journal.Run{
		Target: "build", Project: "api", Status: journal.StatusPass,
		Started: time.UnixMilli(1700000000000), Slots: 2, MemoryMB: 512, Sizing: journal.SizingDeclared,
		Waited: 40 * time.Millisecond, MaxRSSBytes: 3 << 20, CPU: 900 * time.Millisecond,
	})
	emitFact(t, h, "inv1", journal.Run{Target: "lint", Project: "web", Status: journal.StatusCached, Sizing: journal.SizingNone})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	summaries := session.Summarize(fold)
	require.Len(t, summaries, 1)

	assert.Equal(t, []session.TargetResult{
		{
			Target: "build", Project: "api", Outcome: session.OutcomePass,
			StartedMs: 1700000000000, Slots: 2, MemoryMB: 512, Sizing: string(journal.SizingDeclared),
			WaitedMs: 40, MaxRSSBytes: 3 << 20, CPUMs: 900,
		},
		{Target: "lint", Project: "web", Outcome: session.OutcomePass, Replayed: true, Sizing: string(journal.SizingNone)},
	}, summaries[0].Targets)
}

// Everything that is not a target result must leave no trace, or the session store
// becomes a second copy of the execution journal.
func TestFactHandlerIgnoresEverythingButResults(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	h := NewFactHandler(root, session.InvocationStart{Workspace: root, Command: "run build"})
	require.NotNil(t, h)
	emitFact(t, h, "inv1", journal.Output{Text: "compiling"})
	emitFact(t, h, "inv1", journal.Exec{Target: "build", Command: "go build"})
	emitFact(t, h, "inv1", journal.Started{})
	// A result with no invocation id is unattributable, and a result with no target
	// names nothing worth recording.
	emitFact(t, h, "", journal.Run{Target: "build", Status: journal.StatusPass})
	emitFact(t, h, "inv1", journal.Run{Status: journal.StatusPass})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	assert.Empty(t, fold.Records, "a run that produced no target result leaves no session")
}

// Two invocations against two worktrees of one repo must land in one store, which is
// what makes `magus session` a cross-worktree view rather than a per-checkout one.
func TestFactHandlerFoldsSessionsFromEveryWorktree(t *testing.T) {
	testkit.Isolate(t)

	main := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(main, ".git", "worktrees", "feature"), 0o755))
	linked := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(linked, ".git"),
		[]byte(fmt.Sprintf("gitdir: %s\n", filepath.Join(main, ".git", "worktrees", "feature"))), 0o644))

	mainH := NewFactHandler(main, session.InvocationStart{Workspace: main, Command: "run build"})
	require.NotNil(t, mainH)
	linkedH := NewFactHandler(linked, session.InvocationStart{Workspace: linked, Command: "run test"})
	require.NotNil(t, linkedH)

	emitFact(t, mainH, "invMain", journal.Run{Target: "build", Project: "api", Status: journal.StatusPass})
	emitFact(t, linkedH, "invLinked", journal.Run{Target: "test", Project: "api", Status: journal.StatusFail})

	dir, err := Dir(main)
	require.NoError(t, err)
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)

	summaries := session.Summarize(fold)
	require.Len(t, summaries, 2, "both worktrees write into one store")
	var ids []string
	for _, s := range summaries {
		ids = append(ids, s.Invocation)
	}
	slices.Sort(ids)
	assert.Equal(t, []string{"invLinked", "invMain"}, ids)
}

// A store that cannot be written is the case where silence lies: nothing is recorded,
// and `magus session` then reports in good faith that no session has run.
func TestFactHandlerWarnsOnceWhenTheStoreIsUnwritable(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	dir, err := Dir(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	// A regular file where the store directory belongs. It fails the MkdirAll inside
	// Open the way a full disk or a read-only state dir would, without forging a
	// permission bit a test running as root would then ignore.
	require.NoError(t, os.WriteFile(dir, []byte("not a directory"), 0o644))

	h := NewFactHandler(root, session.InvocationStart{Workspace: root, Command: "run build"})
	require.NotNil(t, h)

	logged := captureFactWarnings(t, func() {
		emitFact(t, h, "inv1", journal.Run{Target: "build", Status: journal.StatusPass})
		emitFact(t, h, "inv1", journal.Run{Target: "test", Status: journal.StatusPass})
	})

	assert.Equal(t, 1, strings.Count(logged, "not recorded in the session store"),
		"the handler breaks once, so the warning cannot repeat per target")
	assert.Contains(t, logged, dir, "the warning names the store a person has to go look at")
}

// The lease is stamped from the SessionStart the caller supplied, never re-read per fact:
// a mid-run environment change would otherwise split one session's facts across two
// leases, which is a history no producer could have meant.
func TestFactHandlerStampsTheSuppliedLeaseOnEveryFact(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	h := NewFactHandler(root, session.InvocationStart{Workspace: root, Command: "run ci", Lease: "fleet/f3"})
	require.NotNil(t, h)
	emitFact(t, h, "inv1", journal.Run{Target: "build", Status: journal.StatusPass})
	emitFact(t, h, "inv1", journal.Run{Target: "test", Status: journal.StatusPass})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)

	summaries := session.Summarize(fold)
	require.Len(t, summaries, 1)
	assert.Equal(t, "fleet/f3", summaries[0].Lease)
	require.Len(t, summaries[0].Targets, 2)
	for _, target := range summaries[0].Targets {
		assert.Equal(t, "fleet/f3", target.Lease, "a fact read on its own still says whose work it was")
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

	assert.Nil(t, NewFactHandler(t.TempDir(), session.InvocationStart{}))
}
