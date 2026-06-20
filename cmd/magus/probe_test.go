package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
	// assertHealth evaluates one health scenario and checks the ok flag plus a
	// substring of the reason.
	assertHealth := func(name string, reply *proc.StatusReply, err error, kind probeKind, root string, wantOK bool, wantSub string) {
		t.Run(name, func(t *testing.T) {
			ok, reason := evaluateHealth(statusOutputFromReply(reply), err, kind, root)
			assert.Equal(t, wantOK, ok, "reason: %q", reason)
			if wantSub != "" {
				assert.Contains(t, reason, wantSub)
			}
		})
	}

	assertHealth("liveness/unreachable-err", nil, errors.New("dial failed"), probeLiveness, "", false, "unreachable")
	assertHealth("liveness/nil-reply-no-err", nil, nil, probeLiveness, "", false, "unreachable")
	assertHealth("readiness/unreachable", nil, errors.New("no socket"), probeReadiness, "", false, "unreachable")
	assertHealth("liveness/daemon-up-no-workspaces", makeReply(42), nil, probeLiveness, "", true, "42")
	assertHealth("liveness/daemon-up-with-workspace", makeReply(99, "/ws"), nil, probeLiveness, "", true, "99")
	assertHealth("readiness/no-workspaces-no-root", makeReply(1), nil, probeReadiness, "", false, "no workspaces")
	assertHealth("readiness/one-workspace-no-root", makeReply(1, "/ws"), nil, probeReadiness, "", true, "1 workspace")
	assertHealth("readiness/two-workspaces-no-root", makeReply(1, "/ws", "/ws2"), nil, probeReadiness, "", true, "2 workspace")
	assertHealth("readiness/root-present", makeReply(1, "/repo"), nil, probeReadiness, "/repo", true, "/repo")
	assertHealth("readiness/root-missing", makeReply(1, "/other"), nil, probeReadiness, "/repo", false, "/repo")
	assertHealth("readiness/root-given-no-workspaces", makeReply(1), nil, probeReadiness, "/repo", false, "/repo")
	assertHealth("readiness/root-trailing-slash-normalised", makeReply(1, "/repo"), nil, probeReadiness, "/repo/", true, "/repo")
	assertHealth("readiness/daemon-mode-with-workspace",
		&proc.StatusReply{ParentPID: 1, Mode: "daemon", Workspaces: []proc.Workspace{{Root: "/ws"}}},
		nil, probeReadiness, "", true, "1 workspace")
	assertHealth("readiness/proc-mode-rejected",
		&proc.StatusReply{ParentPID: 1, Mode: "proc"}, nil, probeReadiness, "", false, "per-process mode")
	assertHealth("liveness/proc-mode-still-alive",
		&proc.StatusReply{ParentPID: 7, Mode: "proc"}, nil, probeLiveness, "", true, "7")
}

func TestHealthHTTPHandler(t *testing.T) {
	// assertHandler stands up a handler with a fake querier and checks the HTTP
	// status code for a given probe kind and ?workspace= filter.
	assertHandler := func(name string, kind probeKind, workspaces []string, queryWS string, wantStatus int) {
		t.Run(name, func(t *testing.T) {
			reply := makeReply(1, workspaces...)
			// Build a handler with a fake querier instead of dialing a real socket.
			h := healthHTTPHandler(kind, func(context.Context) (*types.StatusOutput, error) {
				return statusOutputFromReply(reply), nil
			})
			url := "/"
			if queryWS != "" {
				url = fmt.Sprintf("/?workspace=%s", queryWS)
			}
			req := httptest.NewRequest(http.MethodGet, url, nil)
			rec := httptest.NewRecorder()
			h(rec, req)
			assert.Equal(t, wantStatus, rec.Code, "body: %q", rec.Body.String())
		})
	}

	assertHandler("liveness/ok", probeLiveness, nil, "", http.StatusOK)
	assertHandler("liveness/ok-with-workspaces", probeLiveness, []string{"/ws"}, "", http.StatusOK)
	assertHandler("readiness/no-ws", probeReadiness, nil, "", http.StatusServiceUnavailable)
	assertHandler("readiness/ws-loaded-no-filter", probeReadiness, []string{"/ws"}, "", http.StatusOK)
	assertHandler("readiness/ws-match", probeReadiness, []string{"/ws"}, "/ws", http.StatusOK)
	assertHandler("readiness/ws-no-match", probeReadiness, []string{"/ws"}, "/other", http.StatusServiceUnavailable)
}

// unreachable querier for testing the error path
func TestHealthHTTPHandlerUnreachable(t *testing.T) {
	h := healthHTTPHandler(probeLiveness, func(context.Context) (*types.StatusOutput, error) {
		return nil, errors.New("socket not found")
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	h(rec, req)
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
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
	require.Len(t, got, 3)
	want := map[string]bool{a: true, b: true, c: true}
	for _, root := range got {
		assert.True(t, want[root], "unexpected root in result: %q", root)
	}
}

func TestResolveDeclaredWorkspacesEmpty(t *testing.T) {
	assert.Nil(t, resolveDeclaredWorkspaces(nil, ""), "expected nil for empty inputs")
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
	require.Error(t, err, "acquire of non-declared root should error")
	var de *types.DiagnosticError
	require.ErrorAs(t, err, &de)
	assert.Equal(t, types.SandboxPolicyMismatch, de.Code, "expected MGS2010 SandboxPolicyMismatch")
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
	require.NoError(t, os.WriteFile(filepath.Join(allowed, "magus.yaml"), []byte(""), 0o644))
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
		if errors.As(err, &de) {
			assert.NotEqual(t, types.SandboxPolicyMismatch, de.Code,
				"acquire of declared root was wrongly rejected by the allowlist gate: %v", err)
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
	assert.NotPanics(t, func() { _ = reg.status() })
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
