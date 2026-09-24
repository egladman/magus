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

// serverSocketName is the well-known socket filename `magus server` listens on. It sits
// outside the magus-*.sock pattern per-process servers use, as does the broker's, so a
// scan for per-process pools never mistakes either for one.
const serverSocketName = "server.sock"

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
// reclaims a stale path on EADDRINUSE: both correct, and between them they should be
// enough. They are not, because sockets are named magus-<pid>-<rand>.sock: every server
// picks a globally unique path, so the EADDRINUSE reclaim can never fire, and the only
// remaining mechanism is the shutdown unlink, which SIGKILL, a panic, a closed terminal,
// and a sleeping machine all skip. Unique naming is what opted this system out of
// bind-time reclaim, so it owes the filesystem a sweep instead. One development machine
// had accumulated 39 dead sockets with zero magus processes running.
//
// Discovery is the right place: it already dials every candidate to test liveness, so it
// knows which are dead and pays nothing extra to say so. Failure is ignored on purpose:
// a socket another process removed first, or one we cannot unlink, is not worth failing
// a discovery call over.
func reapDeadSocket(path string, e os.DirEntry) {
	info, err := e.Info()
	if err != nil || time.Since(info.ModTime()) < reapGracePeriod {
		return
	}
	_ = os.Remove(path)
}

// ServerSocketName returns the file basename of the server's socket.
func ServerSocketName() string { return serverSocketName }

// ServerDefaultAddr is where `magus server` listens when server.address sets nothing.
func ServerDefaultAddr() string { return "unix://" + filepath.Join(SockDir(), serverSocketName) }

// mcpSocketName is the unix socket `magus server` serves MCP on. Like the server's own, it
// sits outside the magus-*.sock pattern.
const mcpSocketName = "mcp.sock"

// MCPSocketPath is where `magus server` serves MCP over a unix socket, whatever
// server.address says.
func MCPSocketPath() string { return filepath.Join(SockDir(), mcpSocketName) }

// SocketLive reports whether a server is currently accepting on addr, which may be a
// unix:// URL or a bare socket path. It is the shared liveness probe behind idempotent
// `server start` (skip when one is already up) and `server stop` verification (confirm
// the server is actually gone after a shutdown request). A malformed address is treated
// as not-live rather than an error, since callers only care whether a server answers.
func SocketLive(ctx context.Context, addr string) bool {
	ep, err := endpoint.Parse(addr)
	if err != nil {
		return false
	}
	return isSocketLive(ctx, ep.Addr)
}

// LookupServerSocket returns the address of the server's default socket if a server
// answers there; bool is false when none does.
func LookupServerSocket(ctx context.Context) (string, bool) {
	path := filepath.Join(SockDir(), serverSocketName)
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

// DiscoverSocket scans SockDir for a live magus-*.sock file, preferring the server's
// socket. Used where exactly one server has to be chosen to talk to.
//
// The server's socket short-circuits the scan, so a machine running the server plus
// ad-hoc per-process servers still resolves to the server rather than reporting an
// ambiguity.
func DiscoverSocket(ctx context.Context) (string, error) {
	if addr, ok := LookupServerSocket(ctx); ok {
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

// DiscoverSockets returns every live proc-server address in SockDir, the server's socket
// first. Each one is a separate concurrency pool, so a reporter (`magus status`)
// enumerates them instead of demanding the caller pick one.
func DiscoverSockets(ctx context.Context) ([]string, error) {
	var candidates []string
	if addr, ok := LookupServerSocket(ctx); ok {
		candidates = append(candidates, addr)
	}

	dir := SockDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		// Only fatal when the scan is the sole source: with the server already found
		// there is still a pool to report.
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
		p := filepath.Join(dir, name)
		if isSocketLive(ctx, p) {
			// unix:// URL, matching LookupServerSocket's return format above;
			// functionally inert either way (endpoint.Parse accepts both back-compat),
			// but a caller comparing addresses across the two branches should not see
			// two different shapes for the same kind of thing.
			candidates = append(candidates, "unix://"+p)
			continue
		}
		reapDeadSocket(p, e)
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no running magus proc server found (start one with `magus server start`, or use --socket)")
	}
	return candidates, nil
}
