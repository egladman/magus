package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/sessionjournal"
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
	// A developer running with MAGUS_UNIT set would otherwise have it stamped on every request
	// these tests raise. A test that wants one sets it after this call.
	t.Setenv(trail.EnvUnit, "")
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
	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)
	fold, err := sessionjournal.Read(dir)
	require.NoError(t, err)
	var ids []string
	for _, req := range sessionjournal.OpenAttention(fold) {
		ids = append(ids, req.ID)
	}
	return ids
}

func TestAttentionListShowsAnOpenRequest(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision on the schema")))

	out := captureStdout(t, func() {
		require.NoError(t, attentionCmd(context.Background(), root, nil))
	})

	assert.Contains(t, out, "OUTCOME")
	assert.Contains(t, out, "WHERE")
	assert.Contains(t, out, openRequestIDs(t, root)[0])
	assert.Contains(t, out, "waiting")
	assert.Contains(t, out, "agent/claude")
	assert.Contains(t, out, "/repo")
	assert.Contains(t, out, "needs a decision on the schema")
	assert.Contains(t, out, "1 open request(s)")
	assert.Contains(t, out, "magus attention dispose", "the listing names the one command that closes a request")
	assert.Contains(t, out, "Nothing here closes on its own.")
}

func TestAttentionListShowsTheRaisingUnit(t *testing.T) {
	root := attentionTestRoot(t)
	t.Setenv(trail.EnvUnit, "fleet/f3")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision on the schema")))

	out := captureStdout(t, func() {
		require.NoError(t, attentionCmd(context.Background(), root, nil))
	})

	assert.Contains(t, out, "UNIT")
	assert.Contains(t, out, "fleet/f3", "the queue says which slice of the fleet is blocked")
}

func TestAttentionListRendersAnUnattributedRequestWithADash(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	out := captureStdout(t, func() {
		require.NoError(t, attentionCmd(context.Background(), root, nil))
	})

	// ID, AGE, OUTCOME, SOURCE, UNIT, WHERE, MESSAGE...
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

// The unit is attribution, so changing it must not move the row a person is about to dispose of.
// Re-raising the same block under a new unit re-uses the id, and the queue still holds one row.
func TestRecordAttentionOpenKeepsTheIDWhenTheUnitChanges(t *testing.T) {
	root := attentionTestRoot(t)
	t.Setenv(trail.EnvUnit, "fleet/f3")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))
	first := openRequestIDs(t, root)
	require.Len(t, first, 1)

	t.Setenv(trail.EnvUnit, "fleet/f9")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	assert.Equal(t, first, openRequestIDs(t, root), "a re-partitioned fleet must not re-key an open request")
}

// The store carries the unit, not just the rendering: the console reads these records too.
func TestRecordAttentionOpenStoresTheUnit(t *testing.T) {
	root := attentionTestRoot(t)
	t.Setenv(trail.EnvUnit, "fleet/f3")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)
	fold, err := sessionjournal.Read(dir)
	require.NoError(t, err)
	open := sessionjournal.OpenAttention(fold)
	require.Len(t, open, 1)
	assert.Equal(t, "fleet/f3", open[0].Unit)
}

// An id that fails the rule attributes nothing rather than smuggling free text into a field the
// trail carries unredacted. internal/trail asserts the note that explains the drop.
func TestRecordAttentionOpenDropsAnInvalidUnit(t *testing.T) {
	root := attentionTestRoot(t)
	t.Setenv(trail.EnvUnit, "not a unit id")
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))

	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)
	fold, err := sessionjournal.Read(dir)
	require.NoError(t, err)
	open := sessionjournal.OpenAttention(fold)
	require.Len(t, open, 1)
	assert.Empty(t, open[0].Unit)
}

func TestAttentionListEmptyStateNamesTheProducer(t *testing.T) {
	root := attentionTestRoot(t)

	out := captureStdout(t, func() {
		require.NoError(t, attentionCmd(context.Background(), root, []string{"ls"}))
	})

	assert.Contains(t, out, "no open attention requests")
	assert.Contains(t, out, "magus notify", "an empty queue has to say how a request would ever get here")
	assert.Contains(t, out, "waiting or permission")
	assert.Contains(t, out, "magus attention dispose <id>")
}

func TestAttentionListJSONCarriesTheRecordsAndTheStore(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs a decision")))
	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)

	out := captureStdout(t, func() {
		require.NoError(t, attentionCmd(context.Background(), root, []string{"ls", "-o", "json"}))
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
		require.NoError(t, attentionCmd(context.Background(), root, []string{"ls", "-o", "name"}))
	})
	assert.Equal(t, ids[0]+"\n", out)
}

func TestAttentionDisposeUnknownIDNamesTheMechanismAndTheNextStep(t *testing.T) {
	root := attentionTestRoot(t)
	dir, err := sessionjournal.Dir(root)
	require.NoError(t, err)

	err = attentionCmd(context.Background(), root, []string{"dispose", "att-doesnotexist"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "att-doesnotexist")
	assert.Contains(t, err.Error(), dir, "the error names the store it looked in")
	assert.Contains(t, err.Error(), "magus attention", "and the command that lists the ids that do exist")
}

func TestAttentionDisposeClosesOnceAndRefusesASecondTime(t *testing.T) {
	root := attentionTestRoot(t)
	require.NoError(t, recordAttentionOpen(root, blockedEvent("needs approval to push")))
	ids := openRequestIDs(t, root)
	require.Len(t, ids, 1)

	out := captureStdout(t, func() {
		require.NoError(t, attentionCmd(context.Background(), root, []string{"dispose", ids[0], "-note", "pushed it myself"}))
	})
	assert.Contains(t, out, "disposed "+ids[0])
	assert.Contains(t, out, "needs approval to push")
	assert.Contains(t, out, "note: pushed it myself")

	assert.Empty(t, openRequestIDs(t, root), "a disposed request leaves the queue")

	err := attentionCmd(context.Background(), root, []string{"dispose", ids[0]})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already disposed")
	assert.Contains(t, err.Error(), "magus attention")
}

func TestAttentionDisposeNeedsExactlyOneID(t *testing.T) {
	root := attentionTestRoot(t)

	err := attentionCmd(context.Background(), root, []string{"dispose"})
	require.ErrorContains(t, err, "exactly one request id")

	err = attentionCmd(context.Background(), root, []string{"dispose", "att-1", "att-2"})
	require.ErrorContains(t, err, "exactly one request id")
}

func TestAttentionRejectsAnUnknownSubcommand(t *testing.T) {
	root := attentionTestRoot(t)

	err := attentionCmd(context.Background(), root, []string{"resolve", "att-1"})
	require.ErrorContains(t, err, `unknown subcommand "resolve"`)
	require.ErrorContains(t, err, "want ls or dispose")
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

	// Only the four id inputs distinguish requests; the same block from a second agent
	// session is a second block, because a different agent is waiting.
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
		require.NoError(t, attentionCmd(context.Background(), root, nil))
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

func TestAttentionSourceLabelsTheProducer(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "agent/claude", attentionSource(types.EventSource{Kind: "agent", Sub: "claude"}))
	assert.Equal(t, "magus", attentionSource(types.EventSource{Kind: "magus"}))
}
