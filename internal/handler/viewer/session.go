package viewer

import (
	"cmp"
	"context"
	"errors"
	"net"
	"slices"

	"connectrpc.com/connect"

	"github.com/egladman/magus/internal/sessions"
	viewerv1 "github.com/egladman/magus/proto/gen/go/magus/viewer/v1alpha1"
	"github.com/egladman/magus/vcs"
)

// sessionTurnCap bounds one answer. The window is a change's lead-in, and a session that
// wandered for hundreds of calls before one write still answers in a readable page.
const sessionTurnCap = 60

// Option configures a Service.
type Option func(*Service)

// WithSessionRoot lets GetSessionActivity read the loaded session store for the repository
// at root. Without it the RPC answers FailedPrecondition, because a daemon that cannot
// locate the store must not report every session as having done nothing.
func WithSessionRoot(root string) Option {
	return func(s *Service) { s.sessionRoot = root }
}

// unrecordedParts is what the session load contract cannot carry. Every adapter emits tool
// events only (docs/guides/integrations/agents/session-load.md), so these hold for every host
// until the contract grows a turn kind for them.
func unrecordedParts() []*viewerv1.Unrecorded {
	return []*viewerv1.Unrecorded{
		{Role: viewerv1.TurnRole_TURN_ROLE_USER, Reason: "session load records tool calls only; user prompts are not loaded"},
		{Role: viewerv1.TurnRole_TURN_ROLE_ASSISTANT, Reason: "session load records tool calls only; assistant text is not loaded"},
		{Role: viewerv1.TurnRole_TURN_ROLE_REASONING, Reason: "session load records tool calls only; reasoning is not loaded, even where the host log keeps it"},
	}
}

// GetSessionActivity serves the loaded events of one session that led up to its last write
// of the requested path.
func (s *Service) GetSessionActivity(_ context.Context, req *connect.Request[viewerv1.GetSessionActivityRequest]) (*connect.Response[viewerv1.SessionActivity], error) {
	out, err := s.loadSessionActivity(req.Peer().Addr, req.Msg.GetSession(), req.Msg.GetPath())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(out), nil
}

func (s *Service) loadSessionActivity(peer, session, path string) (*viewerv1.SessionActivity, error) {
	// The rest of this service rides the share surface. A session's record does not: the diff
	// routes it annotates are loopback only, and so is this.
	if !loopbackPeer(peer) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("viewer: session activity is served to local peers only"))
	}
	if !sessions.ValidSessionID(session) || path == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("viewer: a session id and a path are required"))
	}
	if s.sessionRoot == "" {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("viewer: this daemon does not serve session activity"))
	}
	dir, err := sessions.Dir(s.sessionRoot)
	if err != nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	fold, err := sessions.ReadAll(dir)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err)
	}
	return sessionActivity(session, path, sessions.AgentEvents(fold, session)), nil
}

// sessionActivity picks the window: the events after the session's previous write of path, up
// to and including its last one, ordered by the host's clock and capped at sessionTurnCap.
func sessionActivity(session, path string, events []sessions.AgentEvent) *viewerv1.SessionActivity {
	out := &viewerv1.SessionActivity{Session: session, Unrecorded: unrecordedParts()}
	slices.SortStableFunc(events, func(a, b sessions.AgentEvent) int { return cmp.Compare(a.AtMs, b.AtMs) })
	var writes []int
	for i, ev := range events {
		out.Host = cmp.Or(out.Host, ev.Host)
		out.Transcript = cmp.Or(out.Transcript, ev.Transcript)
		// compat: see knowledge.go's loadKnowledgeAgentContacts.
		if ev.Kind == sessions.EventFileWrite && (ev.Text == path || vcs.CheckoutRelative(ev.Text) == path) {
			writes = append(writes, i)
		}
	}
	if len(writes) == 0 {
		return out
	}
	out.Wrote = true
	end := writes[len(writes)-1] + 1
	start := 0
	if len(writes) > 1 {
		start = writes[len(writes)-2] + 1
	}
	if end-start > sessionTurnCap {
		start = end - sessionTurnCap
		out.Truncated = true
	}
	for _, ev := range events[start:end] {
		out.Turns = append(out.Turns, turnToProto(ev))
	}
	return out
}

func turnToProto(ev sessions.AgentEvent) *viewerv1.SessionTurn {
	t := &viewerv1.SessionTurn{
		Role:        viewerv1.TurnRole_TURN_ROLE_TOOL,
		Kind:        ev.Kind,
		Program:     ev.Program,
		Time:        tsFromMs(ev.AtMs),
		Verdict:     ev.Verdict,
		Rule:        ev.Rule,
		Exit:        int32(ev.Exit),
		Denied:      ev.Denied,
		Interrupted: ev.Interrupted,
	}
	// A guard's output can quote the argv the store refuses to keep as a command's text.
	if ev.Kind != sessions.EventShellCommand && ev.Kind != sessions.EventHookOutput {
		t.Text = ev.Text
	}
	return t
}

// loopbackPeer mirrors httpx's literal-IP rule: "localhost" can be re-pointed, so only a
// parsed loopback address counts.
func loopbackPeer(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
