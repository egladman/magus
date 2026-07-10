//go:build mcp

package mcp

import (
	"context"

	"github.com/egladman/magus/internal/doctor"
	"github.com/egladman/magus/types"
)

type doctorTool struct {
	opts ServerOptions
}

func (t *doctorTool) Name() string { return "magus_doctor" }

func (t *doctorTool) Invoke(_ context.Context, _ types.InvokeRequest) (types.InvokeResponse, error) {
	ws := t.opts.Magus
	out := doctor.Run(
		ws.Root(), ws, nil,
		doctor.WithConfig(t.opts.Config),
	)
	return types.InvokeResponse{Data: out}, nil
}

var _ types.SpellDriver = (*doctorTool)(nil)
