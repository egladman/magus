// Package graphurl builds loopback Graph Explorer links with a pre-applied query
// or named view, so a magus CLI command can print a clickable "view this in the
// Graph Explorer" line as a COMPLEMENTARY aid alongside its normal output.
//
// The link reproduces `magus graph open --live` (cmd/magus/graph_open.go,
// graphOpenLive): the explorer page is served from the hosted, static origin
// (ExplorerBase), and the running daemon's loopback host:port rides in the
// `#live=` fragment directive together with the bearer token. The page reads the
// directive on load and fetches the graph DATA from http://<host>/api/v1/graph -
// the daemon serves that API over loopback, but NOT the static page itself, so
// the origin is the hosted explorer, never http://<host>/... directly.
//
// On top of the live directive, the caller can pre-apply the fragment directives
// the page honors on load (console/src/console/graph/main.ts applyDeepLinks):
// `q=` auto-runs a search, and `view=` activates a named view with optional
// `node=`/`to=` foci. Only directives that are set are emitted.
//
// The helper is pure: it formats a URL from values the caller already holds (the
// loopback host, and a best-effort token). The token is optional; a link built
// without one still opens the explorer, it just is not pre-authenticated.
package url

import (
	"errors"
	"net/url"
	"strings"
)

// DefaultExplorerBase is the hosted, static Graph Explorer origin. It mirrors
// defaultExploreURL in cmd/magus/graph_open.go and internal/handler/mcp. The
// page is static; the live directive points it back at the loopback daemon for
// data, so the graph never leaves the machine.
const DefaultExplorerBase = "https://eli.gladman.cc/magus/console/graph/"

// ErrNoDaemon is returned when Host is empty: there is no loopback address to put
// in the `#live=` directive, so no link can be built. Callers treat it as "omit
// the link", not a hard failure.
var ErrNoDaemon = errors.New("graphurl: no daemon host; omit the link")

// GraphLinkOpts is the input to GraphLink. Host comes from the daemon address the
// caller already resolves; Token is a best-effort bearer token; the four directive
// fields are all optional.
type GraphLinkOpts struct {
	// Host is the daemon's loopback host:port, e.g. "127.0.0.1:7391". It rides
	// raw in `#live=` (the page validates it is literally 127.0.0.1 or [::1]
	// before any request). Empty Host yields ErrNoDaemon.
	Host string
	// Token is the bearer token for the live API. It is embedded in the fragment
	// (never transmitted in an HTTP request) and stripped by the page on load.
	// Optional: when empty the `token=` directive is omitted and the explorer
	// opens unauthenticated.
	Token string
	// ExplorerBase overrides the hosted explorer origin (for a self-hosted mirror
	// or tests). Empty means DefaultExplorerBase.
	ExplorerBase string

	// Query, when set, is dropped into `q=` and auto-runs a node search on load.
	Query string
	// View, when set, is dropped into `view=` and activates a named view. The
	// page honors blast, trace, critical, hubs, and orphans; unknown views are
	// ignored by the page. A set View takes precedence over Query on the page.
	View string
	// Node, when set, is dropped into `node=` - the focus node for a blast/trace
	// view, or the node to select alongside a query.
	Node string
	// To, when set, is dropped into `to=` - the destination node for a trace view.
	To string
}

// GraphLink formats a live Graph Explorer URL with the given directives applied.
// It returns ErrNoDaemon when Host is empty. The fragment is
// `#live=<host>[&token=<tok>][&q=][&view=][&node=][&to=]`, with only the
// directives that are set included and every value percent-encoded so the page's
// decodeURIComponent-based hash parser reads it back exactly.
func GraphLink(opts GraphLinkOpts) (string, error) {
	if opts.Host == "" {
		return "", ErrNoDaemon
	}

	base := opts.ExplorerBase
	if base == "" {
		base = DefaultExplorerBase
	}

	// The live directive first, matching graphOpenLive: the loopback host raw
	// (the page parses it as a URL host), then the optional token and the
	// view/query directives, in a fixed order for stable output.
	parts := []string{"live=" + opts.Host}
	if opts.Token != "" {
		parts = append(parts, "token="+encodeComponent(opts.Token))
	}
	if opts.Query != "" {
		parts = append(parts, "q="+encodeComponent(opts.Query))
	}
	if opts.View != "" {
		parts = append(parts, "view="+encodeComponent(opts.View))
	}
	if opts.Node != "" {
		parts = append(parts, "node="+encodeComponent(opts.Node))
	}
	if opts.To != "" {
		parts = append(parts, "to="+encodeComponent(opts.To))
	}

	return strings.TrimRight(base, "/") + "/#" + strings.Join(parts, "&"), nil
}

// encodeComponent percent-encodes s the way the browser's encodeURIComponent
// does, which is what the page's hash parser reverses with decodeURIComponent.
// url.QueryEscape is application/x-www-form-urlencoded, which encodes a space as
// "+"; decodeURIComponent would leave that "+" literal, so the "+"->"%20" fixup
// is required. A literal "+" in s is already emitted as "%2B" by QueryEscape, so
// the fixup never corrupts it.
func encodeComponent(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
