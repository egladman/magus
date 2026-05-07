//go:build mcp

package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/egladman/magus/internal/mcp/origin"
	"github.com/egladman/magus/types"
)

// allMCPTools constructs every MCP tool the daemon exposes. Each tool is a
// SpellDriver; the MCP server dispatches by Name and invokes it.
func allMCPTools(opts ServerOptions) []types.SpellDriver {
	wsCfg := types.WorkspaceConfig{
		CacheDir:    opts.Config.Cache.Dir,
		Concurrency: opts.Config.Concurrency,
	}
	return []types.SpellDriver{
		&describeKindTool{ws: opts.Magus, cfg: wsCfg},
		&whereTool{ws: opts.Magus},
		&affectedExplainTool{ws: opts.Magus},
		&runTargetTool{opts: opts},
		&runAffectedTool{opts: opts},
		&doctorTool{opts: opts},
		&statusTool{opts: opts},
		&affectedPlanTool{opts: opts},
		&configGetTool{cfg: opts.Config},
		&tailLogTool{opts: opts},
	}
}

func registerTools(srv *server.MCPServer, opts ServerOptions, log *slog.Logger, agentFn func(context.Context) string) {
	byName := make(map[string]types.SpellDriver, len(Registry))
	for _, t := range allMCPTools(opts) {
		byName[t.Name()] = t
	}
	for _, d := range Registry {
		t, ok := byName[d.Name]
		if !ok {
			panic(fmt.Sprintf("mcp: registry entry %q has no SpellDriver implementation", d.Name))
		}
		srv.AddTool(buildMCPTool(d), wrap(log, agentFn, adapt(t)))
	}
}

// handlerFn is the signature all tool implementations share.
type handlerFn func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error)

// wrap injects origin markers and emits banner log lines around every tool
// call so the human watching magus's stderr can immediately see when an agent
// triggered an operation.
func wrap(log *slog.Logger, agentFn func(context.Context) string, fn handlerFn) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		agentID := agentFn(ctx)
		toolName := req.Params.Name

		ctx = origin.WithContext(ctx, origin.Origin{Agent: agentID})

		reqLog := log.With(
			slog.String("agent", agentID),
			slog.String("tool", toolName),
		)
		ctx = withLogger(ctx, reqLog)

		reqLog.Info("[AGENT] tool called")
		start := time.Now()

		result, err := fn(ctx, req)

		dur := time.Since(start)
		if err != nil {
			reqLog.Error(
				"[AGENT] tool error",
				slog.Duration("duration", dur),
				slog.String("error", err.Error()),
			)
		} else {
			reqLog.Info("[AGENT] tool done", slog.Duration("duration", dur))
		}
		return result, err
	}
}
