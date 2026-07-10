package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/egladman/magus/types"
)

type describePath struct {
	Seed  string   `json:"seed"`
	Chain []string `json:"chain"`
	Files []string `json:"files"`
}

type describeResult struct {
	Project  string         `json:"project"`
	Affected bool           `json:"affected"`
	Base     string         `json:"base"`
	Paths    []describePath `json:"paths,omitempty"`
}

type affectedExplainTool struct {
	ws types.WorkspaceRepository
}

func (t *affectedExplainTool) Name() string { return "magus_affected_explain" }

func (t *affectedExplainTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	project := paramString(req.Params, "project", "")
	if project == "" {
		return types.InvokeResponse{}, errors.New("mcp: project is required")
	}
	base := paramString(req.Params, "base", "")

	r, err := t.ws.Affected(ctx, base)
	if err != nil {
		toolLogger(ctx).Warn("mcp: affected computation failed", "error", err)
		return types.InvokeResponse{}, fmt.Errorf("mcp: affected: %w", err)
	}

	g, err := t.ws.Graph()
	if err != nil {
		toolLogger(ctx).Warn("mcp: graph load failed", "error", err)
		return types.InvokeResponse{}, fmt.Errorf("mcp: graph: %w", err)
	}

	out := describeResult{Project: project, Base: r.Base}
	for _, a := range r.Affected {
		if a == project {
			out.Affected = true
			break
		}
	}

	if out.Affected {
		paths := g.PathsFromSeeds(r.Seed, project)
		for _, ap := range paths {
			out.Paths = append(out.Paths, describePath{
				Seed:  ap.Seed,
				Chain: ap.Chain,
				Files: r.FilesBySeed[ap.Seed],
			})
		}
	}

	return types.InvokeResponse{Data: out}, nil
}

var _ types.SpellDriver = (*affectedExplainTool)(nil)
