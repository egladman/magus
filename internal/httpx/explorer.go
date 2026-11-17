package httpx

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"time"
)

// CheckHostedPageOrigin passes a loopback base and an origin named in allowed.
func CheckHostedPageOrigin(base string, allowed []string) error {
	origin, err := ParseOrigin(base)
	if err != nil {
		return err
	}
	if slices.Contains(allowed, origin) || isLoopbackOrigin(origin) {
		return nil
	}
	return fmt.Errorf("the page at %s is not in mcp.allowed_origins", origin)
}

// isLoopbackOrigin reports whether origin names localhost or a loopback address.
func isLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	if u.Hostname() == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(u.Hostname())
	return err == nil && ip.Unmap().IsLoopback()
}

// ProbeLiveBridgeTimeout bounds the real HTTP probe of the console below.
const ProbeLiveBridgeTimeout = 2 * time.Second

// ProbeLiveBridge issues a real HTTP GET to the console's guarded
// /api/v1/graph route to confirm it is actually up, mirroring the doctor
// bridge check (internal/doctor/checks_mcp.go probeBridgeReachability). A
// server-status probe alone is not enough: serverSnapshot accepts ANY
// reachable proc socket, which is a different transport than
// the console this URL targets: a proc-mode server with no bridge running
// would otherwise let a token be printed for an address nothing is listening
// on. A 401/403 response proves the guarded route exists (auth runs before
// the handler); connection refused/timeout means the bridge is down.
func ProbeLiveBridge(ctx context.Context, addr string) error {
	target := fmt.Sprintf("http://%s/api/v1/graph", addr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("bridge not reachable at %s: %w", target, err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil
	default:
		return fmt.Errorf("bridge at %s responded with unexpected status %d", target, resp.StatusCode)
	}
}

// LiveBridgeReachable reports whether the console at addr is actually up, for the
// zero-arg auto-switch in graphOpen. It never emits a token; it only decides
// whether to attempt live mode at all.
func LiveBridgeReachable(ctx context.Context, addr string) bool {
	pctx, cancel := context.WithTimeout(ctx, ProbeLiveBridgeTimeout)
	defer cancel()
	return ProbeLiveBridge(pctx, addr) == nil
}
