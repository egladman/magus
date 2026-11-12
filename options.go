package magus

import (
	"github.com/egladman/magus/internal/workspace"
	"github.com/egladman/magus/types"
)

// WithJobReader has the guard's lease rules read job rows from r in place of
// the workspace's job store: a Go program embedding magus brings its own tasks
// as rows. The lease a call puts on its context (types.WithJobLease) names the
// row r answers for. Open fails on a nil r.
func WithJobReader(r types.JobReader) Option {
	return func(o *workspace.Load) { o.JobReader, o.JobReaderGiven = r, true }
}
