//go:build mcp

// Package webbridge mounts three frozen, read-only GET routes on the MCP HTTP
// server so a browser running the hosted Graph Explorer can view the current
// workspace over loopback.
//
// The entire API surface is frozen at birth (v1). No mutations, ever (see
// section 0.3 of the PWA plan). Every route is guarded by the same bearer
// token as the MCP server and protected by the same DNS-rebind middleware.
package webbridge

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"sync/atomic"
	"time"

	magus "github.com/egladman/magus"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/knowledge"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// Options carries everything the bridge needs at mount time. All fields are
// required except DaemonSocket (empty means auto-discover).
type Options struct {
	// Magus is the opened workspace. The bridge calls KnowledgeGraph and
	// DescribeGraph; it never writes.
	Magus *magus.Magus

	// Config is the resolved workspace config. Used to discover the daemon
	// socket (Config.Daemon.Address) for /api/v1/status.
	Config config.Config

	// StatusBase carries the static portions of the status report (telemetry,
	// cache, build) that the bridge cannot compute itself because they depend on
	// build-tag constants and config that live in cmd/magus. The caller
	// (mcp/server.go -> cmd/magus) populates this; the bridge merges it with the
	// live pool query from the proc socket to produce the full types.StatusReport
	// that matches the shape of `magus status -o json`.
	StatusBase types.StatusBase

	// Addr is the address the HTTP server is listening on. Used to derive the
	// CORS loopback origin (http://127.0.0.1:<port> / http://localhost:<port>).
	Addr netip.AddrPort

	// SiteOrigin is the configured explorer origin (e.g.
	// "https://eli.gladman.cc"). CORS allows this origin plus the loopback
	// ones derived from Addr. Use siteOriginFromConfig to populate this.
	SiteOrigin string

	// DaemonSocket is the explicit daemon socket address. When empty the bridge
	// calls proc.DiscoverSocket at request time.
	DaemonSocket string

	// GraphInvalidate is an optional channel that is written to (non-blocking)
	// when the workspace graph is invalidated by a file-system change. The SSE
	// /api/v1/events handler reads from it to push graph events to browsers.
	// When nil, the SSE stream emits only heartbeats.
	//
	// The channel MUST be buffered (capacity >= 1). Writers must use a
	// non-blocking send (select with default) so a slow client cannot block the
	// file-watcher goroutine.
	GraphInvalidate <-chan struct{}

	// HeartbeatInterval overrides the default 25-second SSE heartbeat period.
	// Zero uses the default. Exposed for testing; production code leaves it zero.
	HeartbeatInterval time.Duration

	// KnowledgeGraphFn, when non-nil, is called instead of Magus.KnowledgeGraph.
	// Intended for tests that want to drive graph-serving paths without a real
	// workspace. Production code leaves this nil.
	KnowledgeGraphFn func(ctx context.Context, withSymbols bool) (*knowledge.Graph, error)

	// DescribeGraphFn, when non-nil, is called instead of Magus.DescribeGraph.
	// Intended for tests; production code leaves this nil.
	DescribeGraphFn func() types.TargetGraphOutput
}

// Mount registers /api/v1/graph, /api/v1/status, and /api/v1/events on mux.
// The caller is responsible for wrapping with dnsRebindGuard and auth.Guard
// (same as /mcp). Mount itself does not check that addr is loopback -- the
// caller must do that before invoking Mount.
//
// The three routes are GET-only read endpoints. No mutation surface exists.
func Mount(mux *http.ServeMux, opts Options) {
	port := opts.Addr.Port()
	cors := corsMiddleware(opts.SiteOrigin, port)

	mux.Handle("/api/v1/graph", cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleGraph(w, r, opts)
	})))
	mux.Handle("/api/v1/status", cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleStatus(w, r, opts)
	})))
	mux.Handle("/api/v1/events", cors(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handleEvents(w, r, opts)
	})))
}

// handleGraph serves GET /api/v1/graph.
//
// Query params (all frozen v1):
//   - ?flavor=targets  -> describe graph -o json (types.TargetGraphOutput)
//   - ?level=projects  -> skeleton: projects + project->project edges only
//   - ?select=<terms>  -> scoped neighborhood (graph export --select semantics)
//
// Exactly one of flavor, level, or select may be non-empty; combinations are
// rejected. The whole-graph export (no params) is the default.
//
// Symbols are NOT loaded unless the select terms seed symbols
// (knowledge.SeedsSymbols). This preserves the store's lazy-load contract:
// @symbols shards are excluded from the default export.
//
// ETag is sha256 of the response body variant, computed before writing.
// If-None-Match matching yields 304 with no body.
// Accept-Encoding: gzip yields a compressed body.
func handleGraph(w http.ResponseWriter, r *http.Request, opts Options) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	flavor := q.Get("flavor")
	level := q.Get("level")
	sel := q.Get("select")

	// Reject ambiguous combinations.
	set := 0
	if flavor != "" {
		set++
	}
	if level != "" {
		set++
	}
	if sel != "" {
		set++
	}
	if set > 1 {
		http.Error(w, "at most one of flavor, level, select may be specified", http.StatusBadRequest)
		return
	}
	if flavor != "" && flavor != "targets" {
		http.Error(w, "flavor must be 'targets' or empty", http.StatusBadRequest)
		return
	}
	if level != "" && level != "projects" {
		http.Error(w, "level must be 'projects' or empty", http.StatusBadRequest)
		return
	}

	var (
		body []byte
		err  error
	)
	switch {
	case flavor == "targets":
		body, err = buildTargetGraph(r.Context(), opts)
	case level == "projects":
		body, err = buildProjectSkeleton(r.Context(), opts)
	case sel != "":
		body, err = buildSelectGraph(r.Context(), opts, sel)
	default:
		body, err = buildFullGraph(r.Context(), opts)
	}
	if err != nil {
		http.Error(w, "graph build error: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// ETag: sha256 of the body, hex-encoded and quoted per RFC 7232.
	sum := sha256.Sum256(body)
	etag := fmt.Sprintf(`"%x"`, sum)

	if r.Header.Get("If-None-Match") == etag {
		// RFC 7232 §4.1: a 304 response MUST include the ETag that would have
		// been sent in the 200, so the client can update its cache entry.
		w.Header().Set("ETag", etag)
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-store")

	if acceptsGzip(r) {
		w.Header().Set("Content-Encoding", "gzip")
		// Vary informs caches that the response differs by Accept-Encoding.
		w.Header().Set("Vary", "Accept-Encoding")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write(body)
		_ = gz.Close()
		return
	}
	_, _ = w.Write(body)
}

func buildFullGraph(ctx context.Context, opts Options) ([]byte, error) {
	var (
		g   *knowledge.Graph
		err error
	)
	if opts.KnowledgeGraphFn != nil {
		g, err = opts.KnowledgeGraphFn(ctx, false)
	} else {
		g, err = opts.Magus.KnowledgeGraph(ctx, false)
	}
	if err != nil {
		return nil, err
	}
	out := g.Output()
	return json.Marshal(out)
}

func buildSelectGraph(ctx context.Context, opts Options, sel string) ([]byte, error) {
	// Symbol shards are loaded only when the selection actually seeds symbols.
	var g *knowledge.Graph
	var err error
	if opts.KnowledgeGraphFn != nil {
		g, err = opts.KnowledgeGraphFn(ctx, knowledge.SeedsSymbols(sel))
	} else if knowledge.SeedsSymbols(sel) {
		g, err = opts.Magus.KnowledgeGraphWithSymbols(ctx)
	} else {
		g, err = opts.Magus.KnowledgeGraph(ctx, false)
	}
	if err != nil {
		return nil, err
	}
	out := g.Select(sel, knowledge.DefaultBudget)
	return json.Marshal(out)
}

func buildProjectSkeleton(ctx context.Context, opts Options) ([]byte, error) {
	// The skeleton is derived from the target graph: take the TargetGraphOutput
	// and reduce it to project nodes plus project->project depends_on edges.
	// This keeps the payload at KBs at any scale.
	var tg types.TargetGraphOutput
	if opts.DescribeGraphFn != nil {
		tg = opts.DescribeGraphFn()
	} else {
		tg = opts.Magus.DescribeGraph()
	}
	skeleton := projectSkeleton(tg)
	return json.Marshal(skeleton)
}

func buildTargetGraph(_ context.Context, opts Options) ([]byte, error) {
	var out types.TargetGraphOutput
	if opts.DescribeGraphFn != nil {
		out = opts.DescribeGraphFn()
	} else {
		out = opts.Magus.DescribeGraph()
	}
	return json.Marshal(out)
}

// projectSkeleton reduces a TargetGraphOutput to only project nodes and
// project->project depends_on edges, producing a KnowledgeGraphOutput that
// the explorer can render as the collapsed default view.
func projectSkeleton(tg types.TargetGraphOutput) types.KnowledgeGraphOutput {
	nodes := make([]types.KnowledgeNode, 0, len(tg.Projects))
	var links []types.KnowledgeEdge

	for _, p := range tg.Projects {
		nodes = append(nodes, types.KnowledgeNode{
			ID:    p.Path,
			Kind:  "project",
			Label: p.Path,
		})
		for _, dep := range p.DependsOn {
			links = append(links, types.KnowledgeEdge{
				Source:   p.Path,
				Target:   dep,
				Relation: "depends_on",
			})
		}
	}

	return types.KnowledgeGraphOutput{
		Definition:    tg.Definition,
		SchemaVersion: types.KnowledgeSchemaVersion,
		Directed:      true,
		NodeCount:     len(nodes),
		EdgeCount:     len(links),
		Nodes:         nodes,
		Links:         links,
	}
}

// handleStatus serves GET /api/v1/status.
// Returns the same JSON as `magus status -o json` (types.StatusReport): the
// telemetry, cache, and build fields come from opts.StatusBase (populated by
// the caller at mount time); pool and pool_error are queried live from the
// proc socket so the response always reflects the current daemon state.
func handleStatus(w http.ResponseWriter, r *http.Request, opts Options) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	out := types.StatusReport{
		Telemetry: opts.StatusBase.Telemetry,
		Cache:     opts.StatusBase.Cache,
		Build:     opts.StatusBase.Build,
	}
	addr, err := resolveStatusAddr(r.Context(), opts)
	if err != nil {
		out.PoolError = err.Error()
	} else {
		reply, qerr := proc.QueryStatus(r.Context(), addr)
		if qerr != nil {
			out.PoolError = qerr.Error()
		} else {
			out.Pool = statusOutputFromReply(reply)
		}
	}

	body, merr := json.Marshal(out)
	if merr != nil {
		http.Error(w, "marshal error: "+merr.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// statusOutputFromReply converts a proc.StatusReply into a types.StatusOutput,
// mirroring the conversion in cmd/magus/status.go so both consumers produce
// identical shapes.
//
// It deliberately leaves StatusOutput.Affected unset (deferred, not an
// oversight): computing it needs a workspace-scoped VCS diff (magus.Magus.
// Affected), which is a meaningfully heavier per-request operation than the
// rest of this handler and was judged not worth adding under this change.
// The Graph Explorer's live "affected" view is correspondingly kept disabled
// client-side (see website/js/graph-explorer.js fetchLiveStatus) rather than
// wired to a field this handler never populates.
func statusOutputFromReply(r *proc.StatusReply) *types.StatusOutput {
	if r == nil {
		return nil
	}
	out := &types.StatusOutput{
		ParentPID:     r.ParentPID,
		DaemonVersion: r.DaemonVersion,
		Mode:          r.Mode,
		Capacity:      r.Capacity,
		InUse:         r.InUse,
		Waiting:       r.Waiting,
	}
	for _, c := range r.Calls {
		out.Calls = append(out.Calls, types.StatusCall{
			Args:      c.Args,
			Workspace: c.Workspace,
			StartedAt: c.StartedAt,
			SubOp:     c.SubOp,
		})
	}
	for _, ws := range r.Workspaces {
		out.Workspaces = append(out.Workspaces, types.StatusWorkspace{
			Root:       ws.Root,
			LoadedAt:   ws.LoadedAt,
			LastAccess: ws.LastAccess,
		})
	}
	return out
}

func resolveStatusAddr(ctx context.Context, opts Options) (string, error) {
	if v := opts.Config.Daemon.Address; v != "" {
		return v, nil
	}
	if opts.DaemonSocket != "" {
		return opts.DaemonSocket, nil
	}
	return proc.DiscoverSocket(ctx)
}

// handleEvents serves GET /api/v1/events as a Server-Sent Events stream.
//
// Events:
//   - event: graph, data: {"seq": N}   -- workspace graph changed (N is monotonic)
//   - event: status, data: {"seq": N}  -- pool state changed (not yet implemented; reserved)
//   - comment-line heartbeat every 25s
//
// Clients must refetch /api/v1/graph on a graph event; no diff is embedded.
// The stream is driven by opts.Watch.Events(); when Watch is nil only
// heartbeats are emitted.
func handleEvents(w http.ResponseWriter, r *http.Request, opts Options) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx proxy buffering

	var seq atomic.Int64

	hbInterval := opts.HeartbeatInterval
	if hbInterval <= 0 {
		hbInterval = 25 * time.Second
	}
	heartbeat := time.NewTicker(hbInterval)
	defer heartbeat.Stop()

	inv := opts.GraphInvalidate

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case _, ok := <-inv:
			if !ok {
				// Channel closed; keep sending heartbeats but no more graph events.
				inv = nil
				continue
			}
			n := seq.Add(1)
			fmt.Fprintf(w, "event: graph\ndata: {\"seq\": %d}\n\n", n)
			flusher.Flush()
		}
	}
}

// corsMiddleware returns a middleware that adds CORS headers allowing only the
// configured site origin and the loopback origins derived from the server port.
// It answers OPTIONS preflights and adds Access-Control-Allow-Private-Network
// when requested (Chrome Private Network Access).
//
// The allowed origins are:
//   - siteOrigin (e.g. "https://eli.gladman.cc")
//   - http://localhost:<port>
//   - http://127.0.0.1:<port>
func corsMiddleware(siteOrigin string, port uint16) func(http.Handler) http.Handler {
	allowed := map[string]bool{
		siteOrigin:                               siteOrigin != "",
		fmt.Sprintf("http://localhost:%d", port): true,
		fmt.Sprintf("http://127.0.0.1:%d", port): true,
	}
	// Remove empty-string key if siteOrigin was empty.
	delete(allowed, "")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Vary", "Origin")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
					w.Header().Set("Access-Control-Allow-Private-Network", "true")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// acceptsGzip reports whether the request accepts gzip-encoded responses.
func acceptsGzip(r *http.Request) bool {
	for _, v := range r.Header.Values("Accept-Encoding") {
		for _, tok := range splitCSV(v) {
			if tok == "gzip" {
				return true
			}
		}
	}
	return false
}

// splitCSV splits a comma-separated Accept-Encoding value into trimmed tokens.
func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			tok := trimSpace(s[start:i])
			// strip quality value e.g. "gzip;q=0.9"
			for j := 0; j < len(tok); j++ {
				if tok[j] == ';' {
					tok = trimSpace(tok[:j])
					break
				}
			}
			if tok != "" {
				out = append(out, tok)
			}
			start = i + 1
		}
	}
	return out
}

func trimSpace(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t') {
		s = s[:len(s)-1]
	}
	return s
}
