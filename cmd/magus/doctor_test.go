package main

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/stretchr/testify/assert"
)

// TestBuildDaemonInfoRespectsDaemonDisabled pins that daemon.enabled=false stops the
// probe before it resolves an address, let alone dials one.
//
// The regression this guards is a silent one: the probe used to fall straight through to
// resolveDaemonAddr, which never consults the setting, so an invocation that had opted
// out of the daemon still reached whatever daemon the host happened to be running. It
// surfaced as a testscript failure - the suite sets MAGUS_DAEMON_ENABLED=false to stay
// hermetic, and `magus doctor` then probed the real socket and reported on a bridge the
// test never asked about. It reproduced only where a daemon was actually up (CI), which
// is what made it hard to see.
//
// Daemon.Address is set deliberately: it is the one input resolveDaemonAddr honours
// without any discovery, so asserting SockAddr stays EMPTY proves the short-circuit fires
// ahead of address resolution. Without it the field is populated even when nothing
// answers, which is exactly the assertion that fails if the guard is removed. Testing it
// this way needs no live daemon, so the test discriminates on any machine - unlike a
// Reachable-only assertion, which passes vacuously wherever discovery finds nothing.
func TestBuildDaemonInfoRespectsDaemonDisabled(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

	globalCfg = config.Config{}
	globalCfg.Daemon.Address = "unix:///nonexistent/magus-test.sock"
	globalCfg.Daemon.Enabled = false

	di := buildDaemonInfo(context.Background())

	assert.Empty(t, di.SockAddr, "a disabled daemon must not even resolve an address")
	assert.False(t, di.Reachable, "a disabled daemon must never be dialled")
	assert.Empty(t, di.Workspaces, "no workspaces are reported from a daemon that was not asked")
}
