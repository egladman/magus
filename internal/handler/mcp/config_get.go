package mcp

import (
	"context"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
)

type configGetTool struct {
	cfg config.Config
}

func (t *configGetTool) Name() string { return "magus_config_get" }

func (t *configGetTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	return types.InvokeResponse{Data: t.cfg}, nil
}

var _ types.SpellDriver = (*configGetTool)(nil)
