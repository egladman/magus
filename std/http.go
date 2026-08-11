//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/retry"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module http -lang buzz -out ../internal/interp/bindings/gen/http.go

func init() { Register(HTTP) }

// optsDoc is the trailing line shared by every method's Doc describing the
// optional curl-style options map (last argument).
const optsDoc = " opts (curl-style): fail, fail_with_body, fail_early (bool); " +
	"timeout (seconds, default 30). Retrying is NOT configured here - pass a typed " +
	"HttpRetry as the retry argument; without one the request runs exactly once."

// defaultHTTPTimeout bounds a request (including any retries and backoff) so a
// server that accepts the connection then stalls cannot hang a build forever.
// Overridable per call via the curl-style "timeout" opt (curl --max-time).
const defaultHTTPTimeout = 30 * time.Second

// HTTP is the "http" host module: an HTTP client with automatic retry on
// transient errors and curl-style per-request control over retry and failure.
//
// Security note: outbound requests are audited but NOT blocked when the sandbox
// is active. There is no SSRF guard: any URL a magusfile passes is fetched,
// including localhost, internal services, and the cloud metadata endpoint. Only
// pass URLs you trust.
var HTTP = Module{
	Name: "http",
	Doc:  "HTTP client. Requests run ONCE unless given a retry policy.",
	Methods: []Method{
		{
			Name: "get",
			Doc:  "Send a GET request; returns {status, body, headers}." + optsDoc,
			Args: []Arg{
				{Name: "url", Type: TypeString},
				{Name: "headers", Type: TypeStringMap, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
				{Name: "retry", Type: TypeAnyMap, Optional: true, Object: "HttpRetry"},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "HttpResponse"}},
			Impl:    HTTPGet,
		},
		{
			Name: "download",
			Doc:  "GET url and stream the response body straight to dest, returning the HTTP status. The body never becomes a Buzz string, so arbitrary binary (a release tarball, an image layer) survives intact and a large file costs no proportional memory. A non-2xx status writes nothing. Pair it with crypto.sha256_file to verify what you fetched before using it." + optsDoc,
			Args: []Arg{
				{Name: "url", Type: TypeString},
				{Name: "dest", Type: TypeString},
				{Name: "headers", Type: TypeStringMap, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
				{Name: "retry", Type: TypeAnyMap, Optional: true, Object: "HttpRetry"},
			},
			Returns: []Ret{{Type: TypeInt}},
			Impl:    HTTPDownload,
		},
		{
			Name: "post",
			Doc:  "Send a POST request with body; returns {status, body, headers}." + optsDoc,
			Args: []Arg{
				{Name: "url", Type: TypeString},
				{Name: "body", Type: TypeString},
				{Name: "headers", Type: TypeStringMap, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
				{Name: "retry", Type: TypeAnyMap, Optional: true, Object: "HttpRetry"},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "HttpResponse"}},
			Impl:    HTTPPost,
		},
		{
			Name: "request",
			Doc:  "Send an HTTP request; returns {status, body, headers}." + optsDoc,
			Args: []Arg{
				{Name: "method", Type: TypeString},
				{Name: "url", Type: TypeString},
				{Name: "body", Type: TypeString, Optional: true},
				{Name: "headers", Type: TypeStringMap, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
				{Name: "retry", Type: TypeAnyMap, Optional: true, Object: "HttpRetry"},
			},
			Returns: []Ret{{Type: TypeAnyMap, Object: "HttpResponse"}},
			Impl:    HTTPRequest,
		},
		{
			Name: "server",
			Doc: "Start a static file server in the background from an options map and return the bound port. " +
				"opts keys: dir (string) serves a single directory; OR mounts (a map of URL-prefix -> dir, e.g. " +
				"{\"/\": \"docs/gen\", \"/console/\": \"console/gen\"}) serves multiple roots where a request routes to " +
				"the LONGEST matching prefix, so \"/console/\" wins over \"/\" for a /console/ path and the matched prefix " +
				"is stripped before the file lookup. Exactly one of dir or mounts is required. port (int, optional) binds " +
				"that port; 0 (the default) scans upward from 8080 and binds the first available one. Unknown keys are " +
				"rejected. Serves localhost only and runs until the process exits, so pair it with a blocking call like fs.watch.",
			Args: []Arg{
				{Name: "opts", Type: TypeAnyMap},
			},
			Returns: []Ret{{Type: TypeInt}},
			Impl:    HTTPServe,
		},
	},
}

// defaultHTTPClient runs a request EXACTLY ONCE, bounded by the default timeout.
//
// It used to retry three times for every caller that never asked. That is the
// wrong default for a build tool in both directions: a genuinely failing request
// costs three round trips and their backoff before the failure is reported, and a
// real outage reads as a slow build rather than a broken one. Retrying is now
// something a caller states with an HttpRetry policy.
var defaultHTTPClient = retry.NewHTTPClient(
	nil,
	retry.WithAttempts(1),
	retry.WithTimeout(defaultHTTPTimeout),
)

// HTTPGet sends a GET request to url with optional headers and curl-style opts,
// returning {status, body, headers}.
func HTTPGet(ctx context.Context, url string, headers map[string]string, opts, retryPolicy map[string]any) (types.HTTPResponse, error) {
	return doRequest(ctx, http.MethodGet, url, "", headers, opts, retryPolicy)
}

// HTTPDownload GETs url and streams the body to dest, returning the HTTP status.
//
// It lives here rather than beside the other byte-level helpers in
// internal/interp/bindings so it is a declared method like every other: visible in
// `magus describe modules`, in the knowledge graph and the docs, and TYPED in the
// checker. As an undeclared companion it was reachable but invisible, and its
// signature was Unknown to every caller.
//
// Moving it also gained it two things the companion version never had: the
// retrying client the rest of this module uses, and a sandbox write check on dest.
func HTTPDownload(ctx context.Context, url, dest string, headers map[string]string, opts, retryPolicy map[string]any) (int, error) {
	if types.Tracing(ctx) {
		return 0, nil
	}
	dest = resolvePath(ctx, dest)
	if err := checkWrite(ctx, dest); err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("http.download: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	o := parseHTTPOpts(opts)
	r, err := parseHTTPRetry(retryPolicy)
	if err != nil {
		return 0, err
	}
	client := defaultHTTPClient
	if o.custom() || r.retries() {
		client = o.client(r)
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("http.download %s: %w", url, err)
	}
	defer resp.Body.Close()

	// A non-2xx writes NOTHING. Creating the file first and truncating it on a 404
	// would leave an empty artifact that a later cache check reads as a hit.
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, nil
	}

	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return resp.StatusCode, fmt.Errorf("http.download %s: %w", dest, err)
	}
	f, err := os.Create(dest)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("http.download %s: %w", dest, err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		// A truncated download is not a usable file, and leaving it behind invites
		// something downstream to treat a partial tarball as complete.
		_ = os.Remove(dest)
		return resp.StatusCode, fmt.Errorf("http.download %s: %w", dest, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(dest)
		return resp.StatusCode, fmt.Errorf("http.download %s: %w", dest, err)
	}
	return resp.StatusCode, nil
}

// HTTPPost sends a POST of body to url with optional headers and curl-style opts,
// returning {status, body, headers}.
func HTTPPost(ctx context.Context, url, body string, headers map[string]string, opts, retryPolicy map[string]any) (types.HTTPResponse, error) {
	return doRequest(ctx, http.MethodPost, url, body, headers, opts, retryPolicy)
}

// HTTPRequest sends a request with the given method to url and returns
// {status, body, headers}.
func HTTPRequest(ctx context.Context, method, url, body string, headers map[string]string, opts, retryPolicy map[string]any) (types.HTTPResponse, error) {
	return doRequest(ctx, method, url, body, headers, opts, retryPolicy)
}

// HTTPServe starts a static file server on localhost in a background goroutine
// from a validated options bag and returns the bound TCP port. opts is a
// curl-style map that carries either a single "dir" to serve one directory or a
// "mounts" prefix->dir map to serve several roots by URL prefix (longest prefix
// wins), plus an optional "port". The options object is the boundary: an unknown
// key, a missing or ambiguous dir/mounts choice, or a mistyped value fails loudly
// here rather than silently defaulting. The server runs until the process exits,
// so callers pair it with a blocking call such as fs.watch to keep serving.
func HTTPServe(ctx context.Context, opts map[string]any) (int, error) {
	dir, mounts, port, err := parseServerOpts(opts)
	if err != nil {
		return 0, err
	}
	// Under dry-run tracing a target body is evaluated to learn its declarations,
	// not to run, and binding a real socket here would leak a listener out of a
	// plan that was never meant to execute. Matches fs.watch and doRequest.
	if types.Tracing(ctx) {
		return 0, nil
	}

	var handler http.Handler
	if mounts != nil {
		handler = mountsHandler(ctx, mounts)
	} else {
		// Resolve dir against the run's working directory (the project dir), the same
		// way fs/io/os do, since the magusfile runner sets a context cwd instead of
		// chdir-ing the process.
		handler = http.FileServer(http.Dir(resolvePath(ctx, dir)))
	}

	ln, err := httpListen(port)
	if err != nil {
		return 0, err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }()
	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		return 0, fmt.Errorf("http.server: unexpected listener address type %T", ln.Addr())
	}
	return tcpAddr.Port, nil
}

// serverKnownOpts is the exact set of keys http.server accepts. It is both the
// allow-list the validator checks against and the "(known: ...)" hint a typo
// error prints, so the two never drift.
var serverKnownOpts = []string{"dir", "mounts", "port"}

// parseServerOpts validates the http.server options bag and returns the chosen
// single-dir root (dir), the prefix->dir mount table (mounts), and the port.
// Exactly one of dir/mounts is non-empty on success. It rejects unknown keys, a
// missing or doubled dir/mounts choice, and mistyped values so a typo fails loudly
// instead of falling through to a default.
func parseServerOpts(opts map[string]any) (dir string, mounts map[string]string, port int, err error) {
	var unknown []string
	for k := range opts {
		switch k {
		case "dir", "mounts", "port":
		default:
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		return "", nil, 0, unknownOptsError(unknown)
	}

	_, hasDir := opts["dir"]
	_, hasMounts := opts["mounts"]
	switch {
	case hasDir && hasMounts:
		return "", nil, 0, fmt.Errorf(`http.server: give either "dir" or "mounts", not both`)
	case !hasDir && !hasMounts:
		return "", nil, 0, fmt.Errorf(`http.server: one of "dir" or "mounts" is required`)
	}

	if hasDir {
		s, ok := opts["dir"].(string)
		if !ok {
			return "", nil, 0, fmt.Errorf(`http.server: "dir" must be a string, got %T`, opts["dir"])
		}
		if s == "" {
			return "", nil, 0, fmt.Errorf(`http.server: "dir" must not be empty`)
		}
		dir = s
	}

	if hasMounts {
		raw, ok := opts["mounts"].(map[string]any)
		if !ok {
			return "", nil, 0, fmt.Errorf(`http.server: "mounts" must be a map of string to string, got %T`, opts["mounts"])
		}
		if len(raw) == 0 {
			return "", nil, 0, fmt.Errorf(`http.server: "mounts" must not be empty`)
		}
		mounts = make(map[string]string, len(raw))
		for prefix, v := range raw {
			s, ok := v.(string)
			if !ok {
				return "", nil, 0, fmt.Errorf(`http.server: mount %q must map to a string dir, got %T`, prefix, v)
			}
			mounts[prefix] = s
		}
	}

	if v, ok := opts["port"]; ok {
		switch n := v.(type) {
		case int:
			port = n
		case int64:
			port = int(n)
		default:
			return "", nil, 0, fmt.Errorf(`http.server: "port" must be an int, got %T`, v)
		}
	}

	return dir, mounts, port, nil
}

// unknownOptsError builds the typo error listing the offending keys (sorted for a
// deterministic message) alongside the known set.
func unknownOptsError(unknown []string) error {
	sort.Strings(unknown)
	quoted := make([]string, len(unknown))
	for i, k := range unknown {
		quoted[i] = strconv.Quote(k)
	}
	label := "option"
	if len(unknown) > 1 {
		label = "options"
	}
	return fmt.Errorf("http.server: unknown %s %s (known: %s)",
		label, strings.Join(quoted, ", "), strings.Join(serverKnownOpts, ", "))
}

// mountsHandler builds the multi-root handler for a prefix->dir map. A request
// routes to the mount with the LONGEST matching prefix, so "/console/" wins over
// "/" for a /console/... path (mirroring a deploy where the console app shadows a
// docs redirect stub at /console/), and the matched prefix is stripped before the
// file lookup. Each dir is resolved against the run's working directory the same
// way the single-dir path resolves its dir.
func mountsHandler(ctx context.Context, mounts map[string]string) http.Handler {
	// A fixed routing table sorted by DESCENDING prefix length: the first prefix
	// that matches a request path is then the longest one, so a more specific mount
	// (/console/) shadows a broader one (/).
	type mount struct {
		prefix  string
		handler http.Handler
	}
	routes := make([]mount, 0, len(mounts))
	for prefix, dir := range mounts {
		fs := http.FileServer(http.Dir(resolvePath(ctx, dir)))
		// Strip the prefix without its trailing slash so "/console/" strips to "/"
		// (StripPrefix leaves an empty path otherwise) and "/" strips to nothing.
		routes = append(routes, mount{
			prefix:  prefix,
			handler: http.StripPrefix(strings.TrimSuffix(prefix, "/"), fs),
		})
	}
	sort.Slice(routes, func(i, j int) bool { return len(routes[i].prefix) > len(routes[j].prefix) })

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, rt := range routes {
			if pathHasPrefix(r.URL.Path, rt.prefix) {
				rt.handler.ServeHTTP(w, r)
				return
			}
		}
		http.NotFound(w, r)
	})
}

// pathHasPrefix reports whether a request path falls under a mount prefix, treating
// the prefix as a URL subtree: "/" matches everything, and "/console/" matches the
// bare "/console" as well as anything under "/console/". Comparing against the
// trailing-slash-trimmed prefix keeps "/console" from spuriously matching a sibling
// like "/console-x".
func pathHasPrefix(path, prefix string) bool {
	if prefix == "" || prefix == "/" {
		return true
	}
	bare := strings.TrimSuffix(prefix, "/")
	return path == bare || strings.HasPrefix(path, bare+"/")
}

// httpServerBasePort is where http.server starts scanning when the caller does
// not request a specific port. 8080 is the conventional local web port.
const httpServerBasePort = 8080

// httpListen binds a localhost TCP listener. A specific (non-zero) port binds
// exactly and errors if it is taken; port 0 scans upward from httpServerBasePort
// and returns the first bindable port, so an unspecified port never collides with
// a server already running.
func httpListen(port int) (net.Listener, error) {
	if port != 0 {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return nil, fmt.Errorf("http.server: listen on port %d: %w", port, err)
		}
		return ln, nil
	}
	for p := httpServerBasePort; p < httpServerBasePort+100; p++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p))
		if err == nil {
			return ln, nil
		}
	}
	return nil, fmt.Errorf("http.server: no bindable port in %d-%d", httpServerBasePort, httpServerBasePort+99)
}

func doRequest(ctx context.Context, method, url, body string, headers map[string]string, opts, retryPolicy map[string]any) (types.HTTPResponse, error) {
	if types.Tracing(ctx) {
		return types.HTTPResponse{}, nil
	}
	// There is NO network sandboxing: every URL a magusfile reaches is fetched,
	// including localhost, RFC1918 ranges, and the cloud metadata endpoint
	// (169.254.169.254). Treat any URL a magusfile can reach as trusted.
	//
	// An audit log used to sit here. It was removed because it claimed a coverage it
	// never had: it saw only this binding, so it missed magus's own traffic (self
	// update, remote cache) and every subprocess. A record of one narrow slice, framed
	// as network auditing, is worse than an honest absence.

	o := parseHTTPOpts(opts)
	r, retryErr := parseHTTPRetry(retryPolicy)
	if retryErr != nil {
		return types.HTTPResponse{}, retryErr
	}

	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return types.HTTPResponse{}, fmt.Errorf("http.%s: %w", strings.ToLower(method), err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	client := defaultHTTPClient
	if o.custom() || r.retries() {
		client = o.client(r)
	}
	resp, err := client.Do(req)
	if err != nil {
		return types.HTTPResponse{}, fmt.Errorf("http.%s %s: %w", strings.ToLower(method), url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return types.HTTPResponse{}, fmt.Errorf("http.%s %s: read body: %w", strings.ToLower(method), url, err)
	}

	// curl --fail / --fail-with-body: a >= 400 status becomes an error rather
	// than a result. --fail-with-body keeps the body in the error message;
	// --fail omits it.
	if (o.fail || o.failWithBody) && resp.StatusCode >= 400 {
		e := &StatusError{Method: strings.ToLower(method), URL: url, Status: resp.StatusCode}
		if o.failWithBody {
			e.Body = string(raw)
		}
		return types.HTTPResponse{}, e
	}

	// Collect response headers; where a name has multiple values, take the first.
	respHeaders := make(map[string]string, len(resp.Header))
	for k, vs := range resp.Header {
		if len(vs) > 0 {
			respHeaders[k] = vs[0]
		}
	}
	return types.HTTPResponse{
		Status:  resp.StatusCode,
		Body:    string(raw),
		Headers: respHeaders,
	}, nil
}

// httpOpts is the parsed curl-style options map. Zero value means "use the
// default client" (see custom).
type httpOpts struct {
	fail         bool // --fail: >= 400 status becomes an error (no body)
	failWithBody bool // --fail-with-body: >= 400 status becomes an error, body kept
	failEarly    bool // --fail-early: never retry an HTTP error status, fail at once

	timeout time.Duration // --max-time: overall request timeout; 0 = use default
}

// custom reports whether this call needs a client of its own rather than the
// shared default. fail and fail_with_body alone do not; they are applied after the
// response comes back.
func (o httpOpts) custom() bool { return o.failEarly || o.timeout > 0 }

// httpRetry is the parsed HttpRetry policy: the caller's answer to "how hard
// should this try", separate from httpOpts, which is everything else.
type httpRetry struct {
	attempts    int
	delay       time.Duration
	maxDelay    time.Duration
	maxElapsed  time.Duration
	fixed       bool
	allErrs     bool
	connRefused bool
}

// retries reports whether this policy asks for more than one attempt. A zero
// policy - an omitted argument - does not, which is what makes "runs once" the
// default without a separate flag saying so.
func (r httpRetry) retries() bool { return r.attempts > 1 }

// httpRetryKeys is every field HttpRetry declares. Kept beside the parser so an
// added field cannot be silently unreadable.
var httpRetryKeys = map[string]bool{
	"attempts": true, "delay_ms": true, "max_delay_ms": true,
	"max_elapsed_ms": true, "fixed": true, "all_errors": true, "conn_refused": true,
}

// parseHTTPRetry reads the HttpRetry object a caller passed. An absent map is the
// zero policy: one attempt.
//
// AN UNKNOWN KEY IS AN ERROR, matching what http.server already does with its own
// options. It matters more here than anywhere else in this module: a policy with
// a mistyped key name (say, "attempts" spelled wrong) does not fail, it just
// never retries, and the only evidence is a build that gives up on the first blip
// months after someone believed they had configured otherwise. The declared
// HttpRetry object gives the checker the same field list, so a magusfile writing
// HttpRetry{...} is caught earlier still - this is the backstop for the
// map-literal form.
//
// Durations arrive in MILLISECONDS here while httpOpts.timeout is in seconds.
// That is not an inconsistency to tidy away: timeout mirrors curl --max-time,
// which is seconds, while a retry delay is routinely sub-second and expressing
// 250ms as 0.25 invites a caller to write 250 and wait four minutes. The field
// names carry the unit (delay_ms) so neither is guessable.
func parseHTTPRetry(m map[string]any) (httpRetry, error) {
	var r httpRetry
	if m == nil {
		return r, nil
	}
	unknown := make([]string, 0, len(m))
	for k := range m {
		if !httpRetryKeys[k] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return r, fmt.Errorf("http: unknown retry option(s) %s (want attempts, delay_ms, max_delay_ms, max_elapsed_ms, fixed, all_errors, conn_refused)",
			strings.Join(unknown, ", "))
	}
	if f, ok := retryFloat(m["attempts"]); ok && f > 0 {
		r.attempts = int(f)
	}
	r.delay = httpMillis(m, "delay_ms")
	r.maxDelay = httpMillis(m, "max_delay_ms")
	r.maxElapsed = httpMillis(m, "max_elapsed_ms")
	r.fixed = httpBool(m, "fixed")
	r.allErrs = httpBool(m, "all_errors")
	r.connRefused = httpBool(m, "conn_refused")
	return r, nil
}

// httpMillis reads key as a duration expressed in (possibly fractional) ms.
func httpMillis(m map[string]any, key string) time.Duration {
	f, ok := retryFloat(m[key])
	if !ok || f <= 0 {
		return 0
	}
	return time.Duration(f * float64(time.Millisecond))
}

// client builds the *http.Client for one call from the options and the retry
// policy. With no policy this is a single-attempt client.
func (o httpOpts) client(r httpRetry) *http.Client {
	attempts := 1
	if r.retries() {
		attempts = r.attempts
	}
	ropts := []retry.Option{retry.WithAttempts(attempts)}
	switch {
	case r.delay > 0 && r.fixed:
		// A fixed delay mirrors curl --retry-delay: a constant pause, no doubling.
		ropts = append(ropts, retry.WithDelay(r.delay), retry.WithFixedDelay())
	case r.delay > 0:
		ropts = append(ropts, retry.WithDelay(r.delay))
	default:
		ropts = append(ropts, retry.WithDelay(500*time.Millisecond))
	}
	if r.maxDelay > 0 {
		ropts = append(ropts, retry.WithMaxDelay(r.maxDelay))
	}
	if r.maxElapsed > 0 {
		ropts = append(ropts, retry.WithMaxElapsed(r.maxElapsed))
	}
	// Always carry an overall timeout: the per-call "timeout" opt when set, else
	// the same default the shared client uses, so a custom client never reverts
	// to an unbounded (zero) timeout.
	timeout := defaultHTTPTimeout
	if o.timeout > 0 {
		timeout = o.timeout
	}
	ropts = append(ropts, retry.WithTimeout(timeout))
	// Inlined rather than routed through a decider() helper that returns
	// func(*http.Response, error) bool: bodyclose's return-type check is a plain
	// substring match, and that signature's text contains "*net/http.Response",
	// so a helper returning it gets misread as a call that itself produces an
	// unclosed response. The real response lifecycle is unaffected either way -
	// shouldRetry only ever inspects resp.StatusCode, never resp.Body, so it has
	// nothing to close; ownership of Body stays with retryTransport.RoundTrip
	// (internal/retry/retry.go), which closes it on every attempt it discards,
	// and with the caller's own defer resp.Body.Close() on the attempt it keeps.
	ropts = append(ropts, retry.WithRetryDecider(func(resp *http.Response, err error) bool {
		return o.shouldRetry(r, resp, err)
	}))
	return retry.NewHTTPClient(nil, ropts...)
}

// shouldRetry is the retry policy derived from the curl-style options. Exactly
// one of resp/err is non-nil. Transport errors retry on timeouts (and, when
// opted in, connection-refused or any error); HTTP responses retry on the curl
// transient set (5xx/408/429) unless retry_all_errors widens it or fail_early
// disables status retries entirely.
func (o httpOpts) shouldRetry(r httpRetry, resp *http.Response, err error) bool {
	if err != nil {
		switch {
		case r.allErrs:
			return true
		case isConnRefused(err):
			return r.connRefused
		case isTimeout(err):
			return true
		default:
			return false
		}
	}
	code := resp.StatusCode
	if code < 400 {
		return false
	}
	if o.failEarly {
		return false
	}
	if r.allErrs {
		return true
	}
	return code >= 500 || code == http.StatusRequestTimeout || code == http.StatusTooManyRequests
}

// parseHTTPOpts reads the curl-style options map. Unknown keys are ignored;
// durations are read in seconds (curl's unit) and may be fractional.
func parseHTTPOpts(m map[string]any) httpOpts {
	var o httpOpts
	if m == nil {
		return o
	}
	o.fail = httpBool(m, "fail")
	o.failWithBody = httpBool(m, "fail_with_body")
	o.failEarly = httpBool(m, "fail_early")
	if d, ok := httpSeconds(m, "timeout"); ok {
		o.timeout = d
	}
	return o
}

func httpBool(m map[string]any, key string) bool {
	if v, ok := m[key]; ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

// httpSeconds reads key as a duration expressed in (possibly fractional) seconds.
func httpSeconds(m map[string]any, key string) (time.Duration, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	f, ok := retryFloat(v)
	if !ok || f < 0 {
		return 0, false
	}
	return time.Duration(f * float64(time.Second)), true
}

// isConnRefused reports whether err is (or wraps) a connection-refused error.
func isConnRefused(err error) bool { return errors.Is(err, syscall.ECONNREFUSED) }

// isTimeout reports whether err is (or wraps) a network timeout.
func isTimeout(err error) bool {
	var nerr net.Error
	return errors.As(err, &nerr) && nerr.Timeout()
}

// StatusError is what `fail`/`fail_with_body` raises when a response is >= 400.
//
// It exists so a Buzz caller can BRANCH on the status. The thrown value used to be
// `{"message": "http.get https://...: server returned 404"}` and nothing else, so
// telling a 404 (this resource does not exist, which is often a normal answer) from
// a 500 (the server is broken, which is not) meant substring-matching English prose
// - and prose is the one part of an error nobody promises to keep stable.
//
// Additive: `message` reads exactly as before, so anything matching on it still works.
type StatusError struct {
	Method string
	URL    string
	Status int
	// Body is carried only under fail_with_body, matching curl --fail-with-body.
	Body string
}

// statusErrorIsStructured pins the contract that makes the fields reachable from
// Buzz: vm.StructuredError is satisfied structurally, so this type needs no import
// from the interpreter to become a map rather than a string.
var _ interface{ BuzzError() map[string]string } = (*StatusError)(nil)

func (e *StatusError) Error() string {
	msg := "http." + e.Method + " " + e.URL + ": server returned " + strconv.Itoa(e.Status)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// BuzzError renders the error as the map a Buzz `catch` receives.
func (e *StatusError) BuzzError() map[string]string {
	fields := map[string]string{
		"message": e.Error(),
		"status":  strconv.Itoa(e.Status),
		"url":     e.URL,
		"method":  e.Method,
	}
	if e.Body != "" {
		fields["body"] = e.Body
	}
	return fields
}
