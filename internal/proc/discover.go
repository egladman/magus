package proc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/proc/endpoint"
)

// stableSocketName is the well-known socket filename used by `magus server start`.
const stableSocketName = "magus-daemon.sock"

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
			candidates = append(candidates, p)
		}
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
