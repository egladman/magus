package proc

import (
	"context"
	"errors"
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
	ep, err := endpoint.Parse(addr)
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

// ErrMultipleServers reports that discovery found several live proc servers and will not
// choose between them. A sentinel because a caller has to tell it apart from "nothing is
// running": several candidates and none send a reader somewhere different, and folding the
// first into the second reports a busy machine as an idle one.
var ErrMultipleServers = errors.New("multiple proc servers found; use --socket to select one")

// DiscoverSocket scans SockDir for a live magus-*.sock file, preferring the stable daemon
// socket. Used where exactly one server has to be chosen to talk to.
//
// The stable socket short-circuits the scan, so a machine running the daemon plus ad-hoc
// per-process servers still resolves to the daemon rather than reporting an ambiguity.
func DiscoverSocket(ctx context.Context) (string, error) {
	if addr, ok := LookupStableSocket(ctx); ok {
		return addr, nil
	}
	addrs, err := DiscoverSockets(ctx)
	if err != nil {
		return "", err
	}
	if len(addrs) > 1 {
		return "", fmt.Errorf("%w (%s)", ErrMultipleServers, strings.Join(addrs, ", "))
	}
	return addrs[0], nil
}

// DiscoverSockets returns every live proc-server address in SockDir, the stable daemon
// socket first. Each one is a separate concurrency pool, so a reporter (`magus status`)
// enumerates them instead of demanding the caller pick one.
func DiscoverSockets(ctx context.Context) ([]string, error) {
	var candidates []string
	if addr, ok := LookupStableSocket(ctx); ok {
		candidates = append(candidates, addr)
	}

	dir := SockDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Only fatal when the scan is the sole source: with the stable daemon already
		// found there is still a pool to report.
		if len(candidates) > 0 {
			return candidates, nil
		}
		return nil, fmt.Errorf("proc: discover: scan %s: %w", dir, err)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "magus-") || !strings.HasSuffix(name, ".sock") {
			continue
		}
		// Skip the stable socket - already probed above.
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

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no running magus proc server found (set MAGUS_DAEMON_SOCKET or use --socket)")
	}
	return candidates, nil
}
