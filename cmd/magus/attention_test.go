package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attentionTestRoot isolates one test's queue: the store is keyed by repository
// identity under the user state dir, so without both of these a test would file
// requests into whatever repo the test binary happens to be running in.
func attentionTestRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// A developer running with a delegation in BAGGAGE would otherwise have it stamped on every request
	// these tests raise. A test that wants one sets it after this call.
	t.Setenv(trail.EnvBaggage, "")
	global = globalFlags{}
	return t.TempDir()
}

func blockedEvent(message string) types.Event {
	return types.Event{
		SchemaVersion: types.EventSchemaVersion,
		Outcome:       types.OutcomeWaiting,
		Severity:      types.SeverityWarning,
		Source:        types.EventSource{Kind: "agent", Sub: "claude", ID: "sess-1"},
		Where:         &types.EventLocation{Workspace: types.Path{Value: "/repo", IsDir: true}},
		Message:       message,
	}
}

// captureWarnings installs a slog handler for the duration of fn and returns what it
// logged. The producers here report a request they could NOT open through slog rather
// than an error - a non-zero exit from an agent hook interrupts the very session the
// notification exists to help - so the default logger is the only place to observe it.
func captureWarnings(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	fn()
	return buf.String()
}

func openRequestIDs(t *testing.T, root string) []string {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	var ids []string
	for _, req := range sessions.AttentionQueue(fold) {
		ids = append(ids, req.ID)
	}
	return ids
}

func TestAttentionListShowsAnOpenRequest(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision on the schema")))

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"attention"}))
	})

	assert.Contains(t, out, "OUTCOME")
	assert.Contains(t, out, "WHERE")
	assert.Contains(t, out, openRequestIDs(t, root)[0])
	assert.Contains(t, out, "waiting")
	assert.Contains(t, out, "agent/claude")
	assert.Contains(t, out, "/repo")
	assert.Contains(t, out, "needs a decision on the schema")
	assert.Contains(t, out, "1 open request(s)")
	assert.Contains(t, out, "magus session dispose", "the listing names the one command that closes a request")
	assert.Contains(t, out, "Nothing here closes on its own.")
}

func TestAttentionListShowsTheRaisingDelegation(t *testing.T) {
	root := attentionTestRoot(t)
	t.Setenv(trail.EnvBaggage, trail.BaggageDelegation+"=fleet/f3")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision on the schema")))

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"attention"}))
	})

	assert.Contains(t, out, "DELEGATION")
	assert.Contains(t, out, "fleet/f3", "the queue says which slice of the fleet is blocked")
}

func TestAttentionListRendersAnUnattributedRequestWithADash(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"attention"}))
	})

	// ID, AGE, OUTCOME, SOURCE, DELEGATION, WHERE, MESSAGE...
	var cells []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "att-") {
			cells = strings.Fields(line)
			break
		}
	}
	require.Greater(t, len(cells), 4, "no request row in:\n%s", out)
	assert.Equal(t, "-", cells[4], "an unattributed request reads like an unknown SOURCE, not like a blank")
}

// The delegation is attribution, so changing it must not move the row a person is about to dispose of.
// Re-raising the same block under a new delegation re-uses the id, and the queue still holds one row.
func TestRecordAttentionOpenKeepsTheIDWhenTheDelegationChanges(t *testing.T) {
	root := attentionTestRoot(t)
	t.Setenv(trail.EnvBaggage, trail.BaggageDelegation+"=fleet/f3")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))
	first := openRequestIDs(t, root)
	require.Len(t, first, 1)

	t.Setenv(trail.EnvBaggage, trail.BaggageDelegation+"=fleet/f9")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	assert.Equal(t, first, openRequestIDs(t, root), "a re-partitioned fleet must not re-key an open request")
}

// The store carries the delegation, not just the rendering: the console reads these records too.
func TestRecordAttentionOpenStoresTheDelegation(t *testing.T) {
	root := attentionTestRoot(t)
	t.Setenv(trail.EnvBaggage, trail.BaggageDelegation+"=fleet/f3")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	open := sessions.AttentionQueue(fold)
	require.Len(t, open, 1)
	assert.Equal(t, "fleet/f3", open[0].Delegation)
}

// An id that fails the rule attributes nothing rather than smuggling free text into a field the
// trail carries unredacted. internal/trail asserts the note that explains the drop.
func TestRecordAttentionOpenDropsAnInvalidDelegation(t *testing.T) {
	root := attentionTestRoot(t)
	t.Setenv(trail.EnvBaggage, trail.BaggageDelegation+"=not a delegation id")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	open := sessions.AttentionQueue(fold)
	require.Len(t, open, 1)
	assert.Empty(t, open[0].Delegation)
}

func TestAttentionListEmptyStateNamesTheProducer(t *testing.T) {
	root := attentionTestRoot(t)

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"attention"}))
	})

	assert.Contains(t, out, "no open attention requests")
	assert.Contains(t, out, "magus session notify", "an empty queue has to say how a request would ever get here")
	assert.Contains(t, out, "waiting or permission")
	assert.Contains(t, out, "magus session dispose <id>")
}

func TestAttentionListJSONCarriesTheRecordsAndTheStore(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))
	dir, err := sessions.Dir(root)
	require.NoError(t, err)

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"attention", "-o", "json"}))
	})

	assert.Contains(t, out, `"requests"`)
	assert.Contains(t, out, `"outcome": "waiting"`)
	assert.Contains(t, out, `"source": "agent/claude"`)
	assert.Contains(t, out, `"disposed": false`)
	assert.Contains(t, out, dir, "the store path is part of the answer: it is what makes the queue's scope checkable")
}

func TestAttentionListNameFormatPrintsIDsOnly(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))
	ids := openRequestIDs(t, root)
	require.Len(t, ids, 1)

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"attention", "-o", "name"}))
	})
	assert.Equal(t, ids[0]+"\n", out)
}

func TestAttentionDisposeUnknownIDNamesTheMechanismAndTheNextStep(t *testing.T) {
	root := attentionTestRoot(t)
	dir, err := sessions.Dir(root)
	require.NoError(t, err)

	err = sessionCmd(context.Background(), root, []string{"dispose", "att-doesnotexist"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "att-doesnotexist")
	assert.Contains(t, err.Error(), dir, "the error names the store it looked in")
	assert.Contains(t, err.Error(), "magus session attention", "and the command that lists the ids that do exist")
}

func TestAttentionDisposeClosesOnceAndRefusesASecondTime(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs approval to push")))
	ids := openRequestIDs(t, root)
	require.Len(t, ids, 1)

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"dispose", ids[0], "-reason", "pushed it myself"}))
	})
	assert.Contains(t, out, "disposed "+ids[0])
	assert.Contains(t, out, "needs approval to push")
	assert.Contains(t, out, "reason: pushed it myself")

	assert.Empty(t, openRequestIDs(t, root), "a disposed request leaves the queue")

	err := sessionCmd(context.Background(), root, []string{"dispose", ids[0]})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already disposed")
	assert.Contains(t, err.Error(), "magus session attention")
}

func TestAttentionDisposeNeedsExactlyOneID(t *testing.T) {
	root := attentionTestRoot(t)

	err := sessionCmd(context.Background(), root, []string{"dispose"})
	require.ErrorContains(t, err, "exactly one request id")

	err = sessionCmd(context.Background(), root, []string{"dispose", "att-1", "att-2"})
	require.ErrorContains(t, err, "exactly one request id")
}

func TestAttentionRejectsAnUnknownSubcommand(t *testing.T) {
	root := attentionTestRoot(t)

	err := sessionCmd(context.Background(), root, []string{"resolve", "att-1"})
	require.ErrorContains(t, err, `unknown subcommand "resolve"`)
	require.ErrorContains(t, err, "want ls, attention, dispose, hook, or notify")
}

func TestRecordAttentionOpenOnlyForBlockedOutcomes(t *testing.T) {
	root := attentionTestRoot(t)

	for _, outcome := range []types.EventOutcome{types.OutcomeFailed, types.OutcomeFinished, types.OutcomeDiagnostic, types.OutcomeUpdate, types.OutcomeOther} {
		ev := blockedEvent("something happened")
		ev.Outcome = outcome
		require.NoError(t, recordAttentionOpen(root, ev))
	}
	assert.Empty(t, openRequestIDs(t, root), "only a stopped agent queues for a person; news does not")

	permission := blockedEvent("needs approval to push")
	permission.Outcome = types.OutcomePermission
	require.NoError(t, recordAttentionOpen(root, permission))
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))
	assert.Len(t, openRequestIDs(t, root), 2)
}

func TestRecordAttentionOpenDedupesARefiredBlock(t *testing.T) {
	root := attentionTestRoot(t)

	for range 3 {
		require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))
	}
	assert.Len(t, openRequestIDs(t, root), 1, "a hook that fires on every prompt must not queue one request per prompt")

	// Only the session and the three digested payload fields distinguish requests, so
	// the same block from a second agent session is a second block: a different agent
	// is waiting.
	other := blockedEvent("needs a decision")
	other.Source.ID = "sess-2"
	require.NoError(t, recordAttentionOpen(root, other))
	assert.Len(t, openRequestIDs(t, root), 2)
}

// The request id keys on the agent session that raised the block. An event with none
// would key every such producer on "", folding unrelated blocks into one row that a
// single dispose closes.
func TestRecordAttentionOpenRefusesAnEventWithNoSourceID(t *testing.T) {
	root := attentionTestRoot(t)

	ev := blockedEvent("needs a decision")
	ev.Source.ID = ""
	logged := captureWarnings(t, func() {
		assert.NoError(t, recordAttentionOpen(root, ev), "a missing source.id must not fail the notification")
	})

	assert.Empty(t, openRequestIDs(t, root))
	assert.Contains(t, logged, "source.id", "the note names the field that is missing")
	assert.Contains(t, logged, "wrapper", "and who has to send it")
}

func TestRecordAttentionOpenSkipsWhenThereIsNoRepository(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	// An empty root with no discoverable workspace: notify still notifies, and the
	// request has nowhere durable to live rather than landing in a store keyed on "".
	t.Chdir(t.TempDir())
	assert.NoError(t, recordAttentionOpen("", blockedEvent("needs a decision")))
}

func TestNotifyOpensARequestThatAttentionLists(t *testing.T) {
	root := attentionTestRoot(t)

	var out bytes.Buffer
	require.NoError(t, notifyCmd(context.Background(), root,
		strings.NewReader(`{"outcome":"permission","source":{"kind":"agent","sub":"claude","id":"sess-9"},"message":"needs approval to push"}`),
		&out, []string{"-o", "name"}))
	assert.Equal(t, "permission\n", out.String())

	global = globalFlags{}
	listed := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"attention"}))
	})
	assert.Contains(t, listed, "needs approval to push")
	assert.Contains(t, listed, "permission")
	assert.Contains(t, listed, "agent/claude")
}

func TestAttentionWhereNarrowsToTheProject(t *testing.T) {
	t.Parallel()

	assert.Empty(t, attentionWhere(nil))
	assert.Equal(t, "/repo", attentionWhere(&types.EventLocation{Workspace: types.Path{Value: "/repo", IsDir: true}}))
	assert.Equal(t, "/repo", attentionWhere(&types.EventLocation{
		Workspace: types.Path{Value: "/repo", IsDir: true},
		Project:   &types.ProjectRef{Path: ".", Name: "repo"},
	}))
	assert.Equal(t, "/repo [apps/web]", attentionWhere(&types.EventLocation{
		Workspace: types.Path{Value: "/repo", IsDir: true},
		Project:   &types.ProjectRef{Path: "apps/web", Name: "apps/web"},
	}))
}

// The SOURCE column and the request id read the same label, so a producer that renders
// one way in the listing cannot key another way in the digest.
func TestAttentionSourceLabelsTheProducer(t *testing.T) {
	root := attentionTestRoot(t)
	ev := blockedEvent("needs a decision")
	require.NoError(t, recordAttentionOpen(root, ev))

	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	fold, err := sessions.ReadAll(dir)
	require.NoError(t, err)
	queue := sessions.AttentionQueue(fold)
	require.Len(t, queue, 1)

	assert.Equal(t, "agent/claude", queue[0].Source)
	assert.Equal(t, sessions.RequestID(ev.Source.ID, sessions.AttentionOpen{
		Source:  sessions.SourceLabel(ev.Source.Kind, ev.Source.Sub),
		Where:   attentionWhere(ev.Where),
		Message: ev.Message,
	}), queue[0].ID)
}

// -q asks a question rather than printing an answer, so the exit status IS the answer:
// an empty queue is the good state and must not read as a fault.
func TestAttentionQuietAnswersWithTheExitStatus(t *testing.T) {
	root := attentionTestRoot(t)

	global.quiet = true
	out := captureStdout(t, func() {
		err := sessionCmd(context.Background(), root, []string{"attention"})
		var silent errSilent
		require.ErrorAs(t, err, &silent)
		assert.Equal(t, 1, silent.exitCode, "nothing is open")
	})
	assert.Empty(t, out, "-q prints nothing")

	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	global.quiet = true
	out = captureStdout(t, func() {
		assert.NoError(t, sessionCmd(context.Background(), root, []string{"attention"}), "a request is open")
	})
	assert.Empty(t, out)
}

func TestAttentionDisposeAcceptsAnUnambiguousPrefix(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))
	id := openRequestIDs(t, root)[0]

	out := captureStdout(t, func() {
		require.NoError(t, sessionCmd(context.Background(), root, []string{"dispose", id[:8]}))
	})
	assert.Contains(t, out, "disposed "+id, "the confirmation names the full id, not the prefix that was typed")
	assert.Empty(t, openRequestIDs(t, root))
}

func TestAttentionDisposeRefusesAnAmbiguousPrefixAndNamesTheCandidates(t *testing.T) {
	root := attentionTestRoot(t)
	first := blockedEvent("one")
	second := blockedEvent("two")
	second.Source.ID = "sess-2"
	require.NoError(t, recordAttentionOpen(root, first))
	require.NoError(t, recordAttentionOpen(root, second))
	ids := openRequestIDs(t, root)
	require.Len(t, ids, 2)

	err := sessionCmd(context.Background(), root, []string{"dispose", "att-"})
	require.Error(t, err)
	for _, id := range ids {
		assert.Contains(t, err.Error(), id, "a person cannot choose between candidates the error does not name")
	}
	assert.Len(t, openRequestIDs(t, root), 2, "an ambiguous prefix closes nothing")
}

func TestAttentionDisposeReportsAPrefixThatMatchesNothing(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	err := sessionCmd(context.Background(), root, []string{"dispose", "att-zzzz"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no request matches")
	assert.Contains(t, err.Error(), "magus session attention", "the refusal names how to see the open ids")
}
