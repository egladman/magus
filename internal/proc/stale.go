package proc

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/egladman/magus/internal/log/attr"
)

// poolSocketRe matches the per-process pool filename Server.Start picks:
// magus-<pid>-<rand>.sock. server.sock and the broker's socket sit outside it.
var poolSocketRe = regexp.MustCompile(`^magus-(\d+)-[^/]*\.sock$`)

// StalePool is a live per-process pool whose display version differs from the binary
// asking. Typical parents are a leftover `magus mcp` or a run that outlived a rebuild:
// DiscoverSockets already reaps dead sockets, so this is the live-but-wrong-build case.
type StalePool struct {
	Addr      string // unix://… form, matching DiscoverSockets
	ParentPID int
	Version   string
}

// IsPoolSocketName reports whether name is a per-process pool socket basename.
func IsPoolSocketName(name string) bool {
	return poolSocketRe.MatchString(name)
}

// StalePoolsIn is stalePools over an explicit socket directory, for doctor checks that
// already know the directory they are auditing.
func StalePoolsIn(ctx context.Context, dir, selfVersion string) ([]StalePool, error) {
	if selfVersion == "" || dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("proc: stale pools: scan %s: %w", dir, err)
	}
	self := os.Getpid()
	var out []StalePool
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		m := poolSocketRe.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		pid, _ := strconv.Atoi(m[1])
		if pid == self {
			continue
		}
		path := filepath.Join(dir, name)
		if !isSocketLive(ctx, path) {
			continue
		}
		addr := "unix://" + path
		reply, qerr := QueryStatus(ctx, addr)
		if qerr != nil || reply == nil || reply.Version == "" {
			continue
		}
		if reply.Version == selfVersion {
			continue
		}
		out = append(out, StalePool{
			Addr:      addr,
			ParentPID: reply.ParentPID,
			Version:   reply.Version,
		})
	}
	return out, nil
}

// PoolSocketPath returns the filesystem path inside a unix:// pool address, or "".
// server.sock and other non-pool sockets yield "" so callers can tell a leftover
// magus-<pid>-*.sock parent from the persistent server.
func PoolSocketPath(addr string) string {
	addr = strings.TrimSpace(addr)
	var path string
	switch {
	case strings.HasPrefix(addr, "unix://"):
		path = strings.TrimPrefix(addr, "unix://")
	case strings.HasPrefix(addr, "/"):
		path = addr
	default:
		return ""
	}
	if !IsPoolSocketName(filepath.Base(path)) {
		return ""
	}
	return path
}

// StopTimeout bounds how long a stop waits to confirm a server or broker is gone after a
// shutdown request. In-flight builds drain first, so allow generous headroom before
// declaring the stop unverified.
const StopTimeout = 30 * time.Second

// WaitSocketGone polls addr until it stops answering or the timeout elapses. It is the
// verification half of a stop: a shutdown reply only means the request was accepted, so
// stop confirms the socket has actually gone quiet before reporting success.
func WaitSocketGone(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if !SocketLive(ctx, addr) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s still answers after %s", addr, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// StopStalePools shuts down live per-process pool parents whose display version
// differs from selfVersion. Same Shutdown + WaitSocketGone path as a server stop; the
// parents are leftover magus mcp / run processes, not the persistent server.
func StopStalePools(ctx context.Context, selfVersion string) error {
	stale, err := StalePoolsIn(ctx, SockDir(), selfVersion)
	if err != nil {
		return fmt.Errorf("server stop --pools: %w", err)
	}
	if len(stale) == 0 {
		slog.InfoContext(ctx, "no stale pool parents", attr.Notice(""), attr.Component("magus"))
		return nil
	}
	var failed int
	for _, p := range stale {
		if err := Shutdown(ctx, p.Addr); err != nil {
			slog.ErrorContext(ctx, fmt.Sprintf("stop pool parent pid %d (%s)", p.ParentPID, p.Version), attr.Notice(""), attr.Component("magus"), attr.Error(err))
			failed++
			continue
		}
		if err := WaitSocketGone(ctx, p.Addr, StopTimeout); err != nil {
			slog.ErrorContext(ctx, fmt.Sprintf("stop pool parent pid %d (%s)", p.ParentPID, p.Version), attr.Notice(""), attr.Component("magus"), attr.Error(err))
			failed++
			continue
		}
		slog.InfoContext(ctx, fmt.Sprintf("stopped pool parent (pid %d, %s)", p.ParentPID, p.Version), attr.Notice(""), attr.Component("magus"))
	}
	if failed > 0 {
		return fmt.Errorf("server stop --pools: %d of %d parent(s) did not stop", failed, len(stale))
	}
	return nil
}
