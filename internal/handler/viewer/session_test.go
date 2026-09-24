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
	"github.com/egladman/magus/libs/testkit"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1"
	"github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1/viewerv1alpha1connect"
)

// serveSessions mounts a Service over a real session store seeded with events, behind a real
// transport so the RPC sees a loopback peer the way the server's does.
func serveSessions(t *testing.T, events []sessions.LoadEvent) viewerv1alpha1connect.ViewerServiceClient {
	t.Helper()
	testkit.Isolate(t)
	root := t.TempDir()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	_, err = sessions.LoadEvents(dir, events, sessions.InvocationStart{Workspace: root, Command: "test"})
	require.NoError(t, err)

	mux := http.NewServeMux()
	path, handler := viewerv1alpha1connect.NewViewerServiceHandler(NewService(&fakeOutputs{}, &fakeRuns{}, WithSessionRoot(root)))
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return viewerv1alpha1connect.NewViewerServiceClient(srv.Client(), srv.URL)
}

func loaded(session string, at int64, kind, text string, mutate ...func(*sessions.AgentEvent)) sessions.LoadEvent {
	ev := sessions.AgentEvent{
		Host: "claude-code", Kind: kind, Ref: fmt.Sprintf("ref-%d", at), AtMs: at, Text: text,
		Transcript: "/home/u/.claude/projects/x/" + session + ".jsonl",
	}
	for _, m := range mutate {
		m(&ev)
	}
	return sessions.LoadEvent{Session: session, Event: ev}
}

func atMs(ms int64) *timestamppb.Timestamp { return timestamppb.New(time.UnixMilli(ms)) }

// TestGetSessionActivityWindowsTheLastWrite pins the window: the turns after the previous write
// of the path through its last one, oldest first, with a shell command's text and a hook's
// output withheld and every transcript part the store cannot hold named as unrecorded.
func TestGetSessionActivityWindowsTheLastWrite(t *testing.T) {
	const s = "sess-1"
	client := serveSessions(t, []sessions.LoadEvent{
		loaded(s, 1, sessions.EventFileRead, "a.go"),
		loaded(s, 2, sessions.EventFileWrite, "a.go"),
		loaded(s, 3, sessions.EventShellCommand, "go test ./...", func(ev *sessions.AgentEvent) {
			ev.Program, ev.Verdict, ev.Exit = "go", sessions.VerdictPass, 1
		}),
		loaded(s, 4, sessions.EventHookOutput, "denied: rm -rf /secret"),
		loaded(s, 5, sessions.EventSkillLoad, "magus-run"),
		loaded(s, 6, sessions.EventFileWrite, "a.go"),
		loaded(s, 7, sessions.EventFileRead, "after.go"),
		loaded("other", 5, sessions.EventFileWrite, "a.go"),
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
			{Role: tool, Kind: sessions.EventShellCommand, Program: "go", Verdict: sessions.VerdictPass, Exit: 1, Time: atMs(3)},
			{Role: tool, Kind: sessions.EventHookOutput, Time: atMs(4)},
			{Role: tool, Kind: sessions.EventSkillLoad, Text: "magus-run", Time: atMs(5)},
			{Role: tool, Kind: sessions.EventFileWrite, Text: "a.go", Time: atMs(6)},
		},
		Unrecorded: unrecordedParts(),
	}
	assert.True(t, proto.Equal(want, resp.Msg), "got %v", resp.Msg)
}

// TestGetSessionActivityCapsTheWindow pins the bound: a long lead-in keeps its newest turns and
// says it dropped the rest.
func TestGetSessionActivityCapsTheWindow(t *testing.T) {
	const s = "sess-long"
	var events []sessions.LoadEvent
	for i := range sessionTurnCap + 10 {
		events = append(events, loaded(s, int64(i+1), sessions.EventFileRead, fmt.Sprintf("f%d.go", i)))
	}
	events = append(events, loaded(s, int64(sessionTurnCap+20), sessions.EventFileWrite, "a.go"))
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
	client := serveSessions(t, []sessions.LoadEvent{loaded("sess-2", 1, sessions.EventFileRead, "a.go")})

	resp, err := client.GetSessionActivity(context.Background(), connect.NewRequest(&viewerv1.GetSessionActivityRequest{Session: "sess-2", Path: "a.go"}))
	require.NoError(t, err)
	assert.False(t, resp.Msg.GetWrote())
	assert.Empty(t, resp.Msg.GetTurns())
	assert.Len(t, resp.Msg.GetUnrecorded(), 3)
}

// TestGetSessionActivityRefusals pins the three refusals: an unwired server, a malformed id, and
// a peer that is not loopback.
func TestGetSessionActivityRefusals(t *testing.T) {
	unwired := NewService(&fakeOutputs{}, &fakeRuns{})
	_, err := unwired.loadSessionActivity("127.0.0.1:5000", "sess", "a.go")
	assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))

	wired := NewService(&fakeOutputs{}, &fakeRuns{}, WithSessionRoot(t.TempDir()))
	_, err = wired.loadSessionActivity("127.0.0.1:5000", "../etc", "a.go")
	assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))

	_, err = wired.loadSessionActivity("192.168.1.20:5000", "sess", "a.go")
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
