package proc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// poolSocketRe matches the per-process pool filename Server.Start picks:
// magus-<pid>-<rand>.sock. server.sock and broker.sock sit outside it on purpose.
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

// StalePools lists live pool sockets under SockDir whose Status Version differs from
// selfVersion. An empty selfVersion disables the check. This process's own pool is
// skipped. Sockets that do not speak status are ignored (a bare listener is not a pool).
func StalePools(ctx context.Context, selfVersion string) ([]StalePool, error) {
	return StalePoolsIn(ctx, SockDir(), selfVersion)
}

// StalePoolsIn is StalePools over an explicit socket directory, for doctor checks that
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
