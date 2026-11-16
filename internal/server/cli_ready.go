package server

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/proc"
	sysPID "github.com/egladman/magus/internal/sys/pid"
)

// ReapSpawned kills a server this process spawned but never got a working socket out of.
// Without it every failure path leaks the process: with console.enabled=false the wait
// times out by construction, so each attempt left one more running.
//
// It checks WHO it is about to kill. The pid was Release()d to detach the child, so
// this process is not its parent any more and the number is free for reuse the moment
// it exits, and the callers reach here precisely when something went wrong, which is
// when that is likeliest. A server answering on sock under a different pid means ours
// is already gone and the number belongs to somebody else now. An empty sock skips that
// half and checks liveness only, for a caller with no socket address to ask.
//
// os.FindProcess rather than the spawned handle, which was Release()d.
func ReapSpawned(ctx context.Context, pid int, sock string) {
	if !sysPID.Alive(pid) {
		return
	}
	if sock != "" {
		if st, err := proc.QueryStatus(ctx, sock); err == nil && st.ParentPID != 0 && st.ParentPID != pid {
			// Somebody else's server owns the socket, so ours exited and this pid is no
			// longer ours to kill.
			return
		}
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return
	}
	_ = p.Kill()
}

// waitServerReady polls the server socket until it answers a status query or the timeout
// elapses. A successful status round-trip is the readiness signal: the socket is bound and
// the server is accepting, so a script that chained on `server start` can proceed.
func waitServerReady(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := proc.QueryStatus(ctx, addr); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("server did not become ready within %s", timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// IsServerRun reports whether a `server` argv runs a server, here or detached: `start`
// without a help flag, or the detached child's `--foreground`.
func IsServerRun(subArgs []string) bool {
	if len(subArgs) == 0 {
		return false
	}
	switch subArgs[0] {
	case hint.ServerStart.Leaf():
		return !isServerStartHelp(subArgs)
	case "-foreground", "--foreground":
		return true
	}
	return false
}

// isServerStartHelp reports whether `server start` was invoked only to print usage. subArgs
// starts with "start"; a help token after it means the caller wants the flag list, not a
// server, so startup skips the auto-background handoff and lets the normal dispatch print it.
func isServerStartHelp(subArgs []string) bool {
	for _, a := range subArgs[1:] {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}

// WantsForeground detects --foreground before the formal flag parse, so the
// backgrounding decision is made in startup().
func WantsForeground(args []string) bool {
	for _, a := range args {
		if a == "-foreground" || a == "--foreground" {
			return true
		}
	}
	return false
}
