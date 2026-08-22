package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/sessionjournal"
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
