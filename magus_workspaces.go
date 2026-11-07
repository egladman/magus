package magus

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/ward"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// reconcileWorkspaces brings root's .magus/workspaces, and each listed tree's
// view of root, in line with knowledge.workspaces. Every change prints one
// line to stderr; a load that changes nothing prints nothing.
func reconcileWorkspaces(ctx context.Context, root string, entries []config.KnowledgeWorkspace, opts types.VCSOptions, stderr io.Writer) ([]types.ListedWorkspace, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("knowledge.workspaces: %w", err)
	}
	listed := make([]ward.ListedEntry, 0, len(entries))
	for _, e := range entries {
		r, err := e.Root()
		if err != nil {
			return nil, err
		}
		listed = append(listed, ward.ListedEntry{Name: e.Name, Root: r, Where: e.Where()})
	}
	if ward.Listed(abs, listed) {
		if err := refuseTrackedWorkspaces(ctx, abs, opts); err != nil {
			return nil, err
		}
	}
	out, lines, err := ward.ReconcileWorkspaces(abs, listed)
	for _, l := range lines {
		_, _ = fmt.Fprintln(stderr, l)
	}
	return out, err
}

// refuseTrackedWorkspaces refuses a root whose VCS does not ignore
// types.WorkspacesDir. A root no VCS claims has nothing that could commit it.
func refuseTrackedWorkspaces(ctx context.Context, root string, opts types.VCSOptions) error {
	res, err := vcs.Resolve(ctx, root, "", opts)
	if err != nil {
		return err
	}
	if res.VCS == nil || res.Source == types.VCSSourceDisabled || res.Source == types.VCSSourceDefault {
		return nil
	}
	ignored, err := res.VCS.IgnoredFiles(ctx, root, []string{types.WorkspacesDir})
	if err != nil {
		return fmt.Errorf("knowledge.workspaces: ask %s whether it ignores %s: %w", res.Name, types.WorkspacesDir, err)
	}
	if len(ignored) == 0 {
		return types.DiagnosticErrorf(types.WorkspacesTracked,
			"%s does not ignore %s, so a connected workspace could be committed: add .magus/ to the ignore file",
			res.Name, types.WorkspacesDir)
	}
	return nil
}
