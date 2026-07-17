package main

import (
	"strings"

	"github.com/egladman/magus/internal/auth"
	graphurl "github.com/egladman/magus/internal/graph/url"
)

// graph_link.go holds the shared seam for the "view this in the Graph Explorer"
// deep-links a few read-only commands print beneath their output (magus affected
// --impact, magus explain). The link is always emitted; the daemon may not be up
// when the browser opens it, so the call sites follow it with a start-the-daemon
// hint rather than probing and omitting the line.

// graphExplorerBase returns the hosted Graph Explorer base for deep-links: the
// configured console URL (cfg.Console.URL) with "graph/" appended, matching how
// `magus graph open` derives its explorer origin. An unset console URL yields ""
// so GraphLink falls back to graphurl.DefaultExplorerBase, the same value.
func graphExplorerBase() string {
	base := strings.TrimSpace(globalCfg.Console.URL)
	if base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/graph/"
}

// liveExplorerLink formats a live Graph Explorer deep-link (a #live= link into the
// running daemon) with the caller's directives applied. It does not probe the
// daemon: the URL is built unconditionally from the daemon address, the configured
// explorer base, and a best-effort token (a token-load failure just drops the
// token, leaving the link usable). Distinct from graphExplorerLink in
// graph_source.go, which builds a static #src= link for MAGUS.md.
func liveExplorerLink(directives graphurl.GraphLinkOpts) string {
	token, _ := auth.Load() // best-effort: an empty token still yields an openable link
	return buildGraphLink(mcpAddrString(), token, graphExplorerBase(), directives)
}

// buildGraphLink fills Host/Token/ExplorerBase on the caller's directives and
// formats the URL, returning "" only when GraphLink has no host to link to. It is
// split out with the inputs injected so tests can assert the URL without a daemon.
func buildGraphLink(host, token, base string, directives graphurl.GraphLinkOpts) string {
	directives.Host = host
	directives.Token = token
	directives.ExplorerBase = base
	link, err := graphurl.GraphLink(directives)
	if err != nil {
		return ""
	}
	return link
}
