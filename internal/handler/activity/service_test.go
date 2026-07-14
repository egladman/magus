package activity

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	activitystore "github.com/egladman/magus/internal/activity"
	activityv1 "github.com/egladman/magus/proto/gen/go/magus/activity/v1"
)

func seedTrail(t *testing.T) (dir, respRef string) {
	t.Helper()
	dir = t.TempDir()
	l := activitystore.Open(dir)
	require.NotNil(t, l)
	respRef, _ = l.PutBlob("mcp", []byte("the result body"))
	l.Record(activitystore.Event{
		TimeMs: 1, Kind: activitystore.KindMCPToolCall, Actor: "claude",
		Action: "magus_query", Outcome: activitystore.OutcomeOK,
		ResponseRef: respRef, Preview: "the result body", DurationMs: 12,
	})
	l.Record(activitystore.Event{
		TimeMs: 2, Kind: activitystore.KindTokenLifecycle, Actor: "cli",
		Action: "connector.create", Outcome: activitystore.OutcomeOK,
	})
	require.NoError(t, l.Close())
	return dir, respRef
}

func TestListActivity_MapsAndOrdersNewestFirst(t *testing.T) {
	dir, _ := seedTrail(t)
	resp, err := NewService(dir).ListActivity(context.Background(),
		connect.NewRequest(&activityv1.ListActivityRequest{}))
	require.NoError(t, err)

	events := resp.Msg.GetEvents()
	require.Len(t, events, 2)
	assert.Equal(t, "connector.create", events[0].GetAction()) // newest first
	assert.Equal(t, activityv1.Kind_KIND_TOKEN_LIFECYCLE, events[0].GetKind())
	assert.Equal(t, activityv1.Kind_KIND_MCP_TOOL_CALL, events[1].GetKind())
	assert.Equal(t, activityv1.Outcome_OUTCOME_OK, events[1].GetOutcome())
	assert.Equal(t, "magus_query", events[1].GetAction())
	require.NotNil(t, events[1].GetDuration())
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
