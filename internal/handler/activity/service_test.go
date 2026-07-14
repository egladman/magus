package activity

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/trail"
	activityv1 "github.com/egladman/magus/proto/gen/go/magus/activity/v1"
)

func seedTrail(t *testing.T) (dir, respRef string) {
	t.Helper()
	dir = t.TempDir()
	respRef, _ = trail.WriteBlob(dir, "mcp", []byte("the result body"))
	trail.Append(dir, trail.Event{
		Ts: 1, Kind: trail.KindMCPToolCall, Actor: "claude",
		Action: "magus_query", Outcome: trail.OutcomeOK,
		ResponseRef: respRef, Preview: "the result body", DurMs: 12,
	})
	trail.Append(dir, trail.Event{
		Ts: 2, Kind: trail.KindTokenLifecycle, Actor: "cli",
		Action: "connector.create", Outcome: trail.OutcomeOK,
	})
	trail.Append(dir, trail.Event{
		Ts: 3, Kind: trail.KindJob, Actor: "daemon", Workspace: "/ws/a",
		Action: "graph build", Outcome: trail.OutcomeError, Error: "boom", DurMs: 40,
	})
	return dir, respRef
}

func TestListActivity_MapsAndOrdersNewestFirst(t *testing.T) {
	dir, _ := seedTrail(t)
	resp, err := NewService(dir).ListActivity(context.Background(),
		connect.NewRequest(&activityv1.ListActivityRequest{}))
	require.NoError(t, err)

	events := resp.Msg.GetEvents()
	require.Len(t, events, 3)
	// newest first: the KIND_JOB event carries its workspace and error outcome
	assert.Equal(t, "graph build", events[0].GetAction())
	assert.Equal(t, activityv1.Kind_KIND_JOB, events[0].GetKind())
	assert.Equal(t, "/ws/a", events[0].GetWorkspace())
	assert.Equal(t, activityv1.Outcome_OUTCOME_ERROR, events[0].GetOutcome())
	assert.Equal(t, "boom", events[0].GetError())
	assert.Equal(t, "connector.create", events[1].GetAction())
	assert.Equal(t, activityv1.Kind_KIND_TOKEN_LIFECYCLE, events[1].GetKind())
	assert.Empty(t, events[1].GetWorkspace()) // a non-workspace action leaves it empty
	assert.Equal(t, activityv1.Kind_KIND_MCP_TOOL_CALL, events[2].GetKind())
	assert.Equal(t, activityv1.Outcome_OUTCOME_OK, events[2].GetOutcome())
	assert.Equal(t, "magus_query", events[2].GetAction())
	require.NotNil(t, events[2].GetDuration())
}

func TestListActivity_FilterByKind(t *testing.T) {
	dir, _ := seedTrail(t)
	resp, err := NewService(dir).ListActivity(context.Background(),
		connect.NewRequest(&activityv1.ListActivityRequest{
			Filter: &activityv1.ActivityQuery{Kinds: []activityv1.Kind{activityv1.Kind_KIND_MCP_TOOL_CALL}},
		}))
	require.NoError(t, err)
	events := resp.Msg.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "magus_query", events[0].GetAction())
}

func TestGetPayload_RoundTripAndReject(t *testing.T) {
	dir, ref := seedTrail(t)
	svc := NewService(dir)

	pr, err := svc.GetPayload(context.Background(),
		connect.NewRequest(&activityv1.GetPayloadRequest{Ref: ref}))
	require.NoError(t, err)
	assert.Equal(t, "the result body", string(pr.Msg.GetBody()))
	assert.Equal(t, int64(len("the result body")), pr.Msg.GetBytes())

	_, err = svc.GetPayload(context.Background(),
		connect.NewRequest(&activityv1.GetPayloadRequest{Ref: "mcpdeadbeef"}))
	require.Error(t, err) // unknown/short ref
}
