package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/proc"
)

// makeReply builds a minimal StatusReply with the given pid and workspaces.
func makeReply(pid int, roots ...string) *proc.StatusReply {
	r := &proc.StatusReply{ParentPID: pid}
	for _, root := range roots {
		r.Workspaces = append(r.Workspaces, proc.Workspace{
			Root:       root,
			LoadedAt:   time.Now(),
			LastAccess: time.Now(),
		})
	}
	return r
}

func TestEvaluateHealth(t *testing.T) {
	tests := []struct {
		name    string
		reply   *proc.StatusReply
		err     error
		kind    probeKind
		root    string
		wantOK  bool
		wantSub string // substring expected in reason
	}{
		// -- unreachable --
		{
			name:  "liveness/unreachable-err",
			reply: nil, err: errors.New("dial failed"),
			kind: probeLiveness, wantOK: false, wantSub: "unreachable",
		},
		{
			name:  "liveness/nil-reply-no-err",
			reply: nil, err: nil,
			kind: probeLiveness, wantOK: false, wantSub: "unreachable",
		},
		{
			name:  "readiness/unreachable",
			reply: nil, err: errors.New("no socket"),
			kind: probeReadiness, wantOK: false, wantSub: "unreachable",
		},
		// -- liveness OK --
		{
			name:   "liveness/daemon-up-no-workspaces",
			reply:  makeReply(42),
			kind:   probeLiveness,
			wantOK: true, wantSub: "42",
		},
		{
			name:   "liveness/daemon-up-with-workspace",
			reply:  makeReply(99, "/ws"),
			kind:   probeLiveness,
			wantOK: true, wantSub: "99",
		},
		// -- readiness without root (any workspace) --
		{
			name:    "readiness/no-workspaces-no-root",
			reply:   makeReply(1),
			kind:    probeReadiness,
			wantOK:  false,
			wantSub: "no workspaces",
		},
		{
			name:   "readiness/one-workspace-no-root",
			reply:  makeReply(1, "/ws"),
			kind:   probeReadiness,
			wantOK: true, wantSub: "1 workspace",
		},
		{
			name:   "readiness/two-workspaces-no-root",
			reply:  makeReply(1, "/ws", "/ws2"),
			kind:   probeReadiness,
			wantOK: true, wantSub: "2 workspace",
		},
		// -- readiness with specific root --
		{
			name:  "readiness/root-present",
			reply: makeReply(1, "/repo"),
			kind:  probeReadiness, root: "/repo",
			wantOK: true, wantSub: "/repo",
		},
		{
			name:  "readiness/root-missing",
			reply: makeReply(1, "/other"),
			kind:  probeReadiness, root: "/repo",
			wantOK:  false,
			wantSub: "/repo",
		},
		{
			name:  "readiness/root-given-no-workspaces",
			reply: makeReply(1),
			kind:  probeReadiness, root: "/repo",
			wantOK:  false,
			wantSub: "/repo",
		},
		// -- path normalisation --
		{
			name:  "readiness/root-trailing-slash-normalised",
			reply: makeReply(1, "/repo"),
			kind:  probeReadiness, root: "/repo/",
			wantOK: true, wantSub: "/repo",
		},
		// -- daemon mode is explicit; proc mode can't report readiness --
		{
			name:   "readiness/daemon-mode-with-workspace",
			reply:  &proc.StatusReply{ParentPID: 1, Mode: "daemon", Workspaces: []proc.Workspace{{Root: "/ws"}}},
			kind:   probeReadiness,
			wantOK: true, wantSub: "1 workspace",
		},
		{
			name:    "readiness/proc-mode-rejected",
			reply:   &proc.StatusReply{ParentPID: 1, Mode: "proc"},
			kind:    probeReadiness,
			wantOK:  false,
			wantSub: "per-process mode",
		},
		{
			name:   "liveness/proc-mode-still-alive",
			reply:  &proc.StatusReply{ParentPID: 7, Mode: "proc"},
			kind:   probeLiveness,
			wantOK: true, wantSub: "7",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := evaluateHealth(statusOutputFromReply(tc.reply), tc.err, tc.kind, tc.root)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v (reason: %q)", ok, tc.wantOK, reason)
			}
			if tc.wantSub != "" && !strings.Contains(reason, tc.wantSub) {
				t.Errorf("reason %q does not contain %q", reason, tc.wantSub)
			}
		})
	}
}

func TestHealthHTTPHandler(t *testing.T) {
	tests := []struct {
		name       string
		kind       probeKind
		workspaces []string // loaded workspaces in the fake reply
		queryWS    string   // ?workspace= param
		wantStatus int
	}{
		{"liveness/ok", probeLiveness, nil, "", http.StatusOK},
		{"liveness/ok-with-workspaces", probeLiveness, []string{"/ws"}, "", http.StatusOK},
		{"readiness/no-ws", probeReadiness, nil, "", http.StatusServiceUnavailable},
		{"readiness/ws-loaded-no-filter", probeReadiness, []string{"/ws"}, "", http.StatusOK},
		{"readiness/ws-match", probeReadiness, []string{"/ws"}, "/ws", http.StatusOK},
		{"readiness/ws-no-match", probeReadiness, []string{"/ws"}, "/other", http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reply := makeReply(1, tc.workspaces...)
			// Build a handler with a fake querier instead of dialing a real socket.
			h := healthHTTPHandler(tc.kind, func(context.Context) (*proc.StatusReply, error) {
				return reply, nil
			})
			url := "/"
			if tc.queryWS != "" {
				url = fmt.Sprintf("/?workspace=%s", tc.queryWS)
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rec := httptest.NewRecorder()
			h(rec, req)
			if rec.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d (body: %q)", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

// unreachable querier for testing the error path
func TestHealthHTTPHandlerUnreachable(t *testing.T) {
	h := healthHTTPHandler(probeLiveness, func(context.Context) (*proc.StatusReply, error) {
		return nil, errors.New("socket not found")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected 503, got %d", rec.Code)
	}
}
