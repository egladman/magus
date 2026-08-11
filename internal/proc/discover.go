package proc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/proc/endpoint"
)

// stableSocketName is the well-known socket filename used by `magus server start`.
const stableSocketName = "magus-daemon.sock"

// reapGracePeriod is how long a dead socket must have sat untouched before a sweep
// unlinks it. The window closes a narrow race: a server that has created its socket
// file but not yet called listen would fail our liveness dial, and removing it there
// would delete a path a live process is about to bind. Nothing legitimate leaves a
// socket dead for minutes, so the guard costs nothing and removes the hazard.
const reapGracePeriod = 5 * time.Minute

// reapDeadSocket unlinks a socket that failed the liveness probe.
//
// A unix socket is a filesystem entry and does not disappear when its process dies, so
// something has to remove it. Server.Close unlinks on clean shutdown and Server.Start
// reclaims a stale path on EADDRINUSE - both correct, and between them they should be
// enough. They are not, because sockets are named magus-<pid>-<rand>.sock: every server
// picks a globally unique path, so the EADDRINUSE reclaim can never fire, and the only
// remaining mechanism is the shutdown unlink, which SIGKILL, a panic, a closed terminal,
// and a sleeping machine all skip. Unique naming is what opted this system out of
// bind-time reclaim, so it owes the filesystem a sweep instead. One development machine
// had accumulated 39 dead sockets with zero magus processes running.
//
// Discovery is the right place: it already dials every candidate to test liveness, so it
// knows which are dead and pays nothing extra to say so. Failure is ignored on purpose -
// a socket another process removed first, or one we cannot unlink, is not worth failing
// a discovery call over.
func reapDeadSocket(path string, e os.DirEntry) {
	info, err := e.Info()
	if err != nil || time.Since(info.ModTime()) < reapGracePeriod {
		return
	}
	_ = os.Remove(path)
}

// StableSocketName returns the file basename of the stable multi-workspace daemon socket.
func StableSocketName() string { return stableSocketName }

// SocketLive reports whether a daemon is currently accepting on addr, which may be a
// unix:// URL or a bare socket path. It is the shared liveness probe behind idempotent
// `server start` (skip when one is already up) and `server stop` verification (confirm
// the daemon is actually gone after a shutdown request). A malformed address is treated
// as not-live rather than an error, since callers only care whether a daemon answers.
func SocketLive(ctx context.Context, addr string) bool {
	ep, err := endpoint.ParseEndpoint(addr)
	if err != nil {
		return false
	}
	return isSocketLive(ctx, ep.Addr)
}

// LookupStableSocket returns the address of the stable daemon socket if alive; bool is false when absent.
func LookupStableSocket(ctx context.Context) (string, bool) {
	path := filepath.Join(SockDir(), stableSocketName)
	if !isSocketLive(ctx, path) {
		return "", false
	}
	return "unix://" + path, true
}

// DiscoverSocket scans SockDir for a live magus-*.sock file, preferring the stable daemon socket.
// Used by `magus status` when no explicit --socket flag is given.
func DiscoverSocket(ctx context.Context) (string, error) {
	if addr, ok := LookupStableSocket(ctx); ok {
		return addr, nil
	}

	dir := SockDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("proc: discover: scan %s: %w", dir, err)
	}

	var candidates []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "magus-") || !strings.HasSuffix(name, ".sock") {
			continue
		}
		// Skip the stable socket — already checked above.
		if name == stableSocketName {
			continue
		}
		p := filepath.Join(dir, name)
		if isSocketLive(ctx, p) {
			// unix:// URL, matching LookupStableSocket's return format above -
			// functionally inert either way (endpoint.Parse accepts both back-compat),
			// but a caller comparing addresses across the two branches should not see
			// two different shapes for the same kind of thing.
			candidates = append(candidates, "unix://"+p)
			continue
		}
		reapDeadSocket(p, e)
	}

	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("no running magus proc server found (set MAGUS_DAEMON_SOCKET or use --socket)")
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("multiple proc servers found; use --socket to select one (%s)", strings.Join(candidates, ", "))
	}
}
