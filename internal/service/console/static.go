package console

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// consoleCSP is the Content-Security-Policy served with every console HTML document, on BOTH
// listeners that serve the app (the daemon's loopback /console/ mount and the on-demand LAN
// "share to phone" listener). It is deliberately strict: the console ORIGIN holds the operator
// token (in the URL fragment, then in memory) and reaches TokenService (list + revoke), and it
// renders attacker-influenced output - build/test logs, graph labels, activity text - so a single
// injected script would have a large blast radius. Locking script-src to 'self' (no inline, no
// eval, no CDN - the built console loads only its own bundles) is the primary XSS control;
// connect-src 'self' keeps any exfiltration channel on the same origin, and form-action 'self'
// closes the form-POST exfil channel connect-src does not cover. object-src 'none' bans plugin
// embeds; frame-ancestors 'none' bans clickjacking frames (the console is a standalone app, never
// embedded); base-uri 'self' blocks an injected <base> from repointing the relative asset loads to
// a foreign origin while still allowing serveConsoleShell's same-origin <base href="../">. Neither
// object-src, frame-ancestors, base-uri, nor form-action inherits from default-src, so each is set
// explicitly. style-src keeps 'unsafe-inline' because PatternFly and the shell set element styles
// inline; img-src allows data: for the inline SVG/data-URI icons the bundle embeds.
const consoleCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'; form-action 'self'"

// StaticHandler serves the built console at /console/ for a consoleDir, with an SPA fallback for
// the clean surface paths and a strict CSP on every HTML document. It is the ONE implementation
// shared by the daemon's loopback mount and the LAN share listener, so a phone reload of
// /console/<surface>/ gets the same shell fallback the desktop does (rather than a 404) and both
// origins carry the same CSP.
//
// The decoupled console is a single shell page that reads its surface from the URL PATH, so a
// bare /console/<surface>/ request (one of KnownSurfaces) must return the shell - not the static
// directory listing that physically lives there - so the console's boot router can open that
// surface. Every real file (the /console/ root index, the bundles, css, assets, and each
// surface's sub-path files) serves normally through the FileServer.
func StaticHandler(consoleDir string) http.Handler {
	fileServer := http.StripPrefix("/console/", http.FileServer(http.Dir(consoleDir)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// cspHTMLWriter stamps the CSP onto HTML responses only, leaving asset (js/css/image)
		// responses untouched; routing both the shell fallback and the FileServer through it means
		// the root index.html and any surface stub get the header without a per-branch set.
		cw := &cspHTMLWriter{ResponseWriter: w}
		// seg is the single path element under /console/ ("graph"), or "" for the root, or a
		// multi-element sub-path ("graph/explorer.js") - only a bare known surface is a route.
		seg := strings.Trim(strings.TrimPrefix(r.URL.Path, "/console/"), "/")
		if IsSurfaceRoute(seg) {
			// Canonicalize to the trailing-slash form BEFORE serving, because the shell is
			// served with <base href="../"> and that only lands on /console/ when the URL
			// already ends in a slash. Without the redirect, /console/diff resolves every asset
			// one level too high - console.css, theme.js, patternfly.css all 404 at the site
			// root, and the surface renders unstyled and never boots. The trim above hides the
			// difference from IsSurfaceRoute, so the check has to happen on the raw path.
			//
			// KnownSurfaces documents the canonical grammar as /console/<surface>/ and Link
			// mints it that way, so this only affects a URL a person typed - which is exactly
			// the case worth being kind about. Redirecting rather than making the base absolute
			// keeps the shell servable from a prefix it does not know, which is what the
			// relative base is for. StatusFound, matching share.go's redirect to /console/.
			if !strings.HasSuffix(r.URL.Path, "/") {
				// The destination comes from KnownSurfaces itself, not from the request - see
				// CanonicalSurfacePath. It also normalizes an odd but legal /console//diff.
				target, ok := CanonicalSurfacePath(seg)
				if !ok {
					fileServer.ServeHTTP(cw, r)
					return
				}
				if q := r.URL.RawQuery; q != "" {
					target += "?" + q
				}
				// The fragment carries the daemon host and token and is never sent to a server,
				// so there is nothing to preserve here; the browser reattaches it itself.
				//
				//nolint:gosec // G710: the destination is an element of KnownSurfaces, returned by
				// CanonicalSurfacePath, so it cannot be influenced by the request - only the
				// optional query rides along. gosec's taint analysis cannot see through the
				// allow-list lookup and flags any redirect downstream of a request path.
				// TestRedirectNormalizesAndCannotEchoTheRequestPath pins the property.
				http.Redirect(w, r, target, http.StatusFound)
				return
			}
			serveConsoleShell(cw, r, consoleDir)
			return
		}
		fileServer.ServeHTTP(cw, r)
	})
}

// serveConsoleShell writes the console shell (index.html) for a clean /console/<surface>/ route,
// with a <base href="../"> injected as the first <head> child. The injection is REQUIRED: the
// shell loads its assets by RELATIVE path (./console.js, ./patternfly.css) so a single built
// index.html works at both the hosted origin and this daemon; served one level deep at
// /console/<surface>/, those refs must resolve against the parent /console/, which the base
// makes so. (The shell's own lazy imports resolve against import.meta.url, i.e. console.js's
// URL, so they are unaffected.) The hosted static host - which has no such fallback - gets the
// same effect from the per-surface index.html stubs the console build emits into gen/<surface>/.
func serveConsoleShell(w http.ResponseWriter, r *http.Request, consoleDir string) {
	raw, err := os.ReadFile(filepath.Join(consoleDir, "index.html"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	patched := bytes.Replace(raw, []byte("<head>"), []byte("<head>\n  <base href=\"../\">"), 1)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(patched)
}

// cspHTMLWriter sets consoleCSP on responses whose Content-Type is HTML, leaving asset responses
// (js/css/images) alone. The header must land before the status line, so it hooks WriteHeader and
// the implicit first Write; the Content-Type has already been set by the FileServer (via
// ServeContent) or the shell by then, so the sniff reads the resolved type.
type cspHTMLWriter struct {
	http.ResponseWriter
	stamped bool
}

func (w *cspHTMLWriter) stamp() {
	if w.stamped {
		return
	}
	w.stamped = true
	if strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
		w.Header().Set("Content-Security-Policy", consoleCSP)
	}
}

func (w *cspHTMLWriter) WriteHeader(code int) {
	w.stamp()
	w.ResponseWriter.WriteHeader(code)
}

func (w *cspHTMLWriter) Write(b []byte) (int, error) {
	w.stamp()
	return w.ResponseWriter.Write(b)
}
