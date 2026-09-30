package mcp

import (
	"context"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/spells"
)

type configTool struct {
	cfg config.Config
}

func (t *configTool) Name() string { return hint.ToolConfig.String() }

func (t *configTool) Invoke(_ context.Context, _ spells.InvokeRequest) (spells.InvokeResponse, error) {
	return spells.InvokeResponse{Data: t.cfg}, nil
}

var _ spells.Driver = (*configTool)(nil)
