package main

import (
	"github.com/egladman/magus/internal/graph/url"
)

// graph_link.go holds the shared seam for the "view this in the Graph Explorer"
// deep-links a few read-only commands print beneath their output (magus affected
// --impact, magus explain). The link is always emitted; the daemon may not be up
// when the browser opens it, so the call sites follow it with a start-the-daemon
// hint rather than probing and omitting the line.

// tokenHint is the second line every call site prints under the link. The link is
// deliberately UNAUTHENTICATED, so this names the one command that completes it.
const tokenHint = "(append &token=$(magus config mcp token print) to open it authenticated)"

// liveExplorerLink formats a daemon-origin Graph Explorer deep-link (served by the
// running daemon from http://<host>/console/graph/) with the caller's directives
// applied. It does not probe the daemon, and it NEVER embeds the bearer token.
//
// It used to call auth.Load() and put the live token in the fragment. The fragment
// is not transmitted in an HTTP request, which is why that read as safe - but it is
// still a credential written to stdout on every `magus explain`, and stdout is
// scrollback, a captured run log, and the context of whatever agent ran the command.
// A credential that reaches three sinks nobody audits is leaked regardless of what
// the browser does with it afterwards.
//
// The token stays one shell substitution away (see tokenHint), which keeps the
// authenticated URL composable while making its disclosure an explicit act rather
// than a side effect of asking an unrelated question. Distinct from
// graphExplorerLink in graph_source.go, which builds a static #src= link.
func liveExplorerLink(directives url.GraphLinkOpts) string {
	return buildGraphLink(mcpAddrString(), "", directives)
}

// buildGraphLink fills Host/Token on the caller's directives and formats the URL,
// returning "" only when GraphLink has no host to link to. It is split out with the
// inputs injected so tests can assert the URL without a daemon.
func buildGraphLink(host, token string, directives url.GraphLinkOpts) string {
	directives.Host = host
	directives.Token = token
	link, err := url.GraphLink(directives)
	if err != nil {
		return ""
	}
	return link
}
