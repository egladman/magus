package mcp

import (
	"context"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/spells"
)

type configGetTool struct {
	cfg config.Config
}

func (t *configGetTool) Name() string { return hint.ToolConfigGet.String() }

func (t *configGetTool) Invoke(_ context.Context, _ spells.InvokeRequest) (spells.InvokeResponse, error) {
	return spells.InvokeResponse{Data: t.cfg}, nil
}

var _ spells.Driver = (*configGetTool)(nil)
