package magus

import (
	"context"
	"os"
	"strings"

	"github.com/egladman/magus/internal/wslock"
	"github.com/egladman/magus/types"
)

// noWaitLocks reports whether MAGUS_NO_WAIT asks a contended workspace lock to
// fail fast instead of blocking.
func noWaitLocks() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MAGUS_NO_WAIT"))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// acquireProjectLocks takes the per-project EXCLUSIVE workspace lock for every
// project this invocation will mutate, in canonical sorted order (deadlock-safe),
// and returns a single release func.
//
// The lock is held ONCE for the whole invocation, at the boundary where the
// invocation begins mutating the project set - NOT around each target. The
// intra-process target scheduler fans out beneath the held lock and never
// contends on it (it is the same lock-holding process); the lock's only job is to
// keep a SEPARATE magus process from mutating the same project concurrently. This
// is the complement of the per-target `exclusive` scheduling policy, which is a
// different, intra-process concern and is left untouched.
func (m *Magus) acquireProjectLocks(ctx context.Context, projects []*types.Project) (func(), error) {
	paths := make([]string, 0, len(projects))
	for _, p := range projects {
		paths = append(paths, p.Path)
	}
	locker := wslock.New(resolveCacheDir(m.ws.Root, m.cfg), noWaitLocks())
	return locker.AcquireAll(ctx, paths)
}
