package trail

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { testkit.Main(m) }

// emitJournalEvent pushes one event through h along the real capture path, rather than
// hand-building the slog.Record. The attribute key that carries an Event is private
// to internal/journal, so a hand-built record would be testing a copy of it.
func emitJournalEvent(t *testing.T, h slog.Handler, e journal.Event) {
	t.Helper()
	journal.Emit(journal.WithLogger(context.Background(), journal.NewLogger(h)), e)
}

// leaseResolver answers every checkout with lease, from and err. Which lease wins is
// job.RunLease's rule and is tested there; the journal stamps what the resolver says.
func leaseResolver(lease string, from types.LeaseSource, err error) func(context.Context, string) (string, types.LeaseSource, error) {
	return func(context.Context, string) (string, types.LeaseSource, error) { return lease, from, err }
}

// withJournalLease wires the handler with a fixed build version and resolve as the lease.
func withJournalLease(ctx context.Context, handlers []slog.Handler, root, verb string, args []string, resolve func(context.Context, string) (string, types.LeaseSource, error)) []slog.Handler {
	return WithInvocationJournal(ctx, handlers, root, verb, args, "test", resolve)
}

// withJournal is withJournalLease under no lease.
func withJournal(ctx context.Context, handlers []slog.Handler, root, verb string, args []string) []slog.Handler {
	return withJournalLease(ctx, handlers, root, verb, args, leaseResolver("", "", nil))
}

// journalRecordSink keeps the slog.Record journal.Emit builds, so a test can replay the
// real encoding into another handler and see what that handler RETURNS. Going through
// journal.NewLogger instead would drop the error: the fan-out swallows child errors by
// contract, which is the very thing under test here.
type journalRecordSink struct{ records []slog.Record }

func (s *journalRecordSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *journalRecordSink) Handle(_ context.Context, r slog.Record) error {
	s.records = append(s.records, r.Clone())
	return nil
}

func (s *journalRecordSink) WithAttrs([]slog.Attr) slog.Handler { return s }
func (s *journalRecordSink) WithGroup(string) slog.Handler      { return s }

// captureJournalRecord returns the capture record for e, as the run path would build it.
func captureJournalRecord(t *testing.T, e journal.Event) slog.Record {
	t.Helper()
	sink := &journalRecordSink{}
	journal.Emit(journal.WithLogger(context.Background(), journal.NewLogger(sink)), e)
	require.Len(t, sink.records, 1)
	return sink.records[0]
}

// firstPayload returns the raw payload of the first record of kind, which is what the
// shape assertions compare: the bytes on disk, not a re-decoded copy of them.
func firstPayload(t *testing.T, fold sessions.Fold, kind string) string {
	t.Helper()
	for _, rec := range fold.Records {
		if rec.Kind == kind {
			return string(rec.Payload)
		}
	}
	t.Fatalf("no %s record in %d records", kind, len(fold.Records))
	return ""
}

func TestWithInvocationJournalRecordsAffectedResults(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	handlers := withJournal(context.Background(), nil, root, "affected", []string{"ci", "--base", "main"})
	require.Len(t, handlers, 1)

	emitJournalEvent(t, handlers[0], journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Project: "api", Status: journal.StatusPass, DurationMs: 20, Ref: "out1"})
	emitJournalEvent(t, handlers[0], journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "test", Project: "api", Status: journal.StatusFail})
	emitJournalEvent(t, handlers[0], journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "lint", Project: "web", Status: journal.StatusCached})

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)

	summaries := sessions.Summarize(fold)
	require.Len(t, summaries, 1)
	assert.Equal(t, "inv1", summaries[0].Invocation, "the journal's invocation id files the facts")
	assert.Empty(t, summaries[0].Session, "a CLI run is in no host session")
	assert.Equal(t, LocalOrigin(context.Background()).User, summaries[0].User, "the start records the OS account that ran it")
	assert.Equal(t, "affected ci --base main", summaries[0].Command)
	assert.Equal(t, root, summaries[0].Workspace)
	assert.Equal(t, []sessions.TargetResult{
		{Target: "build", Project: "api", Outcome: sessions.OutcomePass, DurationMs: 20, Ref: "out1"},
		{Target: "test", Project: "api", Outcome: sessions.OutcomeFail},
		{Target: "lint", Project: "web", Outcome: sessions.OutcomePass, Replayed: true},
	}, summaries[0].Targets)

	require.Len(t, fold.Records, 4, "one session-start plus one fact per result")
	first := fold.Records[0]
	assert.Equal(t, sessions.Record{
		V:          first.V,
		Invocation: "inv1",
		Seq:        1,
		Kind:       sessions.KindInvocationStart,
		Ts:         first.Ts, // the clock at the start
		Payload:    first.Payload,
	}, first)
}

// `magus session` is a view of the repository, so a fact must not carry a trace of which
// command produced it. Comparing the raw payload bytes is what pins that: a field added
// to one path and not the other would diverge here before anyone noticed in the view.
func TestWithInvocationJournalWritesTheSameFactForRunAndAffected(t *testing.T) {
	testkit.Isolate(t)
	runRoot, affectedRoot := t.TempDir(), t.TempDir()

	result := journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "ci", Project: "api", Status: journal.StatusCached, DurationMs: 7, Ref: "out9"}

	runHandlers := withJournal(context.Background(), nil, runRoot, "run", []string{"ci", "api"})
	require.Len(t, runHandlers, 1)
	emitJournalEvent(t, runHandlers[0], result)

	affectedHandlers := withJournal(context.Background(), nil, affectedRoot, "affected", []string{"ci"})
	require.Len(t, affectedHandlers, 1)
	emitJournalEvent(t, affectedHandlers[0], result)

	runDir, err := sessions.Dir(runRoot)
	require.NoError(t, err)
	runFold, err := sessions.ReadAll(runDir)
	require.NoError(t, err)

	affectedDir, err := sessions.Dir(affectedRoot)
	require.NoError(t, err)
	affectedFold, err := sessions.ReadAll(affectedDir)
	require.NoError(t, err)

	assert.Equal(t,
		firstPayload(t, runFold, sessions.KindTargetResult),
		firstPayload(t, affectedFold, sessions.KindTargetResult))

	// The two stores must also agree on the record envelope; only the recorded command
	// line, which names the workspace and the verb, is allowed to differ.
	require.Len(t, affectedFold.Records, len(runFold.Records))
	for i := range runFold.Records {
		assert.Equal(t, runFold.Records[i].Kind, affectedFold.Records[i].Kind)
		assert.Equal(t, runFold.Records[i].Seq, affectedFold.Records[i].Seq)
		assert.Equal(t, runFold.Records[i].V, affectedFold.Records[i].V)
	}
}

// The lease is stamped in the shared wiring, not per command, for the same reason the fact shape
// is: `magus session` is a view of the repository, and a lease that only `run` recorded would
// read as a fleet that stopped working the moment it ran `affected`.
func TestWithInvocationJournalStampsTheLeaseOnEveryVerb(t *testing.T) {
	testkit.Isolate(t)

	for verb, args := range map[string][]string{
		"run":      {"ci", "api"},
		"affected": {"ci"},
	} {
		t.Run(verb, func(t *testing.T) {
			root := t.TempDir()
			handlers := withJournalLease(context.Background(), nil, root, verb, args, leaseResolver("fleet/f3", types.LeaseSourceEnv, nil))
			require.Len(t, handlers, 1)
			emitJournalEvent(t, handlers[0], journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "ci", Project: "api", Status: journal.StatusPass})

			dir, err := sessions.Dir(root)
			require.NoError(t, err)
			fold, err := sessions.ReadAll(dir)
			require.NoError(t, err)

			summaries := sessions.Summarize(fold)
			require.Len(t, summaries, 1)
			assert.Equal(t, "fleet/f3", summaries[0].Lease)
			assert.Equal(t, []sessions.TargetResult{
				{Target: "ci", Project: "api", Outcome: sessions.OutcomePass, Lease: "fleet/f3", LeaseFrom: types.LeaseSourceEnv},
			}, summaries[0].Targets)
		})
	}
}

// A lease that does not resolve records none and says why, rather than recording a
// claim as if nothing outranked it.
func TestWithInvocationJournalRecordsNoLeaseWhenTheBindingDoesNotRead(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	handlers := withJournalLease(context.Background(), nil, root, "run", []string{"ci"},
		leaseResolver("fleet/claimed", types.LeaseSourceEnv, errors.New("marker unreadable")))
	require.Len(t, handlers, 1)
	emitJournalEvent(t, handlers[0], journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "ci", Status: journal.StatusPass})

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	summaries := sessions.Summarize(fold)
	require.Len(t, summaries, 1)
	assert.Empty(t, summaries[0].Lease)
	assert.Empty(t, summaries[0].LeaseFrom)
}

// An unwritable store must cost the run nothing: journaling is best-effort, and a handler
// that reported an error here would be a build failure caused by bookkeeping.
func TestWithInvocationJournalSurvivesAnUnwritableStore(t *testing.T) {
	testkit.Isolate(t)
	root := t.TempDir()

	// A regular file where the store directory belongs: MkdirAll then fails for a reason
	// no permission bit can be chmod-ed away, which a test running as root would defeat.
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	require.NoError(t, os.WriteFile(dir, []byte("blocked\n"), 0o644))

	handlers := withJournal(context.Background(), nil, root, "affected", []string{"ci"})
	require.Len(t, handlers, 1, "the store is only opened on the first fact, so wiring still succeeds")

	rec := captureJournalRecord(t, journal.Event{Kind: journal.KindResult, Inv: "inv1", Target: "build", Project: "api", Status: journal.StatusPass})
	// Twice: the first failure marks the handler broken, and the second proves the broken
	// path stays silent rather than retrying its way to an error.
	require.NoError(t, handlers[0].Handle(context.Background(), rec))
	require.NoError(t, handlers[0].Handle(context.Background(), rec))

	blocked, err := os.ReadFile(dir)
	require.NoError(t, err)
	assert.Equal(t, "blocked\n", string(blocked), "nothing was written where the store could not be created")
}

// With no resolvable state directory there is nowhere to journal, and a machine in that
// state must still be able to run builds.
func TestWithInvocationJournalLeavesHandlersAloneWithoutAStateDir(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("LocalAppData", "")

	existing := []slog.Handler{&journalRecordSink{}}
	got := withJournal(context.Background(), existing, t.TempDir(), "affected", []string{"ci"})
	assert.Equal(t, existing, got)
}
