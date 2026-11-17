package server

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/proc"
)

// TestServerChildArgsDropsStart pins the detached server's argv: its role, with no
// `start` in ps, and every flag the person passed so it binds where the parent waits.
func TestServerChildArgsDropsStart(t *testing.T) {
	assert.Equal(t, []string{"server", "--foreground"}, serverChildArgs([]string{"server", "start"}))
	assert.Equal(t,
		[]string{"--server-address", "unix:///tmp/m.sock", "server", "--foreground", "-v"},
		serverChildArgs([]string{"--server-address", "unix:///tmp/m.sock", "server", "start", "-v"}))
}

// The message exists because "already running" answered a question nobody asked. A second
// worktree's `server start` returns 0 having loaded nothing from that tree, and the console then
// shows the tree the server was started in, which reads as success.
func TestServingSuffixNamesTheLoadedWorkspaces(t *testing.T) {
	st := &proc.StatusReply{Workspaces: []proc.Workspace{
		{Root: "/repo/worktrees/b"},
		{Root: "/repo"},
	}}

	// Sorted, so two runs of the same server do not print the list two ways.
	assert.Equal(t, ", serving /repo, /repo/worktrees/b", servingSuffix(st))

	// A server that has loaded nothing yet says nothing rather than "serving " with an empty
	// list, which would read as a server that is serving something unnameable.
	assert.Empty(t, servingSuffix(&proc.StatusReply{}))
}
