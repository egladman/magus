package mcp

import (
	"context"

	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/spells"
)

type doctorTool struct {
	opts Options
}

func (t *doctorTool) Name() string { return "magus_doctor" }

func (t *doctorTool) Invoke(ctx context.Context, _ spells.InvokeRequest) (spells.InvokeResponse, error) {
	ws := t.opts.Magus
	out := doctor.Run(
		ctx, ws.Root(), ws, nil,
		doctor.WithConfig(t.opts.Config),
	)
	return spells.InvokeResponse{Data: out}, nil
}

var _ spells.Driver = (*doctorTool)(nil)
