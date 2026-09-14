package mcp

import (
	"context"
	"fmt"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/service/console"
	"github.com/egladman/magus/spells"
)

// consolePresentTool returns a local console link. The MCP client decides how to show it.
type consolePresentTool struct {
	host        string
	unavailable string
}

func (t *consolePresentTool) Name() string { return hint.ToolConsolePresent.String() }

func (t *consolePresentTool) Invoke(_ context.Context, req spells.InvokeRequest) (spells.InvokeResponse, error) {
	if t.unavailable != "" {
		return spells.InvokeResponse{}, fmt.Errorf("mcp: console presentation is unavailable because %s", t.unavailable)
	}
	p, err := console.Present(t.host, paramString(req.Params, "surface", ""), paramString(req.Params, "reason", ""))
	if err != nil {
		return spells.InvokeResponse{}, err
	}
	return spells.InvokeResponse{Data: p}, nil
}

var _ spells.Driver = (*consolePresentTool)(nil)
