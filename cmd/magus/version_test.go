package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/buildinfo"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types/daemon"
)

// TestProbeServerVersionWithNoServer pins the graceful half of the feature: an address
// nothing listens on yields an empty version and no error, fast enough that `magus
// version` stays a command a script can call in a loop.
func TestProbeServerVersionWithNoServer(t *testing.T) {
	defer snapshotGlobals()()
	globalCfg.Server.Address = fmt.Sprintf("unix://%s/magus-version-absent-test.sock", proc.SockDir())

	start := time.Now()
	assert.Equal(t, buildinfo.ServerProbe{}, probeServerVersion(context.Background()))
	assert.Less(t, time.Since(start), 2*serverProbeTimeout, "a dead socket must fail fast, not wait out the status deadline")
}

// TestProbeServerVersionReportsALiveServer drives the probe against a real proc server,
// the path `magus server status` takes, so the wiring is exercised, not mocked.
func TestProbeServerVersionReportsALiveServer(t *testing.T) {
	defer snapshotGlobals()()
	// Let proc pick a socket under SockDir; a t.TempDir() path can exceed the unix
	// socket path length limit on macOS.
	srv, err := proc.New(t.Context(), proc.Options{
		Version: "v9.9.9",
		Handler: func(context.Context, []string) error { return nil },
		Server:  func() *daemon.Server { return &daemon.Server{PID: 1} },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start(t.Context()))
	globalCfg.Server.Address = srv.Addr()

	assert.Equal(t, buildinfo.ServerProbe{Version: "v9.9.9"}, probeServerVersion(context.Background()))
}
