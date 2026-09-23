package job

import (
	"context"

	"github.com/egladman/magus/types"
)

// Snapshot is the job store as one reader already read it, pinned onto a context so code
// running inside that reader sees the same rows instead of opening the store again.
type Snapshot struct {
	Rows []types.Job
	// Err is why the store could not be read, nil when it could.
	Err error
}

type snapshotKey struct{}

// WithSnapshot pins snap onto ctx. A job-store member reached under it answers from the
// pinned rows and refuses every write.
func WithSnapshot(ctx context.Context, snap Snapshot) context.Context {
	return context.WithValue(ctx, snapshotKey{}, snap)
}

// SnapshotFromContext returns a copy of the pinned rows, and false when nothing is pinned.
func SnapshotFromContext(ctx context.Context) (Snapshot, bool) {
	snap, ok := ctx.Value(snapshotKey{}).(Snapshot)
	if !ok {
		return Snapshot{}, false
	}
	rows := make([]types.Job, len(snap.Rows))
	for i, row := range snap.Rows {
		rows[i] = row.Clone()
	}
	return Snapshot{Rows: rows, Err: snap.Err}, true
}

// HasSnapshot reports whether a snapshot is pinned, without cloning its rows: for a caller
// that only needs the presence check, such as a write refusal.
func HasSnapshot(ctx context.Context) bool {
	_, ok := ctx.Value(snapshotKey{}).(Snapshot)
	return ok
}
