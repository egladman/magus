package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/proc"
)

// resolveUnder maps every path under a workspace in roots to that workspace, nearest
// first, the way magus.FindRoot treats a worktree nested inside its parent checkout.
func resolveUnder(roots ...string) func(string) (string, error) {
	return func(dir string) (string, error) {
		for _, r := range roots {
			if rel, err := filepath.Rel(r, dir); err == nil && !strings.HasPrefix(rel, "..") {
				return r, nil
			}
		}
		return "", errors.New("no workspace")
	}
}

func TestFindSync(t *testing.T) {
	const repo, worktree = "/src/magus", "/src/magus/.claude/worktrees/w1"
	resolve := resolveUnder(worktree, repo)
	st := &proc.StatusReply{ParentPID: 4242, Calls: []proc.Call{
		{Args: []string{"run", "build"}, Workspace: repo, Inv: "inv-run"},
		{Args: []string{"graph", "build"}, Workspace: worktree, Inv: "inv-worktree"},
		{Args: []string{"graph", "build"}, Workspace: repo + "/libs/api", Inv: "inv-repo"},
	}}

	got, ok := FindSync(st, repo, resolve)
	require.True(t, ok)
	assert.Equal(t, InFlightSync{Call: st.Calls[2], PID: 4242}, got, "a job submitted from a subdirectory is the repo's; the nested worktree's is not")

	got, ok = FindSync(st, worktree, resolve)
	require.True(t, ok)
	assert.Equal(t, "inv-worktree", got.Call.Inv)

	_, ok = FindSync(st, "/elsewhere", resolve)
	assert.False(t, ok)
	_, ok = FindSync(nil, repo, resolve)
	assert.False(t, ok)
}

func TestAwaitSync(t *testing.T) {
	running := &proc.StatusReply{Calls: []proc.Call{{Inv: "a"}}}
	done := &proc.StatusReply{Calls: []proc.Call{{Inv: "b"}}}

	t.Run("returns once the job leaves the status", func(t *testing.T) {
		replies := []*proc.StatusReply{running, running, done}
		calls := 0
		err := AwaitSync(t.Context(), func(context.Context) (*proc.StatusReply, error) {
			r := replies[calls]
			calls++
			return r, nil
		}, "a", time.Millisecond)
		require.NoError(t, err)
		assert.Equal(t, 3, calls)
	})
	t.Run("a server that stops answering is ErrServerGone", func(t *testing.T) {
		err := AwaitSync(t.Context(), func(context.Context) (*proc.StatusReply, error) {
			return nil, errors.New("dial: no such file")
		}, "a", time.Millisecond)
		require.ErrorIs(t, err, ErrServerGone)
	})
	t.Run("cancellation stops the wait", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		err := AwaitSync(ctx, func(context.Context) (*proc.StatusReply, error) {
			cancel()
			return running, nil
		}, "a", time.Hour)
		require.ErrorIs(t, err, context.Canceled)
	})
}

// The status a real proc server reports for a submitted sync-graph is what FindSync and
// AwaitSync read: the job's argv, the submitter's directory and its invocation id.
func TestSyncJobThroughProcServer(t *testing.T) {
	release := make(chan struct{})
	srv, err := proc.New(proc.Options{
		Handler: func(ctx context.Context, _ []string) error {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return nil
		},
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())
	addr := srv.Addr()

	inv, err := proc.SubmitJob(t.Context(), addr, []string{"graph", "build"}, "")
	require.NoError(t, err)
	require.NotEmpty(t, inv)
	cwd, err := os.Getwd()
	require.NoError(t, err)

	status := func(ctx context.Context) (*proc.StatusReply, error) { return proc.QueryStatus(ctx, addr) }
	st, err := status(t.Context())
	require.NoError(t, err)
	got, ok := FindSync(st, cwd, resolveUnder(cwd))
	require.True(t, ok, "calls: %+v", st.Calls)
	assert.Equal(t, inv, got.Call.Inv)
	assert.Equal(t, os.Getpid(), got.PID)

	again, err := proc.SubmitJob(t.Context(), addr, []string{"graph", "build"}, "")
	require.NoError(t, err)
	assert.Empty(t, again, "a second submit coalesces into the running job")

	waited := make(chan error, 1)
	go func() { waited <- AwaitSync(t.Context(), status, inv, 5*time.Millisecond) }()
	select {
	case err := <-waited:
		t.Fatalf("AwaitSync returned while the job still ran: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-waited:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("AwaitSync did not return after the job finished")
	}
}

func TestSyncRequestRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "knowledge")
	_, ok, err := ReadSyncRequest(dir)
	require.NoError(t, err)
	assert.False(t, ok)

	want := SyncRequest{At: time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC), Outcome: SyncSubmitted, Job: "inv1"}
	require.NoError(t, RecordSyncRequest(dir, want))
	got, ok, err := ReadSyncRequest(dir)
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, want.At.Equal(got.At))
	got.At = want.At
	assert.Equal(t, want, got)
}

func TestDiagnoseSync(t *testing.T) {
	cmds := Commands{GraphBuild: "magus graph build", ServerStart: "magus server start", ServerStop: "magus server stop", JobRunSync: "magus job run sync-graph"}
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	built := now.Add(-time.Hour)
	hook := SyncObservation{Now: now, HookChecked: true, HookCommand: "./magus job run sync-graph", HookBinary: "./magus", HookRunnable: true, IndexBuilt: built, Version: "v1"}
	with := func(f func(*SyncObservation)) SyncObservation {
		o := hook
		f(&o)
		return o
	}
	cases := []struct {
		name         string
		obs          SyncObservation
		cause, fixes string
	}{
		{
			name: "a sync running now explains everything",
			obs: with(func(o *SyncObservation) {
				o.ServerLive = true
				o.InFlight = &InFlightSync{Call: proc.Call{Inv: "inv9", StartedAt: now.Add(-12 * time.Second)}, PID: 77}
			}),
			cause: "the server's sync-graph job inv9 is indexing this checkout now (server pid 77, running 12s)",
			fixes: "`magus graph build` waits for it",
		},
		{
			name:  "no hook installed",
			obs:   with(func(o *SyncObservation) { o.HookCommand = "" }),
			cause: "no VCS refresh hook is installed",
			fixes: "`magus server start` from this checkout installs it",
		},
		{
			name:  "the hook's binary is absent",
			obs:   with(func(o *SyncObservation) { o.HookRunnable = false }),
			cause: "the refresh hook runs `./magus job run sync-graph` and ./magus does not exist",
			fixes: "put ./magus in place",
		},
		{
			name:  "a VCS whose hooks magus cannot read skips the hook checks",
			obs:   with(func(o *SyncObservation) { o.HookChecked, o.HookCommand = false, "" }),
			cause: "no server is running",
			fixes: "`magus server start` keeps it current",
		},
		{
			name: "the hook ran with no server",
			obs: with(func(o *SyncObservation) {
				o.LastRequest = &SyncRequest{At: now.Add(-time.Minute), Outcome: SyncNoServer}
			}),
			cause: "no server was running at the last sync request (`magus job run sync-graph`, from the refresh hook or by hand, at",
			fixes: "`magus graph build` indexes it once now",
		},
		{
			name: "a request older than the index says nothing about it",
			obs: with(func(o *SyncObservation) {
				o.LastRequest = &SyncRequest{At: built.Add(-time.Minute), Outcome: SyncNoServer}
			}),
			cause: "no server is running, so the refresh hook's `magus job run sync-graph` does nothing",
		},
		{
			name: "a refused request names the refusal",
			obs: with(func(o *SyncObservation) {
				o.ServerLive = true
				o.LastRequest = &SyncRequest{At: now.Add(-time.Minute), Outcome: SyncRefused, Detail: "proc: version mismatch"}
			}),
			cause: "the server refused the sync requested at",
			fixes: "`magus server stop` then `magus server start`",
		},
		{
			name:  "a server on another build",
			obs:   with(func(o *SyncObservation) { o.ServerLive, o.ServerVersion, o.ServerPID = true, "v0", 12 }),
			cause: "the server (pid 12) is v0 and this checkout's magus is v1",
			fixes: "`magus server stop` then `magus server start`",
		},
		{
			name: "a taken request that left no index",
			obs: with(func(o *SyncObservation) {
				o.ServerLive, o.ServerVersion = true, "v1"
				o.LastRequest = &SyncRequest{At: now.Add(-time.Minute), Outcome: SyncSubmitted, Job: "inv3"}
			}),
			cause: "(job inv3) and the index is still not current",
			fixes: "`magus graph build` here builds it",
		},
		{
			name:  "a server with nothing asked of it",
			obs:   with(func(o *SyncObservation) { o.ServerLive, o.ServerVersion = true, "v1" }),
			cause: "a server is running, but no sync has been asked for",
			fixes: "`magus job run sync-graph` asks the server",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := DiagnoseSync(tc.obs, cmds)
			assert.Contains(t, got.Cause, tc.cause)
			assert.Contains(t, got.Remedy, tc.fixes)
		})
	}
}

func TestHookBinary(t *testing.T) {
	top := t.TempDir()
	bin, ok := HookBinary(top, "./magus job run sync-graph")
	assert.Equal(t, "./magus", bin)
	assert.False(t, ok, "no ./magus in the checkout yet")

	require.NoError(t, os.WriteFile(filepath.Join(top, "magus"), []byte("#!/bin/sh\n"), 0o755))
	_, ok = HookBinary(top, "./magus job run sync-graph")
	assert.True(t, ok)

	_, ok = HookBinary(top, "magus-no-such-binary-on-path job run sync-graph")
	assert.False(t, ok)
	_, ok = HookBinary(top, "")
	assert.False(t, ok)
}
