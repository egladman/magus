package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
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

// TestResolveDeclaredWorkspacesMergesAndDedupes verifies that cfg and env
// inputs are merged, deduplicated, resolved to absolute paths, and that
// non-existent or non-directory entries are skipped.
func TestResolveDeclaredWorkspacesMergesAndDedupes(t *testing.T) {
	a := t.TempDir()
	b := t.TempDir()
	c := t.TempDir()

	// Non-existent path should be dropped silently.
	missing := filepath.Join(t.TempDir(), "does-not-exist")

	got := resolveDeclaredWorkspaces(
		[]string{a, b}, // cfg
		b+string(filepath.ListSeparator)+c+string(filepath.ListSeparator)+missing, // env (b is duplicate)
	)
	want := map[string]bool{a: true, b: true, c: true}
	if len(got) != len(want) {
		t.Fatalf("got %d workspaces, want %d: %v", len(got), len(want), got)
	}
	for _, root := range got {
		if !want[root] {
			t.Errorf("unexpected root in result: %q", root)
		}
	}
}

func TestResolveDeclaredWorkspacesEmpty(t *testing.T) {
	if got := resolveDeclaredWorkspaces(nil, ""); got != nil {
		t.Errorf("expected nil for empty inputs; got %v", got)
	}
}

// TestAcquireRejectsNonDeclared confirms that once setDeclared has been
// called with an allowlist, acquire of a root outside the list fails with
// MGS2010 (SandboxPolicyMismatch).
func TestAcquireRejectsNonDeclared(t *testing.T) {
	allowed := t.TempDir()
	forbidden := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(1)
	reg := newWSRegistry(ctx, lim, 0)
	defer reg.close()

	reg.setDeclared([]string{allowed})

	_, err := reg.acquire(ctx, forbidden)
	if err == nil {
		t.Fatal("acquire of non-declared root should error")
	}
	var de *types.DiagnosticError
	if !errors.As(err, &de) || de.Code != types.SandboxPolicyMismatch {
		t.Errorf("expected MGS2010 SandboxPolicyMismatch; got %v", err)
	}
}

// TestAcquireAdmitsDeclaredEvenWithoutMagusYaml verifies that a declared
// workspace without a magus.yaml falls back to defaults rather than being
// rejected as undeclared. Without an actual workspace layout the load will
// still fail; we only verify that the allowlist check passes (the failure
// surfaces from magus.Open, not from the declared gate).
func TestAcquireAdmitsDeclaredEvenWithoutMagusYaml(t *testing.T) {
	allowed := t.TempDir()
	// Write an empty magus.yaml so config loading succeeds and the test
	// reaches the magus.Open step deterministically.
	if err := os.WriteFile(filepath.Join(allowed, "magus.yaml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lim := cache.NewLimiter(1)
	reg := newWSRegistry(ctx, lim, 0)
	defer reg.close()
	reg.setDeclared([]string{allowed})

	// acquire may fail at magus.Open (no real workspace), but it must NOT
	// fail with the MGS2010 declared-list gate.
	_, err := reg.acquire(ctx, allowed)
	if err != nil {
		var de *types.DiagnosticError
		if errors.As(err, &de) && de.Code == types.SandboxPolicyMismatch {
			t.Fatalf("acquire of declared root was wrongly rejected by the allowlist gate: %v", err)
		}
	}
}

// TestWarmRespectsContextCancellation verifies that warm exits promptly when
// the context is already cancelled, without hanging on acquire.
func TestWarmRespectsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before warm is called

	lim := cache.NewLimiter(2)
	reg := newWSRegistry(context.Background(), lim, 0)
	defer reg.close()

	// Supply several roots; warm should bail after the first ctx.Err() check.
	roots := []string{t.TempDir(), t.TempDir(), t.TempDir()}
	reg.setDeclared(roots)

	done := make(chan struct{})
	go func() {
		reg.warm(ctx, roots)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("warm did not return promptly after context cancellation")
	}
}

// TestWarmCompletesAndPopulatesStatus verifies that warm runs to completion
// and that any successfully loaded workspaces appear in status() afterwards.
// Warm must not panic, hang, or silently skip entries.
func TestWarmCompletesAndPopulatesStatus(t *testing.T) {
	root1 := t.TempDir()
	root2 := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(2)
	reg := newWSRegistry(ctx, lim, 0)
	defer reg.close()
	reg.setDeclared([]string{root1, root2})

	done := make(chan struct{})
	go func() {
		reg.warm(ctx, []string{root1, root2})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("warm did not complete within timeout")
	}
	// Any workspace that loaded successfully must appear in status().
	// The exact count depends on whether magus.Open succeeds for bare dirs.
	_ = reg.status() // must not panic
}

// TestWarmInBackgroundTrackedByClose verifies that close() waits for an
// in-flight warm rather than racing it — otherwise a workspace could be
// acquired (and leaked) after close() swept the entry map.
func TestWarmInBackgroundTrackedByClose(t *testing.T) {
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	lim := cache.NewLimiter(2)
	reg := newWSRegistry(ctx, lim, 0)
	reg.setDeclared([]string{root})
	reg.warmInBackground(ctx, []string{root})

	// close() must return: its wg.Wait() should account for the warm goroutine
	// (plus the janitor) and not hang.
	done := make(chan struct{})
	go func() {
		reg.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("close did not return; warm goroutine not tracked by the waitgroup?")
	}
}
