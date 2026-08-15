package main

import (
	"context"
	"testing"
	"time"

	"github.com/egladman/magus/internal/proc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonLine(t *testing.T) {
	cases := []struct {
		name string
		out  versionOutput
		want string
	}{
		{"no daemon answered", versionOutput{Version: "v1.2.3"}, "daemon: not running"},
		{"same build on both ends", versionOutput{Version: "v1.2.3", Daemon: "v1.2.3"}, "daemon: v1.2.3"},
		{
			"a daemon left running across an upgrade",
			versionOutput{Version: "v1.2.3", Daemon: "v1.2.0"},
			"daemon: v1.2.0 (differs from this client)",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, daemonLine(c.out))
		})
	}
}

// TestProbeDaemonVersionWithNoDaemon pins the graceful half of the feature: an address
// nothing listens on yields an empty version and no error, fast enough that `magus
// version` stays a command a script can call in a loop.
func TestProbeDaemonVersionWithNoDaemon(t *testing.T) {
	defer snapshotGlobals()()
	globalCfg.Daemon.Address = "unix://" + proc.SockDir() + "/magus-version-absent-test.sock"

	start := time.Now()
	assert.Empty(t, probeDaemonVersion(context.Background()))
	assert.Less(t, time.Since(start), 2*daemonProbeTimeout, "a dead socket must fail fast, not wait out the status deadline")
}

// TestProbeDaemonVersionReportsALiveDaemon drives the probe against a real proc server,
// the same path `magus status` takes, so the wiring is exercised rather than mocked.
func TestProbeDaemonVersionReportsALiveDaemon(t *testing.T) {
	defer snapshotGlobals()()
	// Let proc pick a socket under SockDir; a t.TempDir() path can exceed the unix
	// socket path length limit on macOS.
	srv, err := proc.New(proc.Options{
		Version: "v9.9.9",
		Handler: func(context.Context, []string) error { return nil },
	})
	require.NoError(t, err)
	defer srv.Close()
	require.NoError(t, srv.Start())
	globalCfg.Daemon.Address = srv.Addr()

	assert.Equal(t, "v9.9.9", probeDaemonVersion(context.Background()))
}
