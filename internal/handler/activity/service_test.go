package activity

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/trail"
	activityv1 "github.com/egladman/magus/proto/gen/go/magus/activity/v1alpha1"
	queryv1 "github.com/egladman/magus/proto/gen/go/magus/query/v1alpha1"
)

func actions(events []*activityv1.ActivityEvent) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.GetAction()
	}
	return out
}

// svc builds a Service over one workspace per dir, rooted at "/ws/<dir>" so every seeded trail
// has an owning workspace to attribute its events to.
func svc(dirs ...string) *Service {
	ws := make([]Workspace, len(dirs))
	for i, d := range dirs {
		ws[i] = Workspace{Root: "/ws" + d, CacheDir: d}
	}
	return NewService(func() []Workspace { return ws })
}

func list(t *testing.T, dir string, q *activityv1.ActivityQuery) []*activityv1.ActivityEvent {
	t.Helper()
	resp, err := svc(dir).ListActivityEvents(context.Background(),
		connect.NewRequest(&activityv1.ListActivityEventsRequest{Filter: q}))
	require.NoError(t, err)
	return resp.Msg.GetEvents()
}

func seedTrail(t *testing.T) (dir, respRef string) {
	t.Helper()
	dir = t.TempDir()
	respRef, _ = trail.WriteBlob(t.Context(), dir, "mcp", []byte("the result body"))
	trail.Append(t.Context(), dir, trail.Event{
		Ts: 1, Kind: trail.KindMCPToolCall, Actor: "claude", UserAgent: "claude-code/1.2.3",
		Action: "magus_query", Outcome: trail.OutcomeOK,
		ResponseRef: respRef, Preview: "the result body", DurMs: 12,
	})
	trail.Append(t.Context(), dir, trail.Event{
		Ts: 2, Kind: trail.KindTokenLifecycle, Actor: "cli",
		Action: "connector.create", Outcome: trail.OutcomeOK,
	})
	trail.Append(t.Context(), dir, trail.Event{
		Ts: 3, Kind: trail.KindJob, Actor: "daemon", Workspace: "/ws/a",
		Action: "graph build", Outcome: trail.OutcomeError, Error: "boom", DurMs: 40,
	})
	agentReqBody := []byte(`{"schema_version":1,"tool":"Bash","command":"go test ./..."}`)
	agentRespBody := []byte(`{"schema_version":1,"decision":"deny","reason":"use magus"}`)
	agentReq, _ := trail.WriteBlob(t.Context(), dir, "agent", agentReqBody)
	agentResp, _ := trail.WriteBlob(t.Context(), dir, "agent", agentRespBody)
	trail.Append(t.Context(), dir, trail.Event{
		Ts: 4, Kind: trail.KindAgentCommand, Actor: "session:abc", Workspace: "/ws/a",
		Host: "codex", Session: "abc",
		Action: "Bash", Outcome: trail.OutcomeOK, RequestRef: agentReq, ResponseRef: agentResp,
		RequestBytes: int64(len(agentReqBody)), ResponseBytes: int64(len(agentRespBody)), Preview: "guard: deny",
	})
	return dir, respRef
}

func TestListActivityEvents_MapsAndOrdersNewestFirst(t *testing.T) {
	dir, _ := seedTrail(t)
	resp, err := svc(dir).ListActivityEvents(context.Background(),
		connect.NewRequest(&activityv1.ListActivityEventsRequest{}))
	require.NoError(t, err)

	events := resp.Msg.GetEvents()
	require.Len(t, events, 4)
	// newest first: an agent observation preserves its payload references and workspace.
	assert.Equal(t, "Bash", events[0].GetAction())
	assert.Equal(t, activityv1.Kind_KIND_AGENT_COMMAND, events[0].GetKind())
	assert.Equal(t, "session:abc", events[0].GetActor())
	assert.Equal(t, "codex", events[0].GetHost())
	assert.Equal(t, "abc", events[0].GetSession())
	assert.Equal(t, "/ws/a", events[0].GetWorkspace())
	assert.Equal(t, "guard: deny", events[0].GetPreview())
	assert.NotEmpty(t, events[0].GetRequestRef())
	assert.NotEmpty(t, events[0].GetResponseRef())

	assert.Equal(t, "graph build", events[1].GetAction())
	assert.Equal(t, activityv1.Kind_KIND_JOB, events[1].GetKind())
	assert.Equal(t, "/ws/a", events[1].GetWorkspace())
	assert.Equal(t, activityv1.Outcome_OUTCOME_ERROR, events[1].GetOutcome())
	assert.Equal(t, "boom", events[1].GetError())
	assert.Equal(t, "connector.create", events[2].GetAction())
	assert.Equal(t, activityv1.Kind_KIND_TOKEN_LIFECYCLE, events[2].GetKind())
	// A daemon-wide action carries NO workspace, and the merge must not invent one. Event.Workspace
	// means "the root this action pertained to" (trail.go), and a token rotation genuinely pertains
	// to no single workspace - substituting whichever trail happened to record it would make the
	// field mean two different things depending on the row.
	assert.Empty(t, events[2].GetWorkspace())
	assert.Equal(t, activityv1.Kind_KIND_MCP_TOOL_CALL, events[3].GetKind())
	assert.Equal(t, activityv1.Outcome_OUTCOME_OK, events[3].GetOutcome())
	assert.Equal(t, "magus_query", events[3].GetAction())
	// An MCP call has no hook wrapper to name its host, so its User-Agent fills the same field
	// and one view can group hook and MCP events together.
	assert.Equal(t, "claude-code/1.2.3", events[3].GetHost())
	require.NotNil(t, events[3].GetDuration())
}

func TestEncodeHost_RecordedHostWinsAndMCPFallsBackToUserAgent(t *testing.T) {
	assert.Equal(t, "codex", encodeHost(trail.Event{Kind: trail.KindAgentCommand, Host: "codex"}))
	assert.Equal(t, "claude-code/1.2.3", encodeHost(trail.Event{Kind: trail.KindMCPToolCall, UserAgent: "claude-code/1.2.3"}))
	// A recorded host is what its producer observed, so it wins over the header reading.
	assert.Equal(t, "codex", encodeHost(trail.Event{Kind: trail.KindMCPToolCall, Host: "codex", UserAgent: "curl/8"}))
	// Only an MCP call's User-Agent stands in for a host; no other kind borrows one.
	assert.Empty(t, encodeHost(trail.Event{Kind: trail.KindJob, UserAgent: "curl/8"}))
	assert.Empty(t, encodeHost(trail.Event{Kind: trail.KindAgentCommand}))
}

func TestListActivityEvents_FilterByKind(t *testing.T) {
	dir, _ := seedTrail(t)
	resp, err := svc(dir).ListActivityEvents(context.Background(),
		connect.NewRequest(&activityv1.ListActivityEventsRequest{
			Filter: &activityv1.ActivityQuery{Kinds: []activityv1.Kind{activityv1.Kind_KIND_MCP_TOOL_CALL}},
		}))
	require.NoError(t, err)
	events := resp.Msg.GetEvents()
	require.Len(t, events, 1)
	assert.Equal(t, "magus_query", events[0].GetAction())
}

func TestListActivityEvents_FilterAgentCommand(t *testing.T) {
	dir, _ := seedTrail(t)
	events := list(t, dir, &activityv1.ActivityQuery{Kinds: []activityv1.Kind{activityv1.Kind_KIND_AGENT_COMMAND}})
	require.Len(t, events, 1)
	assert.Equal(t, "Bash", events[0].GetAction())
	assert.Equal(t, "session:abc", events[0].GetActor())
}

// TestListActivityEvents_CarriesAgentSpawn proves the lease record reaches the console over the
// EXISTING listing: a distinct kind, the unit on the row so a reader can join a page to a work
// ledger without a GetPayload per row, and the handed context reachable only by its ref.
func TestListActivityEvents_CarriesAgentSpawn(t *testing.T) {
	dir := t.TempDir()
	trail.AppendAgentSpawn(t.Context(), dir, trail.AgentSpawn{
		Workspace: "/ws/a", Host: "claude-code", Session: "abc", Tool: "Task", Child: "Explore",
		Context: "lease: notes-store-6b\naudit the store",
	})

	events := list(t, dir, nil)
	require.Len(t, events, 1)
	assert.Equal(t, activityv1.Kind_KIND_AGENT_SPAWN, events[0].GetKind())
	assert.Equal(t, "Explore", events[0].GetAction())
	assert.Equal(t, "notes-store-6b", events[0].GetUnit())
	assert.Equal(t, "claude-code", events[0].GetHost())
	assert.Equal(t, "/ws/a", events[0].GetWorkspace())
	assert.NotEmpty(t, events[0].GetRequestRef())
	assert.Empty(t, events[0].GetResponseRef(), "a spawn has no response: nothing judged it")

	// The kind filter selects it, so the console needs no new endpoint to build a lease view.
	assert.Len(t, list(t, dir, &activityv1.ActivityQuery{
		Kinds: []activityv1.Kind{activityv1.Kind_KIND_AGENT_SPAWN},
	}), 1)

	body, err := svc(dir).GetPayload(context.Background(),
		connect.NewRequest(&activityv1.GetPayloadRequest{Ref: events[0].GetRequestRef()}))
	require.NoError(t, err)
	assert.Contains(t, string(body.Msg.GetBody()), "audit the store")
}

func TestGetPayload_RoundTripAndReject(t *testing.T) {
	dir, ref := seedTrail(t)
	s := svc(dir)

	pr, err := s.GetPayload(context.Background(),
		connect.NewRequest(&activityv1.GetPayloadRequest{Ref: ref}))
	require.NoError(t, err)
	assert.Equal(t, "the result body", string(pr.Msg.GetBody()))
	assert.Equal(t, int64(len("the result body")), pr.Msg.GetSizeBytes())

	_, err = s.GetPayload(context.Background(),
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
	dir, _ := seedTrail(t) // Ts 1 (mcp), 2 (token), 3 (job), 4 (agent)

	since := list(t, dir, &activityv1.ActivityQuery{
		Time: &queryv1.TimeRange{Since: timestamppb.New(time.UnixMilli(2))},
	})
	assert.Equal(t, []string{"Bash", "graph build", "connector.create"}, actions(since)) // Ts>=2, newest first

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

func TestListActivityEvents_PageSizeDefaultAndCap(t *testing.T) {
	dir, _ := seedTrail(t)
	// A negative/zero page size falls back to the default; an over-max size is capped.
	// Both still return all seeded events (fewer than the cap), proving the request
	// is accepted rather than rejected.
	for _, size := range []int32{0, -5, maxPageSize + 100} {
		resp, err := svc(dir).ListActivityEvents(context.Background(),
			connect.NewRequest(&activityv1.ListActivityEventsRequest{PageSize: size}))
		require.NoError(t, err)
		assert.Len(t, resp.Msg.GetEvents(), 4, "page_size=%d", size)
		assert.Empty(t, resp.Msg.GetNextPageToken())
	}
}

// row is the merged view's identity for an assertion: which action, from which workspace, when.
// Comparing whole rows keeps ordering and attribution in a single assert.
type row struct {
	Action    string
	Workspace string
	Ts        int64
}

func rows(events []*activityv1.ActivityEvent) []row {
	out := make([]row, len(events))
	for i, e := range events {
		out[i] = row{Action: e.GetAction(), Workspace: e.GetWorkspace(), Ts: e.GetTime().AsTime().UnixMilli()}
	}
	return out
}

// seedAt writes one minimal job event per timestamp into a fresh trail dir, using the timestamp
// as the action so a merged page reads back unambiguously.
//
// Each event RECORDS its workspace, the way a real per-workspace producer does (a job runs against
// one workspace and says so). The merge deliberately does not invent an attribution for events that
// omit it, so a fixture that wants to assert on workspace has to supply it - which is the honest
// shape anyway.
func seedAt(t *testing.T, ts ...int64) string {
	t.Helper()
	dir := t.TempDir()
	for _, at := range ts {
		trail.Append(t.Context(), dir, trail.Event{
			Ts: at, Kind: trail.KindJob, Actor: "daemon", Workspace: "/ws" + dir,
			Action: fmt.Sprintf("job-%d", at), Outcome: trail.OutcomeOK,
		})
	}
	return dir
}

func listAll(t *testing.T, s *Service, pageSize int32) []*activityv1.ActivityEvent {
	t.Helper()
	resp, err := s.ListActivityEvents(context.Background(),
		connect.NewRequest(&activityv1.ListActivityEventsRequest{PageSize: pageSize}))
	require.NoError(t, err)
	return resp.Msg.GetEvents()
}

func TestListActivityEvents_MergesWorkspacesNewestFirst(t *testing.T) {
	// Two workspaces whose events interleave in time. Concatenating the trails would group them
	// by workspace; the merged page must be in time order across both.
	a, b := seedAt(t, 1, 3, 5), seedAt(t, 2, 4, 6)

	assert.Equal(t, []row{
		{Action: "job-6", Workspace: "/ws" + b, Ts: 6},
		{Action: "job-5", Workspace: "/ws" + a, Ts: 5},
		{Action: "job-4", Workspace: "/ws" + b, Ts: 4},
		{Action: "job-3", Workspace: "/ws" + a, Ts: 3},
		{Action: "job-2", Workspace: "/ws" + b, Ts: 2},
		{Action: "job-1", Workspace: "/ws" + a, Ts: 1},
	}, rows(listAll(t, svc(a, b), 0)))
}

func TestListActivityEvents_PageSizeCapsTheMergedSet(t *testing.T) {
	// page_size=3 over two trails must be the 3 most recent DAEMON-WIDE, not 3 from each.
	a, b := seedAt(t, 1, 3, 5), seedAt(t, 2, 4, 6)

	assert.Equal(t, []row{
		{Action: "job-6", Workspace: "/ws" + b, Ts: 6},
		{Action: "job-5", Workspace: "/ws" + a, Ts: 5},
		{Action: "job-4", Workspace: "/ws" + b, Ts: 4},
	}, rows(listAll(t, svc(a, b), 3)))
}

func TestListActivityEvents_SkipsUnreadableWorkspace(t *testing.T) {
	// A workspace that cannot be read must not blank the panel for the ones that can: a cache dir
	// that is a FILE (every path under it fails), and one that never recorded anything.
	broken := filepath.Join(t.TempDir(), "not-a-dir")
	require.NoError(t, os.WriteFile(broken, []byte("x"), 0o644))
	silent := t.TempDir()
	ok := seedAt(t, 7, 8)

	assert.Equal(t, []row{
		{Action: "job-8", Workspace: "/ws" + ok, Ts: 8},
		{Action: "job-7", Workspace: "/ws" + ok, Ts: 7},
	}, rows(listAll(t, svc(broken, ok, silent), 0)))
}

func TestListActivityEvents_MergePreservesRecordedWorkspaceAndDoesNotInventOne(t *testing.T) {
	// Event.Workspace means "the root this action pertained to", and it is deliberately empty for a
	// daemon-wide action (trail.go). Merging trails must not change that: an earlier revision filled
	// blanks from the trail that happened to hold them, which made a daemon-wide MCP call or token
	// rotation claim a workspace it was never bound to, and left the field meaning one thing on some
	// rows and something else on others.
	//
	// So the invariant is per-event, not blanket: what the producer recorded survives the merge, and
	// what it left blank stays blank.
	dir, _ := seedTrail(t)
	other := seedAt(t, 9)

	byAction := map[string]string{}
	for _, e := range listAll(t, svc(dir, other), 0) {
		byAction[e.GetAction()] = e.GetWorkspace()
	}
	assert.Equal(t, "/ws/a", byAction["Bash"], "a workspace-bound agent command keeps its own root")
	assert.Equal(t, "/ws/a", byAction["graph build"], "a workspace-bound job keeps its own root")
	assert.Equal(t, "/ws"+other, byAction["job-9"], "an event from the other trail keeps its root")
	assert.Empty(t, byAction["connector.create"], "a daemon-wide token event stays unattributed")
	assert.Empty(t, byAction["magus_query"], "a daemon-wide MCP call stays unattributed")
}

func TestListActivityEvents_SingleWorkspaceAndNoWorkspaces(t *testing.T) {
	// The common case does not regress: one workspace returns its own events...
	dir, _ := seedTrail(t)
	assert.Len(t, listAll(t, svc(dir), 0), 4)

	// ...and a daemon reporting no workspaces at all serves an empty page, not an error.
	assert.Empty(t, listAll(t, NewService(nil), 0))
	assert.Empty(t, listAll(t, NewService(func() []Workspace { return nil }), 0))
	// A workspace with no cache dir to read is dropped rather than read as the process cwd.
	assert.Empty(t, listAll(t, NewService(func() []Workspace { return []Workspace{{Root: "/ws/a"}} }), 0))
}

func TestGetPayload_FindsTheHoldingWorkspace(t *testing.T) {
	// The ref lives in the SECOND workspace's blob store; a daemon-wide view must still serve it.
	dir, ref := seedTrail(t)
	s := svc(t.TempDir(), dir)

	pr, err := s.GetPayload(context.Background(),
		connect.NewRequest(&activityv1.GetPayloadRequest{Ref: ref}))
	require.NoError(t, err)
	assert.Equal(t, "the result body", string(pr.Msg.GetBody()))

	// No workspace holds it, and no workspace exists at all: both are NotFound, never a panic.
	_, err = s.GetPayload(context.Background(),
		connect.NewRequest(&activityv1.GetPayloadRequest{Ref: "mcp0000000000000000"}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
	_, err = NewService(nil).GetPayload(context.Background(),
		connect.NewRequest(&activityv1.GetPayloadRequest{Ref: ref}))
	assert.Equal(t, connect.CodeNotFound, connect.CodeOf(err))
}

func TestEncodeKindAndOutcome_Defaults(t *testing.T) {
	// Every known Kind maps to its wire value; an unknown string is UNSPECIFIED, not a panic.
	assert.Equal(t, activityv1.Kind_KIND_MCP_TOOL_CALL, encodeKind(trail.KindMCPToolCall))
	assert.Equal(t, activityv1.Kind_KIND_JOB, encodeKind(trail.KindJob))
	assert.Equal(t, activityv1.Kind_KIND_CONFIG_CHANGE, encodeKind(trail.KindConfigChange))
	assert.Equal(t, activityv1.Kind_KIND_TOKEN_LIFECYCLE, encodeKind(trail.KindTokenLifecycle))
	assert.Equal(t, activityv1.Kind_KIND_SANDBOX_DENIAL, encodeKind(trail.KindSandboxDenial))
	assert.Equal(t, activityv1.Kind_KIND_AGENT_COMMAND, encodeKind(trail.KindAgentCommand))
	assert.Equal(t, activityv1.Kind_KIND_MEMORY, encodeKind(trail.KindMemory))
	assert.Equal(t, activityv1.Kind_KIND_UNSPECIFIED, encodeKind("who-knows"))

	assert.Equal(t, activityv1.Outcome_OUTCOME_OK, encodeOutcome(trail.OutcomeOK))
	assert.Equal(t, activityv1.Outcome_OUTCOME_ERROR, encodeOutcome(trail.OutcomeError))
	assert.Equal(t, activityv1.Outcome_OUTCOME_UNSPECIFIED, encodeOutcome(""))
}

// TestEncodeKindCoversEveryTrailKind: a kind with no proto value encodes to
// KIND_UNSPECIFIED, which is indistinguishable from "unset" and cannot be selected with
// ActivityQuery.kinds - so the event is written, stored, and then invisible to the one
// surface that exists to read it. KindCredentialGrant shipped that way and the docs
// promised a governance view that did not exist.
//
// Guards every kind, not just that one, so the next producer cannot repeat it.
func TestEncodeKindCoversEveryTrailKind(t *testing.T) {
	for _, k := range []trail.Kind{
		trail.KindMCPToolCall,
		trail.KindJob,
		trail.KindConfigChange,
		trail.KindTokenLifecycle,
		trail.KindSandboxDenial,
		trail.KindMemory,
		trail.KindAgentCommand,
		trail.KindCredentialGrant,
		trail.KindAgentSpawn,
		trail.KindNotes,
	} {
		assert.NotEqual(t, activityv1.Kind_KIND_UNSPECIFIED, encodeKind(k),
			"trail kind %q has no proto value, so the activity view cannot show or filter it", k)
	}
}
