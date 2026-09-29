package mcp

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/spells"
)

// consoleTool returns a local console link. The MCP client decides how to show it.
type consoleTool struct {
	host        string
	unavailable string
}

func (t *consoleTool) Name() string { return hint.ToolConsole.String() }

func (t *consoleTool) Invoke(_ context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	if t.unavailable != "" {
		return spells.InvokeResponse{}, fmt.Errorf("mcp: console presentation is unavailable because %s", t.unavailable)
	}
	p, err := console.Present(t.host, paramString(req.Params, "surface", ""), paramString(req.Params, "reason", ""))
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: p}, nil
}

var _ spells.Driver = (*consoleTool)(nil)
