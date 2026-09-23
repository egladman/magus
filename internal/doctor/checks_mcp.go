package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/egladman/magus/internal/auth"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
)

// expiringSoon is how far ahead checkTokens warns about a stored token's expiry, so a client
// is re-minted before it starts failing auth.
const expiringSoon = 14 * 24 * time.Hour

// checkTokens reports the daemon's credentials. It FAILS on what stops a credential working
// with an error the reader must act on: an operator token file that is not mgo_, a token store
// written before grants, a secret file looser than 0600. It ADVISES when a stored token has
// expired or expires within expiringSoon, and when the magus state dir is readable by other
// accounts. An absent operator token is normal: the daemon mints one on start.
func (*runner) checkTokens() types.DoctorCheck {
	const name = "tokens"
	var (
		fails, advice []string
		parts         []string
	)

	tok, err := auth.Load()
	switch {
	case errors.Is(err, auth.ErrNoToken):
		parts = append(parts, "operator token: absent (the daemon mints one on start)")
	case err != nil:
		fails = append(fails, err.Error())
	default:
		parts = append(parts, "operator token: present (id "+auth.Fingerprint(tok)+")")
	}

	if store, err := auth.LoadStore(); err != nil {
		fails = append(fails, err.Error())
	} else {
		stored := store.List()
		now := time.Now()
		var nearest time.Time
		for _, t := range stored {
			if nearest.IsZero() || t.Expires.Before(nearest) {
				nearest = t.Expires
			}
			revoke := hint.ConfigConsoleTokenRevoke
			if t.Grant.MCP != types.LevelNone {
				revoke = hint.ConfigMCPConnectorRevoke
			}
			switch left := t.Expires.Sub(now); {
			case left <= 0:
				advice = append(advice, fmt.Sprintf("token %q (%s) expired %s; revoke it: %s", t.Name, t.Grant, t.Expires.Format("2006-01-02"), revoke.With(t.Name)))
			case left <= expiringSoon:
				days := fmt.Sprintf("%dd", int(left.Hours())/24)
				if left < 24*time.Hour {
					days = "<1d"
				}
				advice = append(advice, fmt.Sprintf("token %q (%s) expires in %s (%s); mint its replacement before then", t.Name, t.Grant, days, t.Expires.Format("2006-01-02")))
			}
		}
		msg := fmt.Sprintf("%d stored token(s)", len(stored))
		if !nearest.IsZero() {
			msg += "; nearest expiry " + nearest.Format("2006-01-02")
		}
		parts = append(parts, msg)
	}

	if dir, err := auth.StateDir(); err == nil {
		if info, err := os.Stat(dir); err == nil && info.Mode().Perm()&0o077 != 0 {
			advice = append(advice, fmt.Sprintf("%s is readable by other accounts (%#o); tighten it: chmod 700 %s", dir, info.Mode().Perm(), dir))
		}
	}
	// Named even when nothing else is wrong: without landlock a target can read the operator
	// token file, and a gap nobody reports reads as its absence.
	if runtime.GOOS != "linux" {
		parts = append(parts, "no kernel sandbox on "+runtime.GOOS+": a target can read the operator token file")
	}

	status, details := types.DoctorOK, advice
	switch {
	case len(fails) > 0:
		status, details = types.DoctorFail, append(fails, advice...)
	case len(advice) > 0:
		status = types.DoctorAdvice
	}
	return types.DoctorCheck{Name: name, Status: status, Message: strings.Join(parts, "; "), Details: details}
}

// probeBridgeReachability issues a real HTTP GET to /api/v1/graph to confirm
// the bridge route is mounted. A 401 Unauthorized response proves the guarded
// route exists (auth runs before handler). Connection refused means the MCP
// HTTP server is not up. Any other status is treated as unexpected.
//
// The check gates on the bridge's OWN lifecycle (config saying it is served, and a
// persistent `magus server start` daemon being the process on the socket) rather than
// on the proc daemon being reachable. Reachable was the wrong signal and silently so:
// magus spins up a per-process proc server for ordinary commands, so a plain `magus
// doctor` adopts one, sets Reachable, and the skip below could never fire. Every fresh
// machine failed here on a bridge nothing had started.
func probeBridgeReachability(ctx context.Context, d *DaemonInfo) types.DoctorCheck {
	const name = "bridge-reachability"
	if d == nil {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Evidence: types.EvidenceUnknown, Message: "daemon info unavailable; bridge check skipped"}
	}
	if !d.BridgeEnabled {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "bridge disabled via console.enabled: false"}
	}
	if !d.MCPEnabled {
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Message: "bridge not served: mcp.enabled is false, and the bridge is mounted on the MCP server"}
	}
	// No persistent daemon means no bridge, necessarily. Reporting that as a FAILURE
	// made `magus doctor` red on every machine with the daemon stopped (which is the
	// normal state for a CLI-first tool), and a check that is red by default is a
	// check people learn to ignore, taking the real failures with it. The daemon
	// check immediately above already says the daemon is down; saying it twice,
	// once as a failure, is noise rather than information.
	if !d.Persistent {
		return types.DoctorCheck{
			Name:     name,
			Status:   types.DoctorOK,
			Evidence: types.EvidenceUnknown,
			Message:  "no persistent daemon, so the bridge is not expected; skipped",
			Details:  []string{"start it to serve the console: " + hint.ServerStart.String()},
		}
	}
	if d.MCPAddr == "" {
		// Belt-and-suspenders: mcpAddrString normally falls back to the default
		// address, so this only trips if daemonInfo was built without one.
		return types.DoctorCheck{Name: name, Status: types.DoctorOK, Evidence: types.EvidenceUnknown, Message: "MCP address unknown; bridge check skipped"}
	}

	url := fmt.Sprintf("http://%s/api/v1/graph", d.MCPAddr)
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("bridge probe request failed: %s", err.Error()),
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		// Connection refused or timeout: the MCP HTTP server is not up.
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("bridge endpoint not reachable at %s", url),
			Details: []string{
				err.Error(),
				"start the daemon: " + hint.ServerStart.String(),
				"mint a console token: " + hint.ConfigConsoleTokenCreate.String(),
			},
		}
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusUnauthorized:
		// 401 proves the guarded route exists: auth rejected the unauthenticated probe.
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorOK,
			Message: fmt.Sprintf("reachable at %s", url),
			Details: []string{"console token: " + hint.ConfigConsoleTokenCreate.String()},
		}
	case http.StatusForbidden:
		// 403 can come from the DNS-rebind guard; the server is up.
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorOK,
			Message: fmt.Sprintf("reachable at %s (dns-rebind guard active)", url),
			Details: []string{"console token: " + hint.ConfigConsoleTokenCreate.String()},
		}
	default:
		return types.DoctorCheck{
			Name:    name,
			Status:  types.DoctorFail,
			Message: fmt.Sprintf("bridge responded with unexpected status %d at %s", resp.StatusCode, url),
		}
	}
}
