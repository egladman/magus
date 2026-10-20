package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/egladman/magus/internal/job"
)

// jobWorkspace is the workspace at root as a job record's validation reads it, loaded once.
// A workspace that will not load comes back Unavailable with the load error, and so does
// one listing no projects or rooted elsewhere than root, so the checks that need it say they
// did not run rather than pass or judge against the wrong tree.
func jobWorkspace(ctx context.Context, root string) job.Workspace {
	m, err := loadMagus(ctx, root)
	switch {
	case err != nil:
		return job.Workspace{Unavailable: err.Error()}
	case root != "" && filepath.Clean(m.Root()) != filepath.Clean(root):
		return job.Workspace{Unavailable: fmt.Sprintf("the loaded workspace is %s, not %s", m.Root(), root)}
	case len(m.All()) == 0:
		return job.Workspace{Unavailable: fmt.Sprintf("the workspace at %s lists no projects", m.Root())}
	}
	return job.Workspace{Projects: m.All(), Ignored: job.VCSIgnored(ctx, m.Root())}
}
