package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/interactive/clihint"
	"github.com/egladman/magus/internal/proc"
	"github.com/egladman/magus/types"
)

// probeKind identifies which Kubernetes probe check to perform.
type probeKind int

const (
	probeLiveness  probeKind = iota // daemon answers the status RPC
	probeReadiness                  // daemon answers AND target workspace is loaded
	probeMCP                        // the MCP HTTP endpoint an agent host connects to is reachable
)

// parseProbeKind converts a single flag token to a probeKind.
func parseProbeKind(s string) (probeKind, error) {
	switch s {
	case "liveness":
		return probeLiveness, nil
	case "readiness":
		return probeReadiness, nil
	case "mcp":
		return probeMCP, nil
	default:
		return 0, fmt.Errorf("unknown probe kind %q: must be liveness, readiness, or mcp", s)
	}
}

// parseProbeKinds parses a comma-combinable --probe value into one or more kinds,
// mirroring resolveRace's convention (trim, tolerate empty segments like "mcp," or
// ",liveness", error on unknown, dedupe). At least one kind is required.
func parseProbeKinds(s string) ([]probeKind, error) {
	var kinds []probeKind
	seen := map[probeKind]bool{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kind, err := parseProbeKind(part)
		if err != nil {
			return nil, err
		}
		if !seen[kind] {
			seen[kind] = true
			kinds = append(kinds, kind)
		}
	}
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no probe kind given: choose one or more of liveness, readiness, mcp")
	}
	return kinds, nil
}

// probeName renders a probe kind for multi-probe output labeling.
func probeName(kind probeKind) string {
	switch kind {
	case probeLiveness:
		return "liveness"
	case probeReadiness:
		return "readiness"
	case probeMCP:
		return "mcp"
	default:
		return "unknown"
	}
}

// evaluateHealth reports whether the daemon is healthy. root, if non-empty, pins readiness to a specific workspace.
func evaluateHealth(status *types.StatusOutput, err error, kind probeKind, root string) (ok bool, reason string) {
	if err != nil || status == nil {
		if err != nil {
			return false, fmt.Sprintf("daemon unreachable: %v", err)
		}
		return false, "daemon unreachable"
	}
	if kind == probeLiveness {
		return true, fmt.Sprintf("daemon pid %d is alive", status.ParentPID)
	}
	// Readiness is a multi-workspace daemon concept — only the daemon reports
	// which workspaces are loaded. A per-process proc server never does, so
	// report that honestly instead of a misleading "no workspaces loaded".
	if status.Mode == "proc" {
		return false, "daemon is in per-process mode; readiness requires `magus server start`"
	}
	if root != "" {
		clean := filepath.Clean(root)
		for _, ws := range status.Workspaces {
			if filepath.Clean(ws.Root) == clean {
				return true, fmt.Sprintf("workspace %s is loaded", root)
			}
		}
		return false, fmt.Sprintf("workspace %s is not loaded", root)
	}
	if len(status.Workspaces) > 0 {
		return true, fmt.Sprintf("%d workspace(s) loaded", len(status.Workspaces))
	}
	return false, "no workspaces loaded"
}

// probeResult is one probe's verdict: which kind ran, whether it passed, and the
// human-readable reason. evaluateProbes returns these; runProbes renders them.
type probeResult struct {
	kind   probeKind
	ok     bool
	reason string
}

// evaluateProbes runs each requested probe and returns a result per kind, in order. It is
// the decision half of runProbes, kept pure of output and process exit so the exit-code
// contract (which K8s and shell guards depend on) is unit-testable. statusOf supplies the
// daemon snapshot for socket-based probes and is called at most once, and only when a
// non-mcp probe needs it: an mcp-only invocation makes no proc RPC at all. mcp holds the
// endpoint config the mcp probe reads, passed in so this stays free of package globals.
func evaluateProbes(ctx context.Context, statusOf statusFunc, mcp config.MCP, kinds []probeKind, root string) []probeResult {
	var daemonSnap *types.StatusOutput
	var daemonErr error
	dialed := false

	results := make([]probeResult, 0, len(kinds))
	for _, kind := range kinds {
		var ok bool
		var reason string
		if kind == probeMCP {
			ok, reason = evaluateMCPHealth(buildMCPEndpointStatus(ctx, mcp))
		} else {
			if !dialed {
				daemonSnap, daemonErr = statusOf(ctx)
				dialed = true
			}
			ok, reason = evaluateHealth(daemonSnap, daemonErr, kind, root)
		}
		results = append(results, probeResult{kind: kind, ok: ok, reason: reason})
	}
	return results
}

// runProbes evaluates every requested probe and exits non-zero if ANY is unhealthy, so
// `--probe=liveness,mcp` fails when either the daemon or the MCP endpoint is down. Each
// result prints on its own line ("ok: <reason>" on stdout, the reason on stderr for a
// failure); with more than one probe each line is prefixed with its kind so the caller
// can tell which dimension failed. The mcp probe targets the HTTP endpoint an agent host
// connects to (not the proc socket the liveness/readiness probes dial), so it fails
// exactly when the tools are unreachable even if the daemon itself answers.
func runProbes(ctx context.Context, socket string, mcp config.MCP, kinds []probeKind, root string) error {
	results := evaluateProbes(ctx, daemonStatus(socket), mcp, kinds, root)
	if renderProbeResults(os.Stdout, os.Stderr, results) {
		return nil
	}
	return errSilent{exitCode: 1}
}

// renderProbeResults writes each result ("ok: <reason>" to stdout, the reason to stderr on
// failure) and returns whether all passed. With more than one probe each line is prefixed
// with its kind so a combined `--probe=liveness,mcp` shows which dimension failed.
func renderProbeResults(stdout, stderr io.Writer, results []probeResult) (allOK bool) {
	allOK = true
	for _, r := range results {
		label := ""
		if len(results) > 1 {
			label = probeName(r.kind) + ": "
		}
		if r.ok {
			fmt.Fprintln(stdout, "ok:", label+r.reason)
		} else {
			fmt.Fprintln(stderr, label+r.reason)
			allOK = false
		}
	}
	return allOK
}

// evaluateMCPHealth reports whether the MCP endpoint is reachable, with a reason. A
// reachable endpoint (serving or listening-but-not-ready) passes: for an ensure/liveness
// check the daemon is up either way. Unreachable and disabled both fail, carrying the
// status note (which points at `magus server start`, or names the disabling config).
func evaluateMCPHealth(m *types.MCPEndpointStatus) (ok bool, reason string) {
	if m == nil {
		return false, "mcp endpoint status unavailable"
	}
	if m.Reachable {
		return true, fmt.Sprintf("mcp endpoint %s at %s", m.State, m.URL)
	}
	if m.Note != "" {
		return false, m.Note
	}
	return false, "mcp endpoint " + m.State
}

// statusFunc returns the daemon's current status snapshot for a health check.
// It is a seam so tests can supply a snapshot without dialing a live socket.
type statusFunc func(ctx context.Context) (*types.StatusOutput, error)

// daemonStatus dials socket (auto-discovered when empty) for a live status snapshot.
func daemonStatus(socket string) statusFunc {
	return func(ctx context.Context) (*types.StatusOutput, error) {
		addr, err := resolveStatusSocket(ctx, socket)
		if err != nil {
			return nil, err
		}
		reply, err := proc.QueryStatus(ctx, addr)
		if err != nil {
			return nil, err
		}
		return statusOutputFromReply(reply), nil
	}
}

// mcpProbeTimeout bounds the HTTP probe of the MCP endpoint from `magus status`.
// Short so a `--watch` poll stays snappy; a loopback connection-refused returns well
// inside it, and a reachable endpoint answers immediately.
const mcpProbeTimeout = time.Second

// buildMCPEndpointStatus reports the runtime health of the MCP HTTP endpoint an agent
// host connects to. It probes the endpoint's own HTTP listener - not the proc socket
// the Pool fields report - because that listener is what a connecting agent sees, and
// the two can diverge. Never returns nil so the status render always has the endpoint's
// address and state to show. The MCP config is passed in (not read from globalCfg) so
// the function is self-contained and testable with an explicit config.
func buildMCPEndpointStatus(ctx context.Context, mcp config.MCP) *types.MCPEndpointStatus {
	if mcp.Enabled != nil && !*mcp.Enabled {
		return &types.MCPEndpointStatus{
			State: "disabled",
			Note:  "MCP is disabled (mcp.enabled=false); no agent tools are served.",
		}
	}
	addr := mcpAddress(mcp)
	st := &types.MCPEndpointStatus{
		Enabled: true,
		Address: addr,
		URL:     "http://" + addr + "/mcp",
	}
	pctx, cancel := context.WithTimeout(ctx, mcpProbeTimeout)
	defer cancel()
	switch probeMCPReadiness(pctx, addr) {
	case http.StatusOK:
		st.Reachable = true
		st.State = "serving"
	case http.StatusServiceUnavailable:
		st.Reachable = true
		st.State = "not-ready"
		st.Note = "MCP endpoint is listening but no workspace is loaded yet."
	default:
		st.State = "unreachable"
		st.Note = fmt.Sprintf("nothing is serving MCP at %s; start the daemon: %s", addr, clihint.ServerStart)
	}
	return st
}

// probeMCPReadiness GETs the endpoint's /readyz and returns the HTTP status, or 0 when
// nothing answered (connection refused/timeout). 200 = ready, 503 = listening but no
// workspace loaded; any other answered status is treated by the caller as reachable.
func probeMCPReadiness(ctx context.Context, addr string) int {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/readyz", nil)
	if err != nil {
		return 0
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	// An answered status other than 200/503 (e.g. an older daemon without /readyz)
	// still proves a listener is up; report OK so the endpoint reads as reachable.
	switch resp.StatusCode {
	case http.StatusOK, http.StatusServiceUnavailable:
		return resp.StatusCode
	default:
		return http.StatusOK
	}
}

// healthHTTPHandler returns an http.HandlerFunc that writes 200 when healthy or 503 with a reason line.
// Accepts ?workspace= to pin readiness to a specific workspace root.
func healthHTTPHandler(kind probeKind, status statusFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		snapshot, err := status(r.Context())
		ok, reason := evaluateHealth(snapshot, err, kind, r.URL.Query().Get("workspace"))
		if ok {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		fmt.Fprintln(w, reason)
	}
}
