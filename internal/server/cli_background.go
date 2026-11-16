package server

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/log/attr"
	"github.com/egladman/magus/internal/proc"
)

// serverReadyTimeout bounds how long the backgrounding parent waits for the detached child
// to start accepting on the socket before it reports the start as failed.
const serverReadyTimeout = 60 * time.Second

// StartBackground implements the default auto-backgrounding of `server start`. It
// runs in the launching process before the server is built. It returns done==true when it
// fully handled the request (the caller returns exitCode without building a server): one
// was already running (idempotent no-op), a detached child was spawned and became ready,
// or spawning failed. It returns done==false for a foreground server: an explicit
// `server start --foreground`, or the detached `server --foreground` child.
func StartBackground(ctx context.Context, cfg config.Config, subArgs []string, logPath string, argv []string) (exitCode int, done bool) {
	if WantsForeground(subArgs) {
		return 0, false
	}

	addr := cfg.Server.Address // startup defaulted this to server.sock
	// Idempotent start: a server already accepting on the socket means there is nothing to do.
	if proc.SocketLive(ctx, addr) {
		if st, err := proc.QueryStatus(ctx, addr); err == nil && st.ParentPID != 0 {
			slog.InfoContext(ctx, fmt.Sprintf("server already running (pid %d) on %s%s", st.ParentPID, addr, ServingSuffix(st)),
				attr.Notice(""), attr.Component("magus"))
		} else {
			slog.InfoContext(ctx, "server already running on "+addr, attr.Notice(""), attr.Component("magus"))
		}
		return 0, true
	}

	pid, err := proc.SpawnDetached(ServerChildArgs(argv), logPath)
	if err != nil {
		slog.With(attr.Component("server start")).ErrorContext(ctx, "could not background the server", attr.Error(err))
		return 1, true
	}
	if err := WaitServerReady(ctx, addr, serverReadyTimeout); err != nil {
		slog.ErrorContext(ctx, fmt.Sprintf("server (pid %d) did not become ready within %s; see %s", pid, serverReadyTimeout, logPath),
			attr.Notice(""), attr.Component("magus"))
		return 1, true
	}
	slog.InfoContext(ctx, fmt.Sprintf("server started (pid %d) on %s; logs at %s", pid, addr, logPath), attr.Notice(""), attr.Component("magus"))
	return 0, true
}
