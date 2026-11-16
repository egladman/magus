package job

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attentionRoot isolates one test's queue: the store is keyed by repository identity under
// the user state dir, so without it a test would file requests into whatever repo the test
// binary happens to be running in. The location is where the checkout's binding is read.
func attentionRoot(t *testing.T) (root string, loc Location) {
	t.Helper()
	testkit.Isolate(t)
	t.Setenv(trail.EnvBaggage, "")
	return t.TempDir(), Location{StateBase: t.TempDir(), CacheDir: t.TempDir()}
}

func blockedEvent(message string) types.Event {
	return types.Event{
		APIVersion: types.EventAPIVersion,
		Outcome:    types.OutcomeWaiting,
		Severity:   types.SeverityWarning,
		Source:     types.EventSource{Kind: "agent", Sub: "claude", ID: "sess-1"},
		Where:      &types.EventLocation{Workspace: types.Path{Value: "/repo", IsDir: true}},
		Message:    message,
	}
}

func fileBlockHeld(t *testing.T, root string, loc Location, ev types.Event) (bool, error) {
	t.Helper()
	place := func(root string) (Location, error) {
		loc.Root = root
		return loc, nil
	}
	return FileAttention(t.Context(), root, ev, job.Origin{EntryPoint: job.EntryPointHook}, "test", place)
}

func fileBlock(t *testing.T, root string, loc Location, ev types.Event) error {
	t.Helper()
	_, err := fileBlockHeld(t, root, loc, ev)
	return err
}

func openRequests(t *testing.T, root string) []sessions.AttentionRequest {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	return sessions.AttentionQueue(fold)
}

// wantRequest is the request a hook files for blockedEvent under the given lease: every field
// the event or the lease decides, with the ones the store mints (id, invocation, clock) taken
// from got.
func wantRequest(got sessions.AttentionRequest, lease string, from job.LeaseSource) sessions.AttentionRequest {
	ev := blockedEvent("needs a decision")
	return sessions.AttentionRequest{
		ID:         got.ID,
		Invocation: got.Invocation,
		OpenedMs:   got.OpenedMs,
		Outcome:    string(ev.Outcome),
		Severity:   string(ev.Severity),
		Source:     "agent/claude",
		Where:      sessions.AttentionWhere(ev.Where),
		Lease:      lease,
		LeaseFrom:  from,
		Message:    ev.Message,
	}
}

func openRequestIDs(t *testing.T, root string) []string {
	t.Helper()
	var ids []string
	for _, req := range openRequests(t, root) {
		ids = append(ids, req.ID)
	}
	return ids
}

// captureWarnings installs a slog handler for the duration of fn and returns what it
// logged. The producers here report a request they could NOT open through slog rather
// than an error (a non-zero exit from an agent hook interrupts the very session the
// notification exists to help), so the default logger is the only place to observe it.
func captureWarnings(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

// Files are the subject, not a second identity. A re-fire that names a different
// path re-uses the id, and the row keeps the paths from the open that created it.
func TestFileAttentionKeepsTheIDWhenTheFilesChange(t *testing.T) {
	root, loc := attentionRoot(t)
	first := blockedEvent("needs a decision")
	first.Where.Files = []types.FileRef{{Value: "cmd/magus/notify.go"}}
	require.NoError(t, fileBlock(t, root, loc, first))
	ids := openRequestIDs(t, root)
	require.Len(t, ids, 1)

	again := blockedEvent("needs a decision")
	again.Where.Files = []types.FileRef{{Value: "some/other.go"}}
	held, err := fileBlockHeld(t, root, loc, again)
	require.NoError(t, err)
	assert.True(t, held)
	assert.Equal(t, ids, openRequestIDs(t, root))

	rows := openRequests(t, root)
	require.Len(t, rows, 1)
	assert.Equal(t, []types.FileRef{{Value: "cmd/magus/notify.go"}}, rows[0].Files, "a re-fire must not replace the subject of the open row")
}

// The lease is attribution, so changing it must not move the row a person is about to dispose of.
// Re-raising the same block under a new lease re-uses the id, and the queue still holds one row.
func TestFileAttentionKeepsTheIDWhenTheLeaseChanges(t *testing.T) {
	root, loc := attentionRoot(t)
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=fleet/f3")
	require.NoError(t, fileBlock(t, root, loc, blockedEvent("needs a decision")))
	first := openRequestIDs(t, root)
	require.Len(t, first, 1)

	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=fleet/f9")
	require.NoError(t, fileBlock(t, root, loc, blockedEvent("needs a decision")))

	assert.Equal(t, first, openRequestIDs(t, root), "a re-partitioned fleet must not re-key an open request")
}

// The store carries the lease and the source that answered it, not just the rendering: the
// console reads these records too. The checkout's binding outranks the BAGGAGE claim, as it
// does for every lease magus resolves.
func TestFileAttentionStoresTheClaimAlone(t *testing.T) {
	root, loc := attentionRoot(t)
	t.Setenv(trail.EnvBaggage, fmt.Sprintf("%s=%s", trail.BaggageLease, "fleet/f3"))
	require.NoError(t, fileBlock(t, root, loc, blockedEvent("needs a decision")))

	open := openRequests(t, root)
	require.Len(t, open, 1)
	assert.Equal(t, wantRequest(open[0], "fleet/f3", job.LeaseSourceEnv), open[0])
}

func TestFileAttentionStoresTheBindingAlone(t *testing.T) {
	root, loc := attentionRoot(t)
	require.NoError(t, NewStore(loc).Bind(Caller{}, "fleet/bound"))
	require.NoError(t, fileBlock(t, root, loc, blockedEvent("needs a decision")))

	open := openRequests(t, root)
	require.Len(t, open, 1)
	assert.Equal(t, wantRequest(open[0], "fleet/bound", job.LeaseSourceMarker), open[0])
}

func TestFileAttentionStoresTheBindingOverADifferentClaim(t *testing.T) {
	root, loc := attentionRoot(t)
	require.NoError(t, NewStore(loc).Bind(Caller{}, "fleet/bound"))
	t.Setenv(trail.EnvBaggage, fmt.Sprintf("%s=%s", trail.BaggageLease, "fleet/f3"))
	require.NoError(t, fileBlock(t, root, loc, blockedEvent("needs a decision")))

	open := openRequests(t, root)
	require.Len(t, open, 1)
	assert.Equal(t, wantRequest(open[0], "fleet/bound", job.LeaseSourceContested), open[0])
}

// An id that fails the rule attributes nothing rather than smuggling free text into a field the
// trail carries unredacted. internal/trail asserts the note that explains the drop.
func TestFileAttentionDropsAnInvalidLease(t *testing.T) {
	root, loc := attentionRoot(t)
	t.Setenv(trail.EnvBaggage, trail.BaggageLease+"=not a lease id")
	require.NoError(t, fileBlock(t, root, loc, blockedEvent("needs a decision")))

	open := openRequests(t, root)
	require.Len(t, open, 1)
	assert.Empty(t, open[0].Lease)
}

func TestFileAttentionOnlyForBlockedOutcomes(t *testing.T) {
	root, loc := attentionRoot(t)

	for _, outcome := range []types.EventOutcome{types.OutcomeFailed, types.OutcomeFinished, types.OutcomeDiagnostic, types.OutcomeUpdate, types.OutcomeOther} {
		ev := blockedEvent("something happened")
		ev.Outcome = outcome
		require.NoError(t, fileBlock(t, root, loc, ev))
	}
	assert.Empty(t, openRequestIDs(t, root), "only a stopped agent queues for a person; news does not")

	permission := blockedEvent("needs approval to push")
	permission.Outcome = types.OutcomePermission
	require.NoError(t, fileBlock(t, root, loc, permission))
	require.NoError(t, fileBlock(t, root, loc, blockedEvent("needs a decision")))
	assert.Len(t, openRequestIDs(t, root), 2)
}

func TestFileAttentionDedupesARefiredBlock(t *testing.T) {
	root, loc := attentionRoot(t)

	for range 3 {
		require.NoError(t, fileBlock(t, root, loc, blockedEvent("needs a decision")))
	}
	assert.Len(t, openRequestIDs(t, root), 1, "a hook that fires on every prompt must not queue one request per prompt")

	// Only the session and the three digested payload fields distinguish requests, so
	// the same block from a second agent session is a second block: a different agent
	// is waiting.
	other := blockedEvent("needs a decision")
	other.Source.ID = "sess-2"
	require.NoError(t, fileBlock(t, root, loc, other))
	assert.Len(t, openRequestIDs(t, root), 2)
}

// The request id keys on the agent session that raised the block. An event with none
// would key every such producer on "", folding unrelated blocks into one row that a
// single dispose closes.
func TestFileAttentionRefusesAnEventWithNoSourceID(t *testing.T) {
	root, loc := attentionRoot(t)

	ev := blockedEvent("needs a decision")
	ev.Source.ID = ""
	logged := captureWarnings(t, func() {
		held, err := fileBlockHeld(t, root, loc, ev)
		require.NoError(t, err, "a missing source.id must not fail the notification")
		assert.False(t, held, "no row holds it, so the notification must still fire")
	})

	assert.Empty(t, openRequestIDs(t, root))
	assert.Contains(t, logged, "source.id", "the note names the field that is missing")
	assert.Contains(t, logged, "wrapper", "and who has to send it")
}

func TestFileAttentionSkipsWhenThereIsNoRepository(t *testing.T) {
	_, loc := attentionRoot(t)
	// An empty root: notify still notifies, and the request has nowhere durable to live
	// rather than landing in a store keyed on "".
	held, err := fileBlockHeld(t, "", loc, blockedEvent("needs a decision"))
	require.NoError(t, err)
	assert.False(t, held)
}

// The SOURCE column and the request id read the same label, so a producer that renders
// one way in the listing cannot key another way in the digest.
func TestFileAttentionLabelsTheProducer(t *testing.T) {
	root, loc := attentionRoot(t)
	ev := blockedEvent("needs a decision")
	require.NoError(t, fileBlock(t, root, loc, ev))

	queue := openRequests(t, root)
	require.Len(t, queue, 1)

	got := queue[0]
	assert.Equal(t, sessions.AttentionRequest{
		ID: sessions.RequestID(ev.Source.ID, sessions.AttentionOpen{
			Source:  sessions.SourceLabel(ev.Source.Kind, ev.Source.Sub),
			Where:   sessions.AttentionWhere(ev.Where),
			Message: ev.Message,
		}),
		Invocation: got.Invocation, // minted by the hook invocation and its clock
		OpenedMs:   got.OpenedMs,
		Outcome:    string(ev.Outcome),
		Severity:   string(ev.Severity),
		Source:     "agent/claude",
		Where:      sessions.AttentionWhere(ev.Where),
		Lease:      got.Lease, // answered by the checkout and the environment
		LeaseFrom:  got.LeaseFrom,
		Message:    ev.Message,
	}, got)
}
