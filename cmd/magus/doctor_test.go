package main

import (
	"context"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/stretchr/testify/assert"
)

// TestBuildServerInfoRespectsServerDisabled pins that server.enabled=false stops the
// probe before it resolves an address, let alone dials one.
//
// The regression this guards is a silent one: the probe used to fall straight through to
// address resolution, which never consults the setting, so an invocation that had opted
// out of the server still reached whatever server the host happened to be running. It
// surfaced as a testscript failure: the suite sets MAGUS_SERVER_ENABLED=false to stay
// hermetic, and `magus doctor` then probed the real socket and reported on a bridge the
// test never asked about. It reproduced only where a server was actually up (CI), which
// is what made it hard to see.
//
// Server.Address is set deliberately: it is the one input resolveServerAddr honours
// without any discovery, so asserting SockAddr stays EMPTY proves the short-circuit fires
// ahead of address resolution. Without it the field is populated even when nothing
// answers, which is exactly the assertion that fails if the guard is removed. Testing it
// this way needs no live server, so the test discriminates on any machine, unlike a
// Reachable-only assertion, which passes vacuously wherever discovery finds nothing.
func TestBuildServerInfoRespectsServerDisabled(t *testing.T) {
	saved := globalCfg
	t.Cleanup(func() { globalCfg = saved })

	globalCfg = config.Config{}
	globalCfg.Server.Address = "unix:///nonexistent/magus-test.sock"
	globalCfg.Server.Enabled = false

	di := buildServerInfo(context.Background())

	assert.Empty(t, di.SockAddr, "a disabled server must not even resolve an address")
	assert.False(t, di.Reachable, "a disabled server must never be dialled")
	assert.Empty(t, di.Workspaces, "no workspaces are reported from a server that was not asked")
}
