// Package graphurl builds daemon-origin Graph Explorer links with a pre-applied
// query or named view, so a magus CLI command can print a clickable "view this in
// the Graph Explorer" line as a COMPLEMENTARY aid alongside its normal output.
//
// The link reproduces `magus graph open --live` (cmd/magus/graph_open.go,
// graphOpenLive) under the daemon-origin grammar: the ORIGIN names which daemon,
// and the page is served by that daemon from its own loopback /console/graph/. So
// the link is http://<host>/console/graph/#<directives>, where <host> is the
// daemon's loopback host:port; the browser loads the page and its graph DATA
// (http://<host>/api/v1/graph) from the one same origin - nothing rides a hosted
// static origin, and no #live= daemon param is needed (the origin already says
// which daemon).
//
// On top of that, the caller can pre-apply the fragment directives the page honors
// on load (console/src/console/graph/main.ts applyDeepLinks): `q=` auto-runs a
// search, and `view=` activates a named view with optional `node=`/`to=` foci.
// Only directives that are set are emitted; the bearer token, when present, rides
// the fragment as `token=` (never transmitted on the document GET).
//
// The helper is pure: it formats a URL from values the caller already holds (the
// loopback host, and a best-effort token). The token is optional; a link built
// without one still opens the explorer, it just is not pre-authenticated.
//
// It does NOT hand-build the URL string: it composes internal/service/console.Link,
// the single home for the daemon-origin console URL grammar, so the escaping policy
// (encodeURIComponent-equivalent) and the "/console/<surface>/#...token-last" shape
// live in one place for every producer.
package graphurl

import (
	"errors"

	"github.com/egladman/magus/internal/service/console"
)

// ErrNoDaemon is returned when Host is empty: there is no loopback origin to build
// the link from, so no link can be built. Callers treat it as "omit the link", not
// a hard failure.
var ErrNoDaemon = errors.New("graphurl: no daemon host; omit the link")

// GraphLinkOpts is the input to GraphLink. Host comes from the daemon address the
// caller already resolves; Token is a best-effort bearer token; the four directive
// fields are all optional.
type GraphLinkOpts struct {
	// Host is the daemon's loopback host:port, e.g. "127.0.0.1:7391". It is the
	// link's ORIGIN: the page is served from http://<Host>/console/graph/. Empty
	// Host yields ErrNoDaemon.
	Host string
	// Token is the bearer token for the live API. It is embedded in the fragment
	// (never transmitted in an HTTP request) and stripped by the page on load.
	// Optional: when empty the `token=` directive is omitted and the explorer
	// opens unauthenticated.
	Token string

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

// GraphLink formats a daemon-origin Graph Explorer URL with the given directives
// applied. It returns ErrNoDaemon when Host is empty. The result is
// `http://<host>/console/graph/#[q=][&view=][&node=][&to=][&token=<tok>]`, with only
// the directives that are set included and every value percent-encoded so the page's
// decodeURIComponent-based hash parser reads it back exactly. The token is emitted
// LAST, after the content directives, matching the daemon-origin grammar.
//
// The clean /console/graph/ PATH is the canonical surface URL: the daemon serves the
// console shell for it (SPA fallback) and the console's boot router opens the graph
// surface from the path. The ORIGIN names the daemon: it serves both the shell and
// the graph data over its own loopback.
func GraphLink(opts GraphLinkOpts) (string, error) {
	if opts.Host == "" {
		return "", ErrNoDaemon
	}

	// Content directives first, in a fixed order for stable output; console.Link appends
	// the token last and applies the one shared escaping policy. There is no #live= host
	// directive: the ORIGIN already names which daemon serves both the page and its data.
	var frag []console.FragmentParam
	if opts.Query != "" {
		frag = append(frag, console.FragmentParam{Key: "q", Value: opts.Query})
	}
	if opts.View != "" {
		frag = append(frag, console.FragmentParam{Key: "view", Value: opts.View})
	}
	if opts.Node != "" {
		frag = append(frag, console.FragmentParam{Key: "node", Value: opts.Node})
	}
	if opts.To != "" {
		frag = append(frag, console.FragmentParam{Key: "to", Value: opts.To})
	}
	return console.Link(console.LinkOpts{
		Host:     opts.Host,
		Surface:  "graph",
		Token:    opts.Token,
		Fragment: frag,
	}), nil
}
