package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/types"
)

type describeKindTool struct {
	ws  types.Describer
	cfg types.WorkspaceConfig
}

func (t *describeKindTool) Name() string { return "magus_describe" }

func (t *describeKindTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	kind := paramString(req.Params, "kind", "")
	switch kind {
	case "spells":
		return types.InvokeResponse{Data: t.ws.DescribeSpells()}, nil
	case "targets":
		return types.InvokeResponse{Data: t.ws.DescribeTargets()}, nil
	case "projects":
		return types.InvokeResponse{Data: t.ws.DescribeProjects()}, nil
	case "workspaces":
		return types.InvokeResponse{Data: t.ws.DescribeWorkspaces(t.cfg)}, nil
	case "mcp_tools":
		return types.InvokeResponse{Data: DescribeTools()}, nil
	case "":
		return types.InvokeResponse{}, errors.New("mcp: kind is required (one of: spells, targets, projects, workspaces, mcp_tools)")
	default:
		return types.InvokeResponse{}, fmt.Errorf("mcp: unknown kind %q (one of: spells, targets, projects, workspaces, mcp_tools)", kind)
	}
}

var _ types.SpellDriver = (*describeKindTool)(nil)
