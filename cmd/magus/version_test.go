package main

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unknownVersion's doc says to keep it in sync with proc.devVersionSentinel (see
// TestDevVersionSentinelMatchesWard in internal/proc). This package can see proc's package
// but not its unexported sentinel, so it proves its half through the same shared anchor,
// ward.DevVersion, rather than comparing the two directly.
func TestUnknownVersionMatchesWardDevVersion(t *testing.T) {
	assert.Equal(t, ward.DevVersion, unknownVersion)
}

func TestServerLine(t *testing.T) {
	cases := []struct {
		name   string
		probe  serverProbe
		client string
		want   string
	}{
		{"no server answered", serverProbe{}, "v1.2.3", "server: not running"},
		{"same build on both ends", serverProbe{version: "v1.2.3"}, "v1.2.3", "server: v1.2.3"},
		{
			"a server left running across an upgrade",
			serverProbe{version: "v1.2.0"}, "v1.2.3",
			"server: v1.2.0 (differs from this client)",
		},
		{
			// The case a plain comparison got wrong: an unstamped client reports the same
			// sentinel, so "server: unknown" read as two matching builds.
			"a server that did not report a version, against an unstamped client",
			serverProbe{version: unknownVersion}, unknownVersion,
			"server: running, version not reported",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, serverLine(c.probe, c.client))
		})
	}
}

// TestProbeServerVersionWithNoServer pins the graceful half of the feature: an address
// nothing listens on yields an empty version and no error, fast enough that `magus
// version` stays a command a script can call in a loop.
func TestProbeServerVersionWithNoServer(t *testing.T) {
	defer snapshotGlobals()()
	globalCfg.Server.Address = "unix://" + proc.SockDir() + "/magus-version-absent-test.sock"

	start := time.Now()
	assert.Equal(t, serverProbe{}, probeServerVersion(context.Background()))
	assert.Less(t, time.Since(start), 2*serverProbeTimeout, "a dead socket must fail fast, not wait out the status deadline")
}

// TestProbeServerVersionReportsALiveServer drives the probe against a real proc server,
// the same path `magus status` takes, so the wiring is exercised rather than mocked.
func TestProbeServerVersionReportsALiveServer(t *testing.T) {
	defer snapshotGlobals()()
	// Let proc pick a socket under SockDir; a t.TempDir() path can exceed the unix
	// socket path length limit on macOS.
	srv, err := proc.New(proc.Options{
		Version: "v9.9.9",
		Handler: func(context.Context, []string) error { return nil },
		Server:  func() *types.StatusServer { return &types.StatusServer{PID: 1} },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())
	globalCfg.Server.Address = srv.Addr()

	assert.Equal(t, serverProbe{version: "v9.9.9"}, probeServerVersion(context.Background()))
}
