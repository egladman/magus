package doctor

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/egladman/magus/internal/auth"
)

// expiringSoon is how far ahead checkMCPTokens warns about a connector token's
// upcoming expiry, so a rotation can happen before a client starts failing auth.
const expiringSoon = 14 * 24 * time.Hour

// checkMCPTokens surfaces the daemon's auth credentials: whether the retrievable
// cli token is present (with its fingerprint) and a summary of the named
// connector tokens, flagging any that are expired or expiring within
// expiringSoon. It is informational (always StatusOK): an absent cli token is
// normal (the daemon mints one on start) and a stale connector entry is harmless
// (it simply stops authenticating), so neither should fail the CI preflight. The
// check exists to make credential state and upcoming expiries visible.
func (*runner) checkMCPTokens() Check {
	const name = "mcp tokens"

	cliMsg := "cli token: absent (the daemon mints one on start)"
	if tok, err := auth.Load(); err == nil {
		cliMsg = "cli token: present (fingerprint " + auth.Fingerprint(tok) + ")"
	}

	store, err := auth.LoadConnectorStore()
	if err != nil {
		return Check{Name: name, Status: StatusOK, Message: cliMsg, Details: []string{"connector store: " + err.Error()}}
	}

	conns := store.List()
	now := time.Now()
	var nearest time.Time
	var details []string
	for _, c := range conns {
		if c.Expires.IsZero() {
			continue // never expires
		}
		if nearest.IsZero() || c.Expires.Before(nearest) {
			nearest = c.Expires
		}
		switch {
		case now.After(c.Expires):
			details = append(details, fmt.Sprintf("connector %q expired %s; revoke it: magus config mcp connector revoke %s",
				c.Name, c.Expires.Format("2006-01-02"), c.Name))
		case c.Expires.Sub(now) <= expiringSoon:
			label := fmt.Sprintf("%dd", int(c.Expires.Sub(now).Hours())/24)
			if c.Expires.Sub(now) < 24*time.Hour {
				label = "<1d"
			}
			details = append(details, fmt.Sprintf("connector %q expires in %s (%s); rotate it soon",
				c.Name, label, c.Expires.Format("2006-01-02")))
		}
	}

	connMsg := fmt.Sprintf("%d connector token(s)", len(conns))
	if !nearest.IsZero() {
		connMsg += "; nearest expiry " + nearest.Format("2006-01-02")
	}
	return Check{Name: name, Status: StatusOK, Message: cliMsg + "; " + connMsg, Details: details}
}

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
		// Belt-and-suspenders: mcpAddrString normally falls back to the default
		// address, so this only trips if daemonInfo was built without one.
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
