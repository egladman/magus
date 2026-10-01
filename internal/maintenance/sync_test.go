package maintenance

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	hook := SyncObservation{HookChecked: true, HookCommand: "./magus job run sync-graph", HookBinary: "./magus", HookRunnable: true, IndexBuilt: built, Version: "v1"}
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
			name: "a build running now explains everything",
			obs: with(func(o *SyncObservation) {
				o.ServerLive = true
				o.Building = &GraphBuildHolder{PID: 77, Started: now.Add(-12 * time.Second), By: "the server's sync-graph job"}
			}),
			cause: "the server's sync-graph job (pid 77, started ",
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
			assert.Contains(t, got.Why, tc.cause)
			assert.Contains(t, got.Fix, tc.fixes)
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
