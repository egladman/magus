package console

import "errors"

// GraphLinkOpts are the Graph Explorer directives a link pre-applies. Each is
// optional and only the ones set reach the fragment.
type GraphLinkOpts struct {
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

// GraphLink formats http://<host>/console/graph/#<directives>. An empty Host is
// an error: there is no origin to link to. Link owns the escaping and
// puts the code last.
func GraphLink(opts GraphLinkOpts) (string, error) {
	if opts.Host == "" {
		return "", errors.New("graph link: no server host, omit the link")
	}

	var frag []FragmentParam
	for _, p := range []FragmentParam{
		{Key: "q", Value: opts.Query},
		{Key: "view", Value: opts.View},
		{Key: "node", Value: opts.Node},
		{Key: "to", Value: opts.To},
	} {
		if p.Value != "" {
			frag = append(frag, p)
		}
	}

	return Link(LinkOpts{
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

// AuthHint is the second line every call site prints under the link. The link is
// deliberately UNAUTHENTICATED and every console route needs a token, so this is the
// command a person runs to open it signed in.
func AuthHint(link string) string {
	return "open it signed in: " + OpenCommand(link)
}

// BuildGraphLink fills Host/Code on the caller's directives and formats the URL,
// returning "" only when GraphLink has no host to link to. It is split out with the
// inputs injected so tests can assert the URL without a server.
func BuildGraphLink(host, code string, directives GraphLinkOpts) string {
	directives.Host = host
	directives.Code = code
	link, err := GraphLink(directives)
	if err != nil {
		return ""
	}
	return link
}
