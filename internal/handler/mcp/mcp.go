package mcp

// mcp.go is the dispatch pipeline that joins the tool CATALOG (Registry, in
// registry.go) to the tool IMPLEMENTATIONS (the SpellDriver structs in the
// per-tool files) and mounts them on the mark3labs MCP server: allToolDrivers builds
// the drivers, registerTools pairs each with its descriptor, adapt bridges the
// unified SpellDriver signature to the server's handler shape, and wrap layers the
// per-call origin marker, stderr banner, and audit record.

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/egladman/magus/internal/handler/mcp/origin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/observability"
	"github.com/egladman/magus/internal/review"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// jsonResult sends v as structured MCP content with a JSON text fallback for
// clients that only read content blocks. Both carry the same bytes: structured
// content is the already-encoded JSON, not v handed to mcp-go's own encoder. A
// payload that is not a JSON object goes out as text alone, since the spec
// requires structured content to be an object.
func jsonResult(v any) (*mcplib.CallToolResult, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("mcp: marshal result: %w", err)
	}
	if t := bytes.TrimSpace(b); len(t) == 0 || t[0] != '{' {
		return mcplib.NewToolResultText(string(b)), nil
	}
	return mcplib.NewToolResultStructured(json.RawMessage(b), string(b)), nil
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
// Soft errors from Invoke are surfaced as IsError tool results rather than
// transport errors, so the agent reads the diagnostic.
func adapt(t spells.Driver) handlerFn {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		resp, err := t.Invoke(ctx, spells.InvokeRequest{Params: req.GetArguments()})
		if err != nil {
			return mcplib.NewToolResultError(err.Error()), nil
		}
		if resp.Data != nil {
			return jsonResult(resp.Data)
		}
		return mcplib.NewToolResultText(resp.Text), nil
	}
}

// declaredParams refuses a call that passes a parameter the tool's descriptor
// does not declare, which the handler would otherwise ignore without a word.
func declaredParams(d ToolDescriptor, fn handlerFn) handlerFn {
	declared := make(map[string]bool, len(d.Params))
	for _, p := range d.Params {
		declared[p.Name] = true
	}
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		for name := range req.GetArguments() {
			if !declared[name] {
				return mcplib.NewToolResultError(fmt.Sprintf("%s: unknown parameter %q", d.Name, name)), nil
			}
		}
		return fn(ctx, req)
	}
}

// ToolNeed is what every MCP tool requires of the caller's credential, over either transport.
// The server holds its /mcp route to the same Need, so a credential the route admits is never
// refused by a tool behind it.
var ToolNeed = types.Need{Scope: types.ScopeMCP, Level: types.LevelWrite}

// authorize refuses a call whose credential falls short of ToolNeed, as an MGS9015 tool error.
// The credential is the one on ctx: the server's bearer guard stamps the bearer it verified,
// ServeStdio stamps types.CredentialStdio, and the socket peer guard stamps
// types.CredentialSocketPeer. A ctx carrying none of them holds the zero credential, which
// grants nothing.
func authorize(fn handlerFn) handlerFn {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		cred := trail.CredentialFromContext(ctx)
		if !cred.Grant.Allows(ToolNeed) {
			held := cred.Grant.String()
			if held == "" {
				held = "nothing"
			}
			err := types.DiagnosticErrorf(types.GrantInsufficient, "%s needs %s and the caller holds %s", req.Params.Name, ToolNeed, held)
			return mcplib.NewToolResultError(err.Error()), nil
		}
		return fn(ctx, req)
	}
}

// taskSupport is the MCP task support a tool declares. Only client runs long
// enough to need it: a host that supports tasks runs it without the direct-call
// bound (hint.ClientCallBound) and cancels it instead.
var taskSupport = map[string]mcplib.TaskSupport{
	hint.ToolClient.String(): mcplib.TaskSupportOptional,
}

// buildMCPTool turns a static ToolDescriptor into an mcplib.Tool.
func buildMCPTool(d ToolDescriptor) mcplib.Tool {
	opts := []mcplib.ToolOption{mcplib.WithDescription(d.Description)}
	if ts, ok := taskSupport[d.Name]; ok {
		opts = append(opts, mcplib.WithTaskSupport(ts))
	}
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
		case "object":
			opts = append(opts, mcplib.WithObject(p.Name, propOpts...))
		case "string_array":
			opts = append(opts, mcplib.WithArray(p.Name, append(propOpts, mcplib.WithStringItems())...))
		default:
			panic(fmt.Sprintf("mcp: tool %q param %q has unknown type %q", d.Name, p.Name, p.Type))
		}
	}
	return mcplib.NewTool(d.Name, opts...)
}

// NoteAnchors joins the workspace's declared notes stores against a changeset, for a review
// brief. The server's diff thread route and the diff tool share it so both name the same
// anchors. The graph, with symbol shards, is loaded only when a store is declared.
func (o Options) NoteAnchors() func(ctx context.Context, rev types.Diff) []review.AnchorHit {
	notes := o.Config.Knowledge.Notes
	return func(ctx context.Context, rev types.Diff) []review.AnchorHit {
		return review.ChangesetAnchors(ctx, o.Magus.Root(), notes.Shared, notes.Private, o.Magus.KnowledgeGraphWithSymbols, rev)
	}
}

// allToolDrivers constructs every MCP tool the server exposes. Each tool is a
// SpellDriver; the MCP server dispatches by Name and invokes it.
func allToolDrivers(opts Options) []spells.Driver {
	consoleUnavailable := ""
	if opts.Config.Console.Enabled != nil && !*opts.Config.Console.Enabled {
		consoleUnavailable = "console.enabled is false"
	} else if !opts.httpAddr().Addr().IsLoopback() {
		consoleUnavailable = "mcp.address is not loopback"
	}
	return []spells.Driver{
		&clientTool{root: opts.Magus.Root(), timeout: hint.ClientCallBound},
		&buzzTool{root: opts.Magus.Root(), timeout: cmp.Or(opts.Config.TargetTimeout, buzzDefaultTimeout)},
		&statusTool{opts: opts},
		&consoleTool{host: opts.httpAddr().String(), unavailable: consoleUnavailable},
		&configTool{cfg: opts.Config},
		&diffTool{sessions: opts.DiffSessions, root: opts.Magus.Root(), src: opts.Magus, anchors: opts.NoteAnchors()},
	}
}

func registerTools(srv *server.MCPServer, opts Options, log *slog.Logger, originFn func(context.Context) origin.Client, trailDir string, tasks *taskRuns) {
	// The MCP tool ctx is not stamped with the telemetry provider, so grab the
	// shared one here and close over it in wrap. Telemetry() returns a nil-safe
	// disabledProvider when telemetry is off; a nil Magus (some test paths)
	// leaves tel nil, which wrap guards against.
	var tel observability.Provider
	if opts.Magus != nil {
		tel = opts.Magus.Telemetry()
	}
	if opts.Magus == nil && opts.Unavailable != nil {
		unavailable := func(context.Context, mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
			return mcplib.NewToolResultError(opts.Unavailable().Error()), nil
		}
		for _, d := range Registry {
			srv.AddTool(buildMCPTool(d), wrap(log, originFn, trailDir, func(ctx context.Context) context.Context { return ctx }, tel, authorize(unavailable)))
		}
		return
	}
	tools := allToolDrivers(opts)
	// A function rather than the workspace itself: wrap needs one capability (put this
	// workspace's secret resolver on a context so the trail writes are redacted) and
	// taking *magus.Magus for it would hand the handler a whole workspace to reach into.
	withSecrets := func(ctx context.Context) context.Context { return ctx }
	if opts.Magus != nil {
		withSecrets = opts.Magus.ContextWithSecrets
	}
	byName := make(map[string]spells.Driver, len(tools))
	for _, t := range tools {
		byName[t.Name()] = t
	}
	stamp := newServed(opts.Magus.Root(), opts.Build)
	for _, d := range Registry {
		t, ok := byName[d.Name]
		if !ok {
			panic(fmt.Sprintf("mcp: registry entry %q has no SpellDriver implementation", d.Name))
		}
		srv.AddTool(buildMCPTool(d), tasks.detach(wrap(log, originFn, trailDir, withSecrets, tel, stamp.annotate(authorize(declaredParams(d, adapt(t)))))))
	}
	// The loop above only checks Registry -> driver; a driver built into allToolDrivers but
	// missing its own Registry entry would otherwise mount nowhere, silently, with no
	// error anywhere. Check the other direction too.
	if missing := unregisteredDrivers(tools, Registry); len(missing) > 0 {
		panic(fmt.Sprintf("mcp: drivers %v have no Registry entry and were never mounted", missing))
	}
}

// unregisteredDrivers returns the Name() of every driver in tools absent from reg,
// in tools' order. Empty (nil) when every driver has a matching Registry entry.
func unregisteredDrivers(tools []spells.Driver, reg []ToolDescriptor) []string {
	described := make(map[string]bool, len(reg))
	for _, d := range reg {
		described[d.Name] = true
	}
	var missing []string
	for _, t := range tools {
		if !described[t.Name()] {
			missing = append(missing, t.Name())
		}
	}
	return missing
}

// taskRunKey marks a context whose call a host runs as an MCP task.
type taskRunKey struct{}

// runsAsTask reports whether the call on ctx runs as an MCP task, which the host
// bounds and cancels itself.
func runsAsTask(ctx context.Context) bool {
	return ctx.Value(taskRunKey{}) != nil
}

// taskRuns keeps a call a host runs as an MCP task alive past the request that
// started it, and cancellable by tasks/cancel.
//
// mcp-go derives a task's context from its tools/call request's context, and
// that one ends when the request returns the task id, so the handler would see
// a cancelled context almost at once. detach runs the call on a context of its
// own instead. tasks/cancel names the task by id, which the handler never sees,
// so the hooks below join the two: the request's tools/call hook and its
// task-created hook share the request's context, and the handler shares the
// request's task params.
type taskRuns struct {
	mu        sync.Mutex
	byRequest map[context.Context]*taskRun
	byParams  map[*mcplib.TaskParams]*taskRun
	byID      map[string]*taskRun
}

type taskRun struct {
	params    *mcplib.TaskParams
	id        string
	cancel    context.CancelFunc
	cancelled bool
}

func newTaskRuns() *taskRuns {
	return &taskRuns{
		byRequest: map[context.Context]*taskRun{},
		byParams:  map[*mcplib.TaskParams]*taskRun{},
		byID:      map[string]*taskRun{},
	}
}

// hook registers the joins on the server's hooks and task hooks.
func (r *taskRuns) hook(hooks *server.Hooks, taskHooks *server.TaskHooks) {
	hooks.AddBeforeCallTool(func(ctx context.Context, _ any, req *mcplib.CallToolRequest) {
		if req.Params.Task == nil {
			return
		}
		run := &taskRun{params: req.Params.Task}
		r.mu.Lock()
		r.byRequest[ctx] = run
		r.byParams[run.params] = run
		r.mu.Unlock()
	})
	taskHooks.AddOnTaskCreated(func(ctx context.Context, m server.TaskMetrics) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if run, ok := r.byRequest[ctx]; ok {
			delete(r.byRequest, ctx)
			run.id = m.TaskID
			r.byID[m.TaskID] = run
		}
	})
	taskHooks.AddOnTaskCancelled(func(_ context.Context, m server.TaskMetrics) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if run, ok := r.byID[m.TaskID]; ok {
			run.cancelled = true
			if run.cancel != nil {
				run.cancel()
			}
		}
	})
	// A request that never created a task (refused, or a tool without task support)
	// leaves nothing behind.
	forget := func(ctx context.Context) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if run, ok := r.byRequest[ctx]; ok {
			delete(r.byRequest, ctx)
			delete(r.byParams, run.params)
		}
	}
	hooks.AddAfterCallTool(func(ctx context.Context, _ any, _ *mcplib.CallToolRequest, _ any) { forget(ctx) })
	hooks.AddOnError(func(ctx context.Context, _ any, _ mcplib.MCPMethod, _ any, _ error) { forget(ctx) })
}

// detach runs a task call on a context that outlives its request and ends on
// tasks/cancel. A direct call passes through unchanged.
func (r *taskRuns) detach(fn server.ToolHandlerFunc) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		if req.Params.Task == nil {
			return fn(ctx, req)
		}
		r.mu.Lock()
		run := r.byParams[req.Params.Task]
		delete(r.byParams, req.Params.Task)
		r.mu.Unlock()
		if run == nil {
			return fn(ctx, req)
		}
		ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		defer cancel()
		r.mu.Lock()
		run.cancel = cancel
		if run.cancelled {
			cancel()
		}
		r.mu.Unlock()
		defer func() {
			r.mu.Lock()
			delete(r.byID, run.id)
			r.mu.Unlock()
		}()
		return fn(context.WithValue(ctx, taskRunKey{}, true), req)
	}
}

// wrap injects origin markers and emits banner log lines around every tool
// call so the human watching magus's stderr can immediately see when an agent
// triggered an operation. It also records one activity Event per call to the
// activity trail (best-effort; a nil trail is a no-op) as a KIND_MCP_TOOL_CALL
// (the durable form of the banner that the /dashboard activity view reads, with
// both sides of the exchange captured as content-addressed blobs).
//
// It also records the call to the magus.mcp.tool.* metric family (attributed by
// tool + outcome only; never by argument values or result content). A nil tel is
// a no-op.
func wrap(log *slog.Logger, originFn func(context.Context) origin.Client, trailDir string, withSecrets func(context.Context) context.Context, tel observability.Provider, fn handlerFn) server.ToolHandlerFunc {
	return func(ctx context.Context, req mcplib.CallToolRequest) (*mcplib.CallToolResult, error) {
		o := originFn(ctx)
		agentID := o.Name
		toolName := req.Params.Name

		ctx = origin.WithContext(ctx, o)
		// The one boundary where the client's name is known: every record made under this
		// call is stamped with it as the origin's Host.
		ctx = trail.ContextWithHost(trail.ContextWithEntryPoint(ctx, types.EntryPointMCP), o.Name)
		// The workspace's secret resolver, so the trail writes below are redacted.
		//
		// This context is an ANCESTOR of any run a tool starts, never a descendant, so it
		// does not inherit the resolver the run installs. Without this the ctx parameter
		// threaded through internal/trail did nothing on the path that motivated it: a
		// request/response pair is a whole tool payload, persisted verbatim to an
		// append-only file, and nothing else on that path scrubs it.
		ctx = withSecrets(ctx)
		// The provider a tool needs to record its OWN domain metric. The wrapper can only
		// see what every tool has in common (name, outcome, sizes, duration); a bounded
		// vocabulary like the job store's base verdict is known to the tool alone, and reading
		// it out of the argument map here would be attributing a metric by caller-supplied
		// text.
		if tel != nil {
			ctx = observability.WithProvider(ctx, tel)
		}

		reqLog := log.With(
			slog.String("agent", agentID),
			slog.String("tool", toolName),
		)
		// The HTTP User-Agent is a second identity signal (empty over stdio);
		// attach it only when present so stdio call logs stay unchanged.
		if o.UserAgent != "" {
			reqLog = reqLog.With(slog.String("user_agent", o.UserAgent))
		}

		reqLog.InfoContext(ctx, "[AGENT] tool called")
		start := time.Now()

		result, err := fn(ctx, req)

		// Capture both sides the agent exchanged with the tool as content-addressed blobs
		// (prefixed "mcp"), keeping only refs on the event so a large body never bloats the
		// trail line. Request = the tool arguments, response = the result text.
		reqRef, reqBytes := trail.WriteBlob(ctx, trailDir, "mcp", argsJSON(req.GetArguments()))
		respText := allText(result)
		respRef, respBytes := trail.WriteBlob(ctx, trailDir, "mcp", []byte(respText))

		dur := time.Since(start)
		ev := trail.Event{
			Ts:   start.UnixMilli(),
			Kind: trail.KindMCPToolCall,
			// Append stamps the origin from ctx: the entry point, the client's name as Host,
			// and the credential authorize checks.
			UserAgent:     o.UserAgent,
			Action:        toolName,
			Outcome:       trail.OutcomeOK,
			DurationMs:    dur.Milliseconds(),
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
			reqLog.ErrorContext(ctx, "[AGENT] tool error", slog.Duration("duration", dur), slog.String("error", err.Error()))
		case result != nil && result.IsError:
			ev.Outcome = trail.OutcomeError // the error text is the response body, captured above
			reqLog.WarnContext(ctx, "[AGENT] tool failed", slog.Duration("duration", dur))
		default:
			reqLog.InfoContext(ctx, "[AGENT] tool done", slog.Duration("duration", dur))
		}
		trail.Append(ctx, trailDir, ev)
		if tel != nil {
			// INPUT = the serialized tool arguments; OUTPUT = every byte the agent is
			// sent, the structured copy of the payload included. Attribute by tool +
			// outcome only to keep metric cardinality bounded.
			tel.RecordMCPCall(ctx, observability.MCPCall{
				Tool:        toolName,
				Outcome:     ev.Outcome,
				InputBytes:  reqBytes,
				OutputBytes: respBytes + structuredBytes(result),
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

// structuredBytes is the encoded size of a result's structured content, which a
// client receives beside the text blocks.
func structuredBytes(result *mcplib.CallToolResult) int64 {
	if result == nil || result.StructuredContent == nil {
		return 0
	}
	b, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return 0
	}
	return int64(len(b))
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
