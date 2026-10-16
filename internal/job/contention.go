package job

import (
	"context"

	"github.com/egladman/magus/types"
)

// UnusedWritePaths returns the write paths of holder its footprint has not touched, the
// ones it could release to a job refused for wanting them.
func UnusedWritePaths(ctx context.Context, store *Store, rows []types.Job, holder types.Job) []string {
	return nil
}
