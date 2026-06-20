//go:build mcp

package mcp

import (
	"context"
	"fmt"

	mcplib "github.com/mark3labs/mcp-go/mcp"

	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/types"
)

// jsonResult marshals v as compact JSON and wraps it in a text CallToolResult.
func jsonResult(v any) (*mcplib.CallToolResult, error) {
	b, err := codec.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal result: %w", err)
	}
	return mcplib.NewToolResultText(string(b)), nil
}

// paramString reads a string parameter from a InvokeRequest.Params map.
func paramString(params map[string]any, key, def string) string {
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// paramBool reads a bool parameter from a InvokeRequest.Params map.
func paramBool(params map[string]any, key string, def bool) bool {
	if v, ok := params[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return def
}

// paramFloat reads a numeric parameter from a InvokeRequest.Params map.
// JSON numbers are decoded as float64; ints from struct binders are also accepted.
func paramFloat(params map[string]any, key string, def float64) float64 {
	if v, ok := params[key]; ok {
		switch n := v.(type) {
		case float64:
			return n
		case int:
			return float64(n)
		case int64:
			return float64(n)
		}
	}
	return def
}

// adapt converts a SpellDriver into the MCP server's handler signature.
// Soft errors from Invoke are surfaced as IsError tool results, mirroring the
// pre-refactor behaviour where validation failures returned via
// NewToolResultError rather than transport errors.
func adapt(t types.SpellDriver) handlerFn {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		resp, err := t.Invoke(ctx, types.InvokeRequest{Params: req.GetArguments()})
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		if resp.Data != nil {
			return jsonResult(resp.Data)
		}
		return mcplib.NewToolResultText(resp.Text), nil
	}
}

// buildMCPTool turns a static ToolDescriptor into an mcplib.Tool.
func buildMCPTool(d ToolDescriptor) mcplib.Tool {
	opts := []mcplib.ToolOption{mcplib.WithDescription(d.Description)}
	for _, p := range d.Params {
		var propOpts []mcplib.PropertyOption
		if p.Required {
			propOpts = append(propOpts, mcplib.Required())
		}
		if p.Description != "" {
			propOpts = append(propOpts, mcplib.Description(p.Description))
		}
		switch p.Type {
		case "string":
			opts = append(opts, mcplib.WithString(p.Name, propOpts...))
		case "boolean":
			opts = append(opts, mcplib.WithBoolean(p.Name, propOpts...))
		case "number":
			opts = append(opts, mcplib.WithNumber(p.Name, propOpts...))
		default:
			panic(fmt.Sprintf("mcp: tool %q param %q has unknown type %q", d.Name, p.Name, p.Type))
		}
	}
	return mcplib.NewTool(d.Name, opts...)
}
