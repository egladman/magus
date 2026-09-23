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

// Clone returns a copy whose rows share nothing with s, for a reader that may keep or
// edit them.
func (s Snapshot) Clone() Snapshot {
	rows := make([]types.Job, len(s.Rows))
	for i, row := range s.Rows {
		rows[i] = row.Clone()
	}
	return Snapshot{Rows: rows, Err: s.Err}
}

type snapshotKey struct{}

// WithSnapshot pins snap onto ctx. A job-store member reached under it answers from the
// pinned rows and refuses every write.
func WithSnapshot(ctx context.Context, snap Snapshot) context.Context {
	return context.WithValue(ctx, snapshotKey{}, snap)
}

// SnapshotFromContext returns the pinned snapshot, and false when nothing is pinned. Its
// rows are the pinned ones, shared with every other reader under ctx: read them, and
// Clone before handing them to code that may keep or edit them.
func SnapshotFromContext(ctx context.Context) (Snapshot, bool) {
	snap, ok := ctx.Value(snapshotKey{}).(Snapshot)
	return snap, ok
}

// HasSnapshot reports whether a snapshot is pinned.
func HasSnapshot(ctx context.Context) bool {
	_, ok := ctx.Value(snapshotKey{}).(Snapshot)
	return ok
}
