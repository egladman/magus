package job

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/egladman/magus/types"
)

// LoadedWorkspace is the part of an opened workspace a job record's validation reads.
type LoadedWorkspace interface {
	Root() string
	All() []*types.Project
}

// WorkspaceOf is the workspace at root as a job record's validation reads it, from loaded
// and the error that opening it returned. A workspace that will not load comes back
// Unavailable with failure's text for that error, and so does one listing no projects or
// rooted elsewhere than root, so the checks that need it say they did not run rather than
// pass or judge against the wrong tree.
func WorkspaceOf(ctx context.Context, root string, loaded LoadedWorkspace, err error, failure func(error) string) Workspace {
	switch {
	case err != nil:
		return Workspace{Unavailable: failure(err)}
	case root != "" && filepath.Clean(loaded.Root()) != filepath.Clean(root):
		return Workspace{Unavailable: fmt.Sprintf("the loaded workspace is %s, not %s", loaded.Root(), root)}
	case len(loaded.All()) == 0:
		return Workspace{Unavailable: fmt.Sprintf("the workspace at %s lists no projects", loaded.Root())}
	}
	return Workspace{Projects: loaded.All(), Ignored: VCSIgnored(ctx, loaded.Root())}
}

// LoadFailureText is err's text. A binary too old for the tree also names the tree's own
// ./magus, own, when there is one.
func LoadFailureText(err error, own string) string {
	if !errors.Is(err, types.WorkspaceNeedsNewerMagus) || own == "" {
		return err.Error()
	}
	return fmt.Sprintf("%s\nThis checkout has its own build: rerun the command with `%s`", err, own)
}
