//go:build mcp

package doctor

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// probeBridgeReachability issues a real HTTP GET to /api/v1/graph to confirm
// the bridge route is mounted. A 401 Unauthorized response proves the guarded
// route exists (auth runs before handler). Connection refused means the MCP
// HTTP server is not up. Any other status is treated as unexpected.
//
// The check gates on d.MCPAddr being non-empty and d.BridgeEnabled being true,
// independent of whether the proc daemon is reachable (the bridge lives on the
// MCP HTTP server, which has a distinct lifecycle from the proc socket).
func probeBridgeReachability(d *DaemonInfo) Check {
	const name = "web bridge"
	if d == nil {
		return Check{Name: name, Status: StatusOK, Message: "daemon info unavailable; bridge check skipped"}
	}
	if !d.BridgeEnabled {
		return Check{Name: name, Status: StatusOK, Message: "bridge disabled via bridge.enabled: false"}
	}
	if d.MCPAddr == "" {
		// MCPAddr is empty on non-mcp builds via the build-tagged mcpAddrPortString;
		// this guard is kept here as a belt-and-suspenders check.
		return Check{Name: name, Status: StatusOK, Message: "MCP address unknown; bridge check skipped"}
	}

	url := fmt.Sprintf("http://%s/api/v1/graph", d.MCPAddr)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Check{
			Name:    name,
			Status:  StatusFail,
			Message: fmt.Sprintf("bridge probe request failed: %s", err.Error()),
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Connection refused or timeout: the MCP HTTP server is not up.
		return Check{
			Name:    name,
			Status:  StatusFail,
			Message: fmt.Sprintf("bridge endpoint not reachable at %s", url),
			Details: []string{
				err.Error(),
				"start the daemon: magus server start",
				"retrieve the bearer token: magus config mcp token print",
			},
		}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		// 401 proves the guarded route exists: auth rejected the unauthenticated probe.
		return Check{
			Name:    name,
			Status:  StatusOK,
			Message: fmt.Sprintf("reachable at %s", url),
			Details: []string{"bearer token: magus config mcp token print"},
		}
	case http.StatusForbidden:
		// 403 can come from the DNS-rebind guard; the server is up.
		return Check{
			Name:    name,
			Status:  StatusOK,
			Message: fmt.Sprintf("reachable at %s (dns-rebind guard active)", url),
			Details: []string{"bearer token: magus config mcp token print"},
		}
	default:
		return Check{
			Name:    name,
			Status:  StatusFail,
			Message: fmt.Sprintf("bridge responded with unexpected status %d at %s", resp.StatusCode, url),
		}
	}
}
