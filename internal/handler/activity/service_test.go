package activity

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/trail"
	activityv1 "github.com/egladman/magus/proto/gen/go/magus/activity/v1"
	queryv1 "github.com/egladman/magus/proto/gen/go/magus/query/v1"
)

func actions(events []*activityv1.ActivityEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.GetAction()
	}
	return out
}

func list(t *testing.T, dir string, q *activityv1.ActivityQuery) []*activityv1.ActivityEvent {
	t.Helper()
	resp, err := NewService(dir).ListActivity(context.Background(),
		connect.NewRequest(&activityv1.ListActivityRequest{Filter: q}))
	require.NoError(t, err)
	return resp.Msg.GetEvents()
}

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

func TestMatchFilter_ActorsActions(t *testing.T) {
	dir, _ := seedTrail(t) // mcp(claude,magus_query) token(cli,connector.create) job(daemon,graph build)

	assert.Equal(t, []string{"connector.create"},
		actions(list(t, dir, &activityv1.ActivityQuery{Actors: []string{"cli"}})))
	assert.Equal(t, []string{"magus_query"},
		actions(list(t, dir, &activityv1.ActivityQuery{Actions: []string{"magus_query"}})))
	// actors AND actions both constrain: a mismatch on either drops the event.
	assert.Empty(t, list(t, dir, &activityv1.ActivityQuery{
		Actors: []string{"cli"}, Actions: []string{"magus_query"},
	}))
	// an unmatched value yields nothing, not everything.
	assert.Empty(t, list(t, dir, &activityv1.ActivityQuery{Actors: []string{"nobody"}}))
}

func TestMatchFilter_TimeWindow(t *testing.T) {
	dir, _ := seedTrail(t) // Ts 1 (mcp), 2 (token), 3 (job)

	since := list(t, dir, &activityv1.ActivityQuery{
		Time: &queryv1.TimeRange{Since: timestamppb.New(time.UnixMilli(2))},
	})
	assert.Equal(t, []string{"graph build", "connector.create"}, actions(since)) // Ts>=2, newest first

	until := list(t, dir, &activityv1.ActivityQuery{
		Time: &queryv1.TimeRange{Until: timestamppb.New(time.UnixMilli(2))},
	})
	assert.Equal(t, []string{"connector.create", "magus_query"}, actions(until)) // Ts<=2

	window := list(t, dir, &activityv1.ActivityQuery{
		Time: &queryv1.TimeRange{
			Since: timestamppb.New(time.UnixMilli(2)),
			Until: timestamppb.New(time.UnixMilli(2)),
		},
	})
	assert.Equal(t, []string{"connector.create"}, actions(window)) // exactly Ts==2
}

func TestListActivity_PageSizeDefaultAndCap(t *testing.T) {
	dir, _ := seedTrail(t)
	// A negative/zero page size falls back to the default; an over-max size is capped.
	// Both still return all seeded events (fewer than the cap), proving the request
	// is accepted rather than rejected.
	for _, size := range []int32{0, -5, maxPageSize + 100} {
		resp, err := NewService(dir).ListActivity(context.Background(),
			connect.NewRequest(&activityv1.ListActivityRequest{PageSize: size}))
		require.NoError(t, err)
		assert.Len(t, resp.Msg.GetEvents(), 3, "page_size=%d", size)
		assert.Empty(t, resp.Msg.GetNextPageToken())
	}
}

func TestEncodeKindAndOutcome_Defaults(t *testing.T) {
	// Every known Kind maps to its wire value; an unknown string is UNSPECIFIED, not a panic.
	assert.Equal(t, activityv1.Kind_KIND_MCP_TOOL_CALL, encodeKind(trail.KindMCPToolCall))
	assert.Equal(t, activityv1.Kind_KIND_JOB, encodeKind(trail.KindJob))
	assert.Equal(t, activityv1.Kind_KIND_CONFIG_CHANGE, encodeKind(trail.KindConfigChange))
	assert.Equal(t, activityv1.Kind_KIND_TOKEN_LIFECYCLE, encodeKind(trail.KindTokenLifecycle))
	assert.Equal(t, activityv1.Kind_KIND_SANDBOX_DENIAL, encodeKind(trail.KindSandboxDenial))
	assert.Equal(t, activityv1.Kind_KIND_UNSPECIFIED, encodeKind("who-knows"))

	assert.Equal(t, activityv1.Outcome_OUTCOME_OK, encodeOutcome(trail.OutcomeOK))
	assert.Equal(t, activityv1.Outcome_OUTCOME_ERROR, encodeOutcome(trail.OutcomeError))
	assert.Equal(t, activityv1.Outcome_OUTCOME_UNSPECIFIED, encodeOutcome(""))
}
