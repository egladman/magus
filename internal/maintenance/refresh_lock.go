package maintenance

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/egladman/magus/internal/log/attr"
)

// RefreshLock is one graph build lock: the machine's or a store's.
type RefreshLock struct {
	acquire func(context.Context, GraphBuildHolder, io.Writer, time.Duration) (*GraphBuildLock, *GraphBuildHolder, error)
	try     func(GraphBuildHolder, io.Writer) (*GraphBuildLock, *GraphBuildHolder, error)
}

// MachineRefreshLock is the lock one machine runs one graph build under.
func MachineRefreshLock() RefreshLock {
	return RefreshLock{acquire: acquireMachineGraphBuild, try: TryMachineGraphBuild}
}

// StoreRefreshLock is the lock the knowledge store at dir builds under.
func StoreRefreshLock(dir string) RefreshLock {
	return RefreshLock{
		acquire: func(ctx context.Context, me GraphBuildHolder, w io.Writer, every time.Duration) (*GraphBuildLock, *GraphBuildHolder, error) {
			return acquireGraphBuild(ctx, dir, me, w, every)
		},
		try: func(me GraphBuildHolder, w io.Writer) (*GraphBuildLock, *GraphBuildHolder, error) {
			return tryGraphBuild(dir, me, w)
		},
	}
}

// TakeRefreshLock takes a graph build lock for a refresh, writing the build's progress to
// notices and polling a held lock every poll. A detached refresh never queues: a nil lock
// and nil error mean it should stop.
func TakeRefreshLock(ctx context.Context, take RefreshLock, me GraphBuildHolder, verb string, detached bool, notices io.Writer, poll time.Duration) (*GraphBuildLock, error) {
	if !detached {
		lock, _, err := take.acquire(ctx, me, notices, poll)
		return lock, err
	}
	lock, holder, err := take.try(me, notices)
	if holder != nil {
		slog.InfoContext(ctx, fmt.Sprintf("%s holds the refresh lock, so this refresh stops", holder), attr.Notice(verb))
	}
	return lock, err
}
