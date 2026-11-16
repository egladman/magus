package main

import "github.com/egladman/magus/internal/service/console"

// graphLinkOpts, authHint and liveExplorerLink stay named here for callers in files this
// move does not own (affected.go, diff.go, query.go); they delegate to the console.
type graphLinkOpts = console.GraphLinkOpts

func authHint(link string) string { return console.AuthHint(link) }

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
	return console.BuildGraphLink(mcpAddrString(), "", directives)
}
