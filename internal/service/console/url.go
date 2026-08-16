package console

import (
	"net/url"
	"slices"
	"strings"

	wire "github.com/egladman/magus/internal/handler/viewer"
	"github.com/egladman/magus/internal/journal"
)

// KnownSurfaces is the CANONICAL list of the console's deep-linkable surfaces, by their clean
// URL path segment. It is the single source of truth shared by the two producers of the clean
// /console/<surface>/ grammar: Link/GraphLink mint these segments, and the daemon's static
// handler serves the console shell for a bare /console/<surface>/ request (SPA fallback) so the
// console's own boot router can open that surface from the path. The decoupled console is a
// single shell page; these clean paths are its public surface URLs (there is no ?app= query form
// in canonical links). Keep it in step with the console's own surface registry.
var KnownSurfaces = []string{"logs", "dashboard", "graph", "activity", "notes", "diff", "plan"}

// IsSurfaceRoute reports whether seg is exactly one known surface segment (no sub-path), i.e. a
// bare /console/<surface>/ route the daemon must serve the shell for rather than a static file.
func IsSurfaceRoute(seg string) bool {
	return slices.Contains(KnownSurfaces, seg)
}

// CanonicalSurfacePath returns the canonical trailing-slash URL for a surface segment, built
// from the ENTRY IN KnownSurfaces rather than from the caller's string.
//
// The distinction is the point. A redirect assembled from a request path is a redirect whose
// destination the requester influenced, which is both a real hazard class and one a taint
// analyser is right to flag (gosec G710). Returning the matched constant means the destination
// is drawn from this file's own list and can be nothing else, so the property holds by
// construction rather than by the caller having validated first.
func CanonicalSurfacePath(seg string) (string, bool) {
	for _, s := range KnownSurfaces {
		if s == seg {
			return "/console/" + s + "/", true
		}
	}
	return "", false
}

// LogViewerURL assembles the log-viewer deep link: BOTH the ref identity and the encoded
// output ride the URL fragment (after #), which the browser NEVER transmits to a server - so
// nothing about the run, not even its ref id, ever leaves the machine. The payload is a
// magus.viewer.v1 Journal (protobuf, gzip+base64url) of the ref's events; the browser decodes
// it and renders pretty from structure (the generated JS client, bundled in).
// keyDigests carries the run's cache-key identity as a `key` directive holding
// "<class>:<digest>,..." (see KeyDigestsParam). It is what lets the viewer answer
// "why did CI get a different ref than me" without either machine seeing the other's
// tree: the page compares the digests in the link against the ones the local daemon
// computes and reports WHICH CLASS differs. Digests are short and per-class, so this
// costs a few dozen bytes of a fragment budget measured in tens of kilobytes. Empty
// keyDigests omits the directive entirely.
func LogViewerURL(base, ref string, events []journal.Event, inv journal.Invocation, keyDigests string) (string, error) {
	j := journal.InvocationFromEvents(ref, events)
	// A single ref's display events are output+result only (no `started`), so
	// InvocationFromEvents yields no command lineage; graft the resolved run's Command so the
	// viewer's lineage header shows which command (and trigger) produced this output.
	if inv.ID != "" {
		j.Command = inv.Command
	}
	encoded, err := wire.EncodeJournalFragment(j, events)
	if err != nil {
		return "", err
	}
	u := strings.TrimRight(base, "/") + "/#ref=" + url.QueryEscape(ref)
	if keyDigests != "" {
		u += "&key=" + encodeComponent(keyDigests)
	}
	return u + "&data=" + encoded, nil
}

// KeyClassDigest is one cache-key component class and the digest of its key inputs.
// A pair type rather than two parallel slices, so a caller cannot hand the renderer
// mismatched lists.
type KeyClassDigest struct {
	Class  string
	Digest string
}

// KeyDigestsParam renders per-class key digests as one compact fragment value:
// "class:digest,class:digest", in the classes' key order. The rendering is
// deliberately trivial to parse in the browser (split on "," then ":") and carries no
// key CONTENT - a digest names that a class differs, never what is in it.
func KeyDigestsParam(digests []KeyClassDigest) string {
	parts := make([]string, 0, len(digests))
	for _, d := range digests {
		parts = append(parts, d.Class+":"+d.Digest)
	}
	return strings.Join(parts, ",")
}

// LinkOpts is the input to Link: the single home for the daemon-origin console URL grammar.
// Host is the daemon's loopback host:port (from mcp.address); Surface is a console surface
// segment (see KnownSurfaces: "dashboard", "graph", ...); Token is an optional bearer token;
// Fragment holds any extra content directives that ride the fragment ahead of the token.
type LinkOpts struct {
	Host    string
	Surface string
	Token   string
	// Fragment is the ordered list of extra content directives (e.g. {"flavor","targets"} or
	// {"view","blast"}) appended to the fragment BEFORE the token. Order is preserved so callers
	// get stable, testable output; each value is percent-encoded with the one escaping policy.
	Fragment []FragmentParam
}

// FragmentParam is one fragment directive, a key and its raw (unencoded) value.
type FragmentParam struct {
	Key   string
	Value string
}

// Link assembles a console surface's daemon-origin deep link:
// http://<host>/console/<surface>/#[<directives>&]token=<token>. Under the daemon-origin grammar
// the ORIGIN names which daemon - the daemon serves both the console shell (over its loopback
// /console/) and the data API - so nothing but content state and the bearer token rides the
// fragment; there is no #live= host directive. The clean /console/<surface>/ PATH is the canonical
// surface URL: the daemon serves the shell for it (SPA fallback) and the console's boot router
// opens that surface from the path. The token rides the fragment (never transmitted on the
// document GET) and is emitted LAST, after any content directives, so callers hold a secret: a
// link with a token must only be surfaced to an interactive user, never written to a log.
//
// This is the ONE home for the grammar: url.GraphLink composes it rather than hand-building
// the "http://"+host+"/console/graph/"+frag string, so both producers share a single escaping
// policy (encodeComponent, the encodeURIComponent equivalent the page's hash parser reverses).
func Link(opts LinkOpts) string {
	var parts []string
	for _, p := range opts.Fragment {
		parts = append(parts, p.Key+"="+encodeComponent(p.Value))
	}
	if opts.Token != "" {
		parts = append(parts, "token="+encodeComponent(opts.Token))
	}
	frag := ""
	if len(parts) > 0 {
		frag = "#" + strings.Join(parts, "&")
	}
	return "http://" + opts.Host + "/console/" + opts.Surface + "/" + frag
}

// encodeComponent percent-encodes s the way the browser's encodeURIComponent does, which is what
// the page's hash parser reverses with decodeURIComponent. url.QueryEscape is
// application/x-www-form-urlencoded, which encodes a space as "+"; decodeURIComponent would leave
// that "+" literal, so the "+"->"%20" fixup is required. A literal "+" in s is already emitted as
// "%2B" by QueryEscape, so the fixup never corrupts it. It is the single escaping policy shared by
// every producer of the console URL grammar (Link here, and url.GraphLink through it).
func encodeComponent(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
