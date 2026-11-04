package main

import (
	"errors"

	"github.com/egladman/magus/internal/service/console"
)

// graphLinkOpts are the Graph Explorer directives a link pre-applies. Each is
// optional and only the ones set reach the fragment.
type graphLinkOpts struct {
	// Host is the server's loopback host:port, the link's origin.
	Host string
	// Code is a one-time exchange code the page trades for its token.
	Code string
	// Query runs a node search on load.
	Query string
	// View activates a named view; it wins over Query on the page.
	View string
	// Node is the focus node of a blast or trace view.
	Node string
	// To is the destination node of a trace view.
	To string
}

// graphLink formats http://<host>/console/graph/#<directives>. An empty Host is
// an error: there is no origin to link to. console.Link owns the escaping and
// puts the code last.
func graphLink(opts graphLinkOpts) (string, error) {
	if opts.Host == "" {
		return "", errors.New("graph link: no server host, omit the link")
	}

	var frag []console.FragmentParam
	for _, p := range []console.FragmentParam{
		{Key: "q", Value: opts.Query},
		{Key: "view", Value: opts.View},
		{Key: "node", Value: opts.Node},
		{Key: "to", Value: opts.To},
	} {
		if p.Value != "" {
			frag = append(frag, p)
		}
	}

	return console.Link(console.LinkOpts{
		Host:     opts.Host,
		App:      "graph",
		Code:     opts.Code,
		Fragment: frag,
	}), nil
}

// graph_link.go holds the shared seam for the "view this in the Graph Explorer"
// deep-links a few read-only commands print beneath their output (magus affected
// --impact, magus explain). The link is always emitted; the server may not be up
// when the browser opens it, so the call sites follow it with a start-the-server
// hint rather than probing and omitting the line.

// authHint is the second line every call site prints under the link. The link is
// deliberately UNAUTHENTICATED and every console route needs a token, so this is the
// command a person runs to open it signed in.
func authHint(link string) string {
	return "open it signed in: " + console.OpenCommand(link)
}

// liveExplorerLink formats a server-origin Graph Explorer deep-link (served by the
// running server from http://<host>/console/graph/) with the caller's directives
// applied. It does not probe the server, and it NEVER embeds the bearer token.
//
// It used to call auth.Load() and put the live token in the fragment. The fragment
// is not transmitted in an HTTP request, which is why that read as safe, but it is
// still a credential written to stdout on every `magus explain`, and stdout is
// scrollback, a captured run log, and the context of whatever agent ran the command.
// A credential that reaches three sinks nobody audits is leaked regardless of what
// the browser does with it afterwards.
//
// The token stays one shell substitution away (see authHint), which keeps the
// authenticated URL composable while making its disclosure an explicit act rather
// than a side effect of asking an unrelated question. Distinct from
// graphExplorerLink in graph_source.go, which builds a static #src= link.
func liveExplorerLink(directives graphLinkOpts) string {
	return buildGraphLink(mcpAddrString(), "", directives)
}

// buildGraphLink fills Host/Code on the caller's directives and formats the URL,
// returning "" only when GraphLink has no host to link to. It is split out with the
// inputs injected so tests can assert the URL without a server.
func buildGraphLink(host, code string, directives graphLinkOpts) string {
	directives.Host = host
	directives.Code = code
	link, err := graphLink(directives)
	if err != nil {
		return ""
	}
	return link
}
