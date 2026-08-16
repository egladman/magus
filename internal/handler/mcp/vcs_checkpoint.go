package mcp

import (
	"context"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// vcsCheckpointTool reports the identity of the working state: the CLI's
// `magus vcs checkpoint` as a tool, returning the same types.VCSCheckpoint the CLI
// renders, so an agent recording what it was handed and a human reading the terminal
// cannot describe the tree differently.
//
// It takes no parameters. A checkpoint is about THIS workspace at THIS moment, and
// there is nothing to narrow: a path would scope the dirtiness probe and quietly make
// the digest mean something else.
//
// types.WorkspaceReader is the whole dependency - Root and VCSOptions - which is also
// what makes it obviously read-only.
type vcsCheckpointTool struct {
	ws types.WorkspaceReader
}

func (t *vcsCheckpointTool) Name() string { return ToolVCSCheckpoint.String() }

func (t *vcsCheckpointTool) Invoke(ctx context.Context, _ spells.InvokeRequest) (spells.InvokeResponse, error) {
	root := t.ws.Root()
	res, err := vcs.Resolve(ctx, root, "", t.ws.VCSOptions())
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	cp, err := vcs.Checkpoint(ctx, root, res)
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: cp}, nil
}

var _ spells.Driver = (*vcsCheckpointTool)(nil)
