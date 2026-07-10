package httpx

import (
	"fmt"
	"net/http"
	"net/url"
)

// ParseOrigin extracts the scheme://host[:port] origin from a page's base URL, for the
// loopback server's CORS Allow-Origin. An unparseable or non-absolute base is a user
// error worth surfacing rather than defaulting to a permissive value.
func ParseOrigin(base string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("httpx: origin: %q is not a valid absolute URL", base)
	}
	return u.Scheme + "://" + u.Host, nil
}

// CORS locks every wrapped response to a single site origin and answers the OPTIONS
// preflight here, so a route handler never touches CORS itself. The preflight advertises
// the GET, OPTIONS methods the loopback tool-page routes use and allows the Authorization
// header, since the live viewer's fetch-based SSE client sends the per-run bearer token as
// a non-simple Authorization header that requires a preflight allowance.
func CORS(origin string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
			if r.Method == http.MethodOptions {
				w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization")
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
