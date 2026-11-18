package viewer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/libs/safetyscissors/session"
	"github.com/egladman/magus/libs/safetyscissors/verdict"
	"github.com/egladman/magus/libs/testkit"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1/viewerv1alpha1connect"
)

// serveSessions mounts a Service over a real session store seeded with events, behind a real
// transport so the RPC sees a loopback peer the way the server's does.
func serveSessions(t *testing.T, events []session.LoadEvent) viewerv1alpha1connect.ViewerServiceClient { //nolint:ireturn // the generated client type is an interface; the implementation is chosen at runtime
	t.Helper()
	testkit.Isolate(t)
	root := t.TempDir()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	_, err = session.LoadEvents(t.Context(), sessions.StoreConfig(dir, "", session.InvocationStart{Workspace: root, Command: "test"}), events)
	require.NoError(t, err)

	mux := http.NewServeMux()
	path, handler := viewerv1alpha1connect.NewViewerServiceHandler(NewService(&storedOutputs{}, &storedRuns{}, t.TempDir(), WithSessionRoot(root)))
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return viewerv1alpha1connect.NewViewerServiceClient(srv.Client(), srv.URL)
}

func loaded(hostSession string, at int64, kind, text string, mutate ...func(*session.AgentEvent)) session.LoadEvent {
	ev := session.AgentEvent{
		Host: "claude-code", Kind: kind, Ref: fmt.Sprintf("ref-%d", at), AtMs: at, Text: text,
		Transcript: fmt.Sprintf("/home/u/.claude/projects/x/%s.jsonl", hostSession),
	}
	for _, m := range mutate {
		m(&ev)
	}
	return session.LoadEvent{Session: hostSession, Event: ev}
}

func atMs(ms int64) *timestamppb.Timestamp { return timestamppb.New(time.UnixMilli(ms)) }

// TestGetSessionActivityWindowsTheLastWrite pins the window: the turns after the previous write
// of the path through its last one, oldest first, with a shell command's text and a hook's
// output withheld and every transcript part the store cannot hold named as unrecorded.
func TestGetSessionActivityWindowsTheLastWrite(t *testing.T) {
	const s = "sess-1"
	client := serveSessions(t, []session.LoadEvent{
		loaded(s, 1, session.EventFileRead, "a.go"),
		loaded(s, 2, session.EventFileWrite, "a.go"),
		loaded(s, 3, session.EventShellCommand, "go test ./...", func(ev *session.AgentEvent) {
			ev.Program, ev.Verdict, ev.Exit = "go", verdict.Defer, 1
		}),
		loaded(s, 4, session.EventHookOutput, "denied: rm -rf /secret"),
		loaded(s, 5, session.EventSkillLoad, "magus-run"),
		loaded(s, 6, session.EventFileWrite, "a.go"),
		loaded(s, 7, session.EventFileRead, "after.go"),
		loaded("other", 5, session.EventFileWrite, "a.go"),
	})

	resp, err := client.GetSessionActivity(context.Background(), connect.NewRequest(&viewerv1.GetSessionActivityRequest{Session: s, Path: "a.go"}))
	require.NoError(t, err)

	tool := viewerv1.TurnRole_TURN_ROLE_TOOL
	want := &viewerv1.SessionActivity{
		Session:    s,
		Host:       "claude-code",
		Transcript: "/home/u/.claude/projects/x/sess-1.jsonl",
		Wrote:      true,
		Turns: []*viewerv1.SessionTurn{
			{Role: tool, Kind: session.EventShellCommand, Program: "go", Verdict: string(verdict.Defer), Exit: 1, Time: atMs(3)},
			{Role: tool, Kind: session.EventHookOutput, Time: atMs(4)},
			{Role: tool, Kind: session.EventSkillLoad, Text: "magus-run", Time: atMs(5)},
			{Role: tool, Kind: session.EventFileWrite, Text: "a.go", Time: atMs(6)},
		},
		Unrecorded: unrecordedParts(),
	}
	assert.True(t, proto.Equal(want, resp.Msg), "got %v", resp.Msg)
}

// TestGetSessionActivityCapsTheWindow pins the bound: a long lead-in keeps its newest turns and
// says it dropped the rest.
func TestGetSessionActivityCapsTheWindow(t *testing.T) {
	const s = "sess-long"
	var events []session.LoadEvent
	for i := range sessionTurnCap + 10 {
		events = append(events, loaded(s, int64(i+1), session.EventFileRead, fmt.Sprintf("f%d.go", i)))
	}
	events = append(events, loaded(s, int64(sessionTurnCap+20), session.EventFileWrite, "a.go"))
	client := serveSessions(t, events)

	resp, err := client.GetSessionActivity(context.Background(), connect.NewRequest(&viewerv1.GetSessionActivityRequest{Session: s, Path: "a.go"}))
	require.NoError(t, err)
	assert.True(t, resp.Msg.GetTruncated())
	require.Len(t, resp.Msg.GetTurns(), sessionTurnCap)
	assert.Equal(t, "f11.go", resp.Msg.GetTurns()[0].GetText())
	assert.Equal(t, "a.go", resp.Msg.GetTurns()[sessionTurnCap-1].GetText())
}

// TestGetSessionActivityNoWrite is a session the store holds that never wrote the path: no
// turns, Wrote false, and the unrecorded parts still stated.
func TestGetSessionActivityNoWrite(t *testing.T) {
	client := serveSessions(t, []session.LoadEvent{loaded("sess-2", 1, session.EventFileRead, "a.go")})

	resp, err := client.GetSessionActivity(context.Background(), connect.NewRequest(&viewerv1.GetSessionActivityRequest{Session: "sess-2", Path: "a.go"}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.GetWrote())
	assert.Empty(t, resp.Msg.GetTurns())
	assert.Len(t, resp.Msg.GetUnrecorded(), 3)
}

// TestGetSessionActivityRefusals pins the three refusals: an unwired server, a malformed id, and
// a peer that is not loopback.
func TestGetSessionActivityRefusals(t *testing.T) {
	unwired := NewService(&storedOutputs{}, &storedRuns{}, t.TempDir())
	_, err := unwired.loadSessionActivity(t.Context(), "127.0.0.1:5000", "sess", "a.go")
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	wired := NewService(&storedOutputs{}, &storedRuns{}, t.TempDir(), WithSessionRoot(t.TempDir()))
	_, err = wired.loadSessionActivity(t.Context(), "127.0.0.1:5000", "../etc", "a.go")
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = wired.loadSessionActivity(t.Context(), "192.168.1.20:5000", "sess", "a.go")
	assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
}

func TestLoopbackPeer(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1:80": true, "[::1]:80": true, "::1": true,
		"localhost:80": false, "10.0.0.2:80": false, "": false,
	} {
		assert.Equal(t, want, loopbackPeer(addr), addr)
	}
}
