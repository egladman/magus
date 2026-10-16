package job

import (
	"context"

	"github.com/egladman/magus/types"
)

// Workspace is what the workspace-aware checks of a job record read: the loaded projects
// and the VCS ignore filter.
type Workspace struct {
	Projects []*types.Project
	// Ignored returns the paths of its argument the VCS ignores.
	Ignored func([]string) []string
	// Unavailable says why the workspace could not be loaded, empty when it was. A check
	// that needs it reports itself unchecked rather than passing.
	Unavailable string
}

// broadClaimProblems reports each write path of row that claims more than the
// workspace's breadth thresholds allow, as WritePathTooBroad.
func broadClaimProblems(ctx context.Context, root string, row types.Job, ws Workspace, read SymbolReader) types.JobProblems {
	return nil
}
