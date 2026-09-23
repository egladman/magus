package mcp

import (
	"context"

	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

type doctorTool struct {
	opts Options
}

func (t *doctorTool) Name() string { return hint.ToolDoctor.String() }

func (t *doctorTool) Invoke(ctx context.Context, _ spells.InvokeRequest) (spells.InvokeResponse, error) {
	ws := t.opts.Magus
	out := doctor.Run(
		ctx, ws.Root(), ws, nil,
		doctor.WithConfig(t.opts.Config),
		doctor.WithGraphNodes(func(ctx context.Context) ([]types.KnowledgeNode, error) {
			g, err := ws.KnowledgeGraph(ctx, false)
			if err != nil {
				return nil, err
			}
			return g.Nodes(), nil
		}),
	)
	return spells.InvokeResponse{Data: out}, nil
}

var _ spells.Driver = (*doctorTool)(nil)
