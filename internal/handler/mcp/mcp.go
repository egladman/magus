package mcp

// mcp.go is the dispatch pipeline that joins the tool CATALOG (Registry, in
// registry.go) to the tool IMPLEMENTATIONS (the SpellDriver structs in the
// per-tool files) and mounts them on the mark3labs MCP server: allMCPTools builds
// the drivers, registerTools pairs each with its descriptor, adapt bridges the
// unified SpellDriver signature to the server's handler shape, and wrap layers the
// per-call origin marker, request-scoped logger, stderr banner, and audit record.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/egladman/magus"
	"github.com/egladman/magus/internal/codec"
	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/types"
)

type ctxKey int

const keyLogger ctxKey = iota

// withLogger attaches a request-scoped slog.Logger to ctx. toolLogger retrieves it.
func withLogger(ctx context.Context, log *slog.Logger) context.Context {
	return context.WithValue(ctx, keyLogger, log)
}

// toolLogger returns the request-scoped logger when present, falling back to
// slog.Default(). Call this in tool handlers when surfacing sub-step errors
// so they appear within the agent-request's visual bracket in stderr.
func toolLogger(ctx context.Context) *slog.Logger {
	if log, ok := ctx.Value(keyLogger).(*slog.Logger); ok && log != nil {
		return log
	}
	return slog.Default()
}

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

// handlerFn is the signature all tool implementations share.
type handlerFn func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error)

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

// allMCPTools constructs every MCP tool the daemon exposes. Each tool is a
// SpellDriver; the MCP server dispatches by Name and invokes it.
func allMCPTools(opts Options) []types.SpellDriver {
	wsCfg := types.WorkspaceConfig{
		CacheDir:    opts.Config.Cache.Dir,
		Concurrency: opts.Config.Concurrency,
	}
	return []types.SpellDriver{
		&describeKindTool{ws: opts.Magus, cfg: wsCfg},
		&describeFileTool{ws: opts.Magus},
		&whereTool{ws: opts.Magus},
		&affectedExplainTool{ws: opts.Magus},
		&insightTool{ws: opts.Magus},
		&runTargetTool{opts: opts},
		&runAffectedTool{opts: opts},
		&doctorTool{opts: opts},
		&statusTool{opts: opts},
		&affectedPlanTool{opts: opts},
		&configGetTool{cfg: opts.Config},
		&tailLogTool{opts: opts},
		&scratchpadTool{opts: opts},
		&memoryTool{opts: opts},
		&queryTool{graph: opts.Magus},
		&outputTool{reader: opts.Magus},
		&explainTool{graph: opts.Magus},
		&pathTool{graph: opts.Magus},
		&statsTool{graph: opts.Magus},
		&refsTool{graph: opts.Magus},
	}
}

// *magus.Magus satisfies the narrow reader interfaces the read-tools depend on,
// structurally and with no changes to the magus package.
var (
	_ outputReader  = (*magus.Magus)(nil)
	_ graphResolver = (*magus.Magus)(nil)
)

func registerTools(srv *server.MCPServer, opts Options, log *slog.Logger, agentFn func(context.Context) string, trailDir string) {
	// The MCP tool ctx is not stamped with the telemetry provider, so grab the
	// shared one here and close over it in wrap. Telemetry() returns a nil-safe
	// disabledProvider when telemetry is off; a nil Magus (some test paths)
	// leaves tel nil, which wrap guards against.
	var tel observability.Provider
	if opts.Magus != nil {
		tel = opts.Magus.Telemetry()
	}
	byName := make(map[string]types.SpellDriver, len(Registry))
	for _, t := range allMCPTools(opts) {
		byName[t.Name()] = t
	}
	for _, d := range Registry {
		t, ok := byName[d.Name]
		if !ok {
			panic(fmt.Sprintf("mcp: registry entry %q has no SpellDriver implementation", d.Name))
		}
		srv.AddTool(buildMCPTool(d), wrap(log, agentFn, trailDir, tel, adapt(t)))
	}
}

// wrap injects origin markers and emits banner log lines around every tool
// call so the human watching magus's stderr can immediately see when an agent
// triggered an operation. It also records one activity Event per call to the
// activity trail (best-effort; a nil trail is a no-op) as a KIND_MCP_TOOL_CALL -
// the durable form of the banner that the /dashboard activity view reads, with
// both sides of the exchange captured as content-addressed blobs - and records
// the call to the magus.mcp.tool.* metric family (attributed by tool + outcome
// only; never by argument values or result content). A nil tel is a no-op.
func wrap(log *slog.Logger, agentFn func(context.Context) string, trailDir string, tel observability.Provider, fn handlerFn) server.ToolHandlerFunc {
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

		// Cross-link the result before measuring: a hint is output the agent
		// reads, so its bytes belong in the output-size metric (its context cost).
		decorateResult(result, toolName)

		// Capture both sides the agent exchanged with the tool as content-addressed blobs
		// (prefixed "mcp"), keeping only refs on the event so a large body never bloats the
		// trail line. Request = the tool arguments, response = the result text.
		reqRef, reqBytes := trail.WriteBlob(trailDir, "mcp", argsJSON(req.GetArguments()))
		respText := allText(result)
		respRef, respBytes := trail.WriteBlob(trailDir, "mcp", []byte(respText))

		dur := time.Since(start)
		ev := trail.Event{
			Ts:            start.UnixMilli(),
			Kind:          trail.KindMCPToolCall,
			Actor:         agentID,
			Action:        toolName,
			Outcome:       trail.OutcomeOK,
			DurMs:         dur.Milliseconds(),
			RequestRef:    reqRef,
			RequestBytes:  reqBytes,
			ResponseRef:   respRef,
			ResponseBytes: respBytes,
			Preview:       preview(respText, respPreviewLen),
		}
		// adapt() turns every validation/soft failure into an IsError result with a nil err, so
		// result.IsError is the reachable failure signal; the err != nil arm is defensive against
		// a future fn that returns a transport error. Both must read as error on the trail AND
		// the metric (which takes ev.Outcome), or the view shows green for calls that failed.
		switch {
		case err != nil:
			ev.Outcome = trail.OutcomeError
			ev.Error = err.Error()
			reqLog.Error("[AGENT] tool error", slog.Duration("duration", dur), slog.String("error", err.Error()))
		case result != nil && result.IsError:
			ev.Outcome = trail.OutcomeError // the error text is the response body, captured above
			reqLog.Warn("[AGENT] tool failed", slog.Duration("duration", dur))
		default:
			reqLog.Info("[AGENT] tool done", slog.Duration("duration", dur))
		}
		trail.Append(trailDir, ev)
		if tel != nil {
			// INPUT = the serialized tool arguments; OUTPUT = the response text length
			// (same bytes captured above). Attribute by tool + outcome only to keep
			// metric cardinality bounded.
			tel.RecordMCPCall(ctx, observability.MCPCall{
				Tool:        toolName,
				Outcome:     ev.Outcome,
				InputBytes:  reqBytes,
				OutputBytes: respBytes,
				Duration:    dur.Seconds(),
			})
		}
		return result, err
	}
}

// argsJSON marshals a tool call's arguments map for capture into the activity blob store.
// An empty map or a marshal failure yields nil (no request blob is stored), never a panic.
func argsJSON(args map[string]any) []byte {
	if len(args) == 0 {
		return nil
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return nil
	}
	return raw
}

// respPreviewLen bounds the inline response preview kept on each audit event, so a
// list view can show what came back without fetching every full body from the blob store.
const respPreviewLen = 240

// allText concatenates every text block of a tool result in order. A nil result (the
// transport-error path) yields "", and non-text content blocks are ignored.
func allText(result *mcplib.CallToolResult) string {
	if result == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range result.Content {
		if tc, ok := c.(mcplib.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// preview returns the first n runes of s, appending an ellipsis marker when it had to cut.
// Rune-aware so it never splits a multibyte character mid-way.
func preview(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
