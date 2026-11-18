package sessions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egladman/magus/internal/recorder"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/safetyscissors/journal"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type InvocationFactsSuite struct{ suite.Suite }

func TestInvocationFacts(t *testing.T) { suite.Run(t, new(InvocationFactsSuite)) }

// emitJournalEvent hands one result for inv to the sink h.
func emitJournalEvent(t *testing.T, h recorder.Sink, inv string, run journal.Run) {
	t.Helper()
	h(context.Background(), journal.NewRecord(time.Now(), inv, run))
}

// leaseResolver answers every checkout with lease, from and err. Which lease wins is
// job.RunLease's rule and is tested there; the journal stamps what the resolver says.
func leaseResolver(lease string, from job.LeaseSource, err error) func(context.Context, string) (string, job.LeaseSource, error) {
	return func(context.Context, string) (string, job.LeaseSource, error) { return lease, from, err }
}

// withJournalLease wires the handler with a fixed build version and resolve as the lease.
func withJournalLease(ctx context.Context, sinks []recorder.Sink, root, verb string, args []string, resolve func(context.Context, string) (string, job.LeaseSource, error)) []recorder.Sink {
	return WithInvocationFacts(ctx, sinks, root, verb, args, "test", resolve)
}

// withJournal is withJournalLease under no lease.
func withJournal(ctx context.Context, sinks []recorder.Sink, root, verb string, args []string) []recorder.Sink {
	return withJournalLease(ctx, sinks, root, verb, args, leaseResolver("", "", nil))
}

// firstPayload returns the raw payload of the first record of kind, which is what the
// shape assertions compare: the bytes on disk, not a re-decoded copy of them.
func firstPayload(t *testing.T, fold session.Fold, kind string) string {
	t.Helper()
	for _, rec := range fold.Records {
		if rec.Kind == kind {
			return string(rec.Payload)
		}
	}
	t.Fatalf("no %s record in %d records", kind, len(fold.Records))
	return ""
}

func (s *InvocationFactsSuite) TestWithInvocationFactsRecordsAffectedResults() {
	t := s.T()
	testkit.Isolate(t)
	root := t.TempDir()

	sinks := withJournal(context.Background(), nil, root, "affected", []string{"ci", "--base", "main"})
	require.Len(t, sinks, 1)

	emitJournalEvent(t, sinks[0], "inv1", journal.Run{Target: "build", Project: "api", Status: journal.StatusPass, Duration: 20 * time.Millisecond, Ref: "out1"})
	emitJournalEvent(t, sinks[0], "inv1", journal.Run{Target: "test", Project: "api", Status: journal.StatusFail})
	emitJournalEvent(t, sinks[0], "inv1", journal.Run{Target: "lint", Project: "web", Status: journal.StatusCached})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)

	summaries := session.Summarize(fold)
	require.Len(t, summaries, 1)
	assert.Equal(t, "inv1", summaries[0].Invocation, "the journal's invocation id files the facts")
	assert.Empty(t, summaries[0].Session, "a CLI run is in no host session")
	assert.Equal(t, recorder.LocalOrigin(context.Background()).User, summaries[0].User, "the start records the OS account that ran it")
	assert.Equal(t, "affected ci --base main", summaries[0].Command)
	assert.Equal(t, root, summaries[0].Workspace)
	assert.Equal(t, []session.TargetResult{
		{Target: "build", Project: "api", Outcome: session.OutcomePass, DurationMs: 20, Ref: "out1"},
		{Target: "test", Project: "api", Outcome: session.OutcomeFail},
		{Target: "lint", Project: "web", Outcome: session.OutcomePass, Replayed: true},
	}, summaries[0].Targets)

	require.Len(t, fold.Records, 4, "one session-start plus one fact per result")
	first := fold.Records[0]
	assert.Equal(t, session.Record{
		V:          first.V,
		Invocation: "inv1",
		Seq:        1,
		Kind:       session.KindInvocationStart,
		Ts:         first.Ts, // the clock at the start
		Payload:    first.Payload,
	}, first)
}

// `magus session` is a view of the repository, so a fact must not carry a trace of which
// command produced it. Comparing the raw payload bytes is what pins that: a field added
// to one path and not the other would diverge here before anyone noticed in the view.
func (s *InvocationFactsSuite) TestWithInvocationFactsWritesTheSameFactForRunAndAffected() {
	t := s.T()
	testkit.Isolate(t)
	runRoot, affectedRoot := t.TempDir(), t.TempDir()

	result := journal.Run{Target: "ci", Project: "api", Status: journal.StatusCached, Duration: 7 * time.Millisecond, Ref: "out9"}

	runSinks := withJournal(context.Background(), nil, runRoot, "run", []string{"ci", "api"})
	require.Len(t, runSinks, 1)
	emitJournalEvent(t, runSinks[0], "inv1", result)

	affectedSinks := withJournal(context.Background(), nil, affectedRoot, "affected", []string{"ci"})
	require.Len(t, affectedSinks, 1)
	emitJournalEvent(t, affectedSinks[0], "inv1", result)

	runDir, err := Dir(runRoot)
	require.NoError(t, err)
	runFold, err := session.ReadAll(t.Context(), runDir)
	require.NoError(t, err)

	affectedDir, err := Dir(affectedRoot)
	require.NoError(t, err)
	affectedFold, err := session.ReadAll(t.Context(), affectedDir)
	require.NoError(t, err)

	assert.Equal(t,
		firstPayload(t, runFold, session.KindTargetResult),
		firstPayload(t, affectedFold, session.KindTargetResult))

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
func (s *InvocationFactsSuite) TestWithInvocationFactsStampsTheLeaseOnRun() {
	s.leaseStamped("run", "ci", "api")
}

func (s *InvocationFactsSuite) TestWithInvocationFactsStampsTheLeaseOnAffected() {
	s.leaseStamped("affected", "ci")
}

// leaseStamped records one result under verb and a resolved lease, and
// asserts the store stamped that lease on the invocation and its result.
func (s *InvocationFactsSuite) leaseStamped(verb string, args ...string) {
	t := s.T()
	testkit.Isolate(t)
	root := t.TempDir()
	sinks := withJournalLease(context.Background(), nil, root, verb, args, leaseResolver("fleet/f3", job.LeaseSourceEnv, nil))
	s.Require().Len(sinks, 1)
	emitJournalEvent(t, sinks[0], "inv1", journal.Run{Target: "ci", Project: "api", Status: journal.StatusPass})

	dir, err := Dir(root)
	s.Require().NoError(err)
	fold, err := session.ReadAll(t.Context(), dir)
	s.Require().NoError(err)

	summaries := session.Summarize(fold)
	s.Require().Len(summaries, 1)
	s.Equal("fleet/f3", summaries[0].Lease)
	s.Equal([]session.TargetResult{
		{Target: "ci", Project: "api", Outcome: session.OutcomePass, Lease: "fleet/f3", LeaseFrom: job.LeaseSourceEnv},
	}, summaries[0].Targets)
}

// A lease that does not resolve records none and says why, rather than recording a
// claim as if nothing outranked it.
func (s *InvocationFactsSuite) TestWithInvocationFactsRecordsNoLeaseWhenTheBindingDoesNotRead() {
	t := s.T()
	testkit.Isolate(t)
	root := t.TempDir()

	sinks := withJournalLease(context.Background(), nil, root, "run", []string{"ci"},
		leaseResolver("fleet/claimed", job.LeaseSourceEnv, errors.New("marker unreadable")))
	require.Len(t, sinks, 1)
	emitJournalEvent(t, sinks[0], "inv1", journal.Run{Target: "ci", Status: journal.StatusPass})

	dir, err := Dir(root)
	require.NoError(t, err)
	fold, err := session.ReadAll(t.Context(), dir)
	require.NoError(t, err)
	summaries := session.Summarize(fold)
	require.Len(t, summaries, 1)
	assert.Empty(t, summaries[0].Lease)
	assert.Empty(t, summaries[0].LeaseFrom)
}

// An unwritable store must cost the run nothing: journaling is best-effort, and a handler
// that reported an error here would be a build failure caused by bookkeeping.
func (s *InvocationFactsSuite) TestWithInvocationFactsSurvivesAnUnwritableStore() {
	t := s.T()
	testkit.Isolate(t)
	root := t.TempDir()

	// A regular file where the store directory belongs: MkdirAll then fails for a reason
	// no permission bit can be chmod-ed away, which a test running as root would defeat.
	dir, err := Dir(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(dir), 0o755))
	require.NoError(t, os.WriteFile(dir, []byte("blocked\n"), 0o644))

	sinks := withJournal(context.Background(), nil, root, "affected", []string{"ci"})
	require.Len(t, sinks, 1, "the store is only opened on the first fact, so wiring still succeeds")

	// Twice: the first failure marks the handler broken, and the second proves the broken
	// path stays silent rather than retrying its way to an error.
	run := journal.Run{Target: "build", Project: "api", Status: journal.StatusPass}
	emitJournalEvent(t, sinks[0], "inv1", run)
	emitJournalEvent(t, sinks[0], "inv1", run)

	blocked, err := os.ReadFile(dir)
	require.NoError(t, err)
	assert.Equal(t, "blocked\n", string(blocked), "nothing was written where the store could not be created")
}

// With no resolvable state directory there is nowhere to journal, and a machine in that
// state must still be able to run builds.
func (s *InvocationFactsSuite) TestWithInvocationFactsLeavesHandlersAloneWithoutAStateDir() {
	t := s.T()
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("LocalAppData", "")

	existing := []recorder.Sink{func(context.Context, journal.Record) {}}
	got := withJournal(context.Background(), existing, t.TempDir(), "affected", []string{"ci"})
	assert.Len(t, got, 1, "no fact sink joins the ones already there")
}
