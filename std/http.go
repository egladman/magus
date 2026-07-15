//go:build !wasm

package std

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/retry"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-utils bindings -module http -lang buzz -out ../host/gen/http.go

func init() { Register(HTTP) }

// optsDoc is the trailing line shared by every method's Doc describing the
// optional curl-style options map (last argument).
const optsDoc = " opts (curl-style): fail, fail_with_body, fail_early (bool); " +
	"retry (int), retry_delay, retry_max_time, timeout (seconds, default 30); " +
	"retry_all_errors, retry_connrefused (bool)."

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
	Doc:  "HTTP client with automatic retry on transient errors.",
	Methods: []Method{
		{
			Name: "get",
			Doc:  "Send a GET request; returns {status, body, headers}." + optsDoc,
			Args: []Arg{
				{Name: "url", Type: TypeString},
				{Name: "headers", Type: TypeStringMap, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
			Impl:    HTTPGet,
		},
		{
			Name: "post",
			Doc:  "Send a POST request with body; returns {status, body, headers}." + optsDoc,
			Args: []Arg{
				{Name: "url", Type: TypeString},
				{Name: "body", Type: TypeString},
				{Name: "headers", Type: TypeStringMap, Optional: true},
				{Name: "opts", Type: TypeAnyMap, Optional: true},
			},
			Returns: []Ret{{Type: TypeAnyMap}},
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
			},
			Returns: []Ret{{Type: TypeAnyMap}},
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

var defaultHTTPClient = retry.NewHTTPClient(
	nil,
	retry.WithAttempts(3),
	retry.WithDelay(500*time.Millisecond),
	retry.WithTimeout(defaultHTTPTimeout),
)

// HTTPGet sends a GET request to url with optional headers and curl-style opts,
// returning {status, body, headers}.
func HTTPGet(ctx context.Context, url string, headers map[string]string, opts map[string]any) (types.HTTPResponse, error) {
	return doRequest(ctx, http.MethodGet, url, "", headers, opts)
}

// HTTPPost sends a POST of body to url with optional headers and curl-style opts,
// returning {status, body, headers}.
func HTTPPost(ctx context.Context, url, body string, headers map[string]string, opts map[string]any) (types.HTTPResponse, error) {
	return doRequest(ctx, http.MethodPost, url, body, headers, opts)
}

// HTTPRequest sends a request with the given method to url and returns
// {status, body, headers}.
func HTTPRequest(ctx context.Context, method, url, body string, headers map[string]string, opts map[string]any) (types.HTTPResponse, error) {
	return doRequest(ctx, method, url, body, headers, opts)
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

func doRequest(ctx context.Context, method, url, body string, headers map[string]string, opts map[string]any) (types.HTTPResponse, error) {
	if types.Tracing(ctx) {
		return types.HTTPResponse{}, nil
	}
	// Outbound egress is AUDITED BUT NOT BLOCKED: with the sandbox active the
	// request is logged, but every URL is still fetched, including localhost,
	// RFC1918 ranges, and the cloud metadata endpoint (169.254.169.254). No SSRF
	// enforcement yet; treat any URL a magusfile reaches as trusted. See the http
	// module Doc.
	if p := sandbox.FromContext(ctx); p != nil {
		p.RecordConnect(ctx, method, url)
	}

	o := parseHTTPOpts(opts)

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
	if o.custom() {
		client = o.client()
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
		if o.failWithBody {
			return types.HTTPResponse{}, fmt.Errorf("http.%s %s: server returned %d: %s",
				strings.ToLower(method), url, resp.StatusCode, string(raw))
		}
		return types.HTTPResponse{}, fmt.Errorf("http.%s %s: server returned %d",
			strings.ToLower(method), url, resp.StatusCode)
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

	retry        int           // --retry N: number of retries (attempts = N+1); -1 = unset
	retryDelay   time.Duration // --retry-delay: fixed pause between attempts; 0 = unset
	retryMaxTime time.Duration // --retry-max-time: total wall-clock cap on retrying
	retryAllErrs bool          // --retry-all-errors: retry any error, incl. 4xx
	retryConnRef bool          // --retry-connrefused: treat connection-refused as retryable
	timeout      time.Duration // --max-time: overall request timeout; 0 = use default
}

// custom reports whether any retry-affecting option was set, i.e. whether the
// request needs a per-call client instead of the shared default. fail and
// fail_with_body alone do not; they are applied after the response.
func (o httpOpts) custom() bool {
	return o.failEarly || o.retry >= 0 || o.retryDelay > 0 || o.retryMaxTime > 0 ||
		o.retryAllErrs || o.retryConnRef || o.timeout > 0
}

// client builds a retrying *http.Client configured from the parsed options.
func (o httpOpts) client() *http.Client {
	attempts := 3 // default when retry is unset (matches defaultHTTPClient)
	if o.retry >= 0 {
		attempts = o.retry + 1
	}
	ropts := []retry.Option{retry.WithAttempts(attempts)}
	if o.retryDelay > 0 {
		// A fixed delay mirrors curl --retry-delay (a constant pause, no doubling).
		ropts = append(ropts, retry.WithDelay(o.retryDelay), retry.WithFixedDelay())
	} else {
		ropts = append(ropts, retry.WithDelay(500*time.Millisecond))
	}
	if o.retryMaxTime > 0 {
		ropts = append(ropts, retry.WithMaxElapsed(o.retryMaxTime))
	}
	// Always carry an overall timeout: the per-call "timeout" opt when set, else
	// the same default the shared client uses, so a custom client never reverts
	// to an unbounded (zero) timeout.
	timeout := defaultHTTPTimeout
	if o.timeout > 0 {
		timeout = o.timeout
	}
	ropts = append(ropts, retry.WithTimeout(timeout))
	ropts = append(ropts, retry.WithRetryDecider(o.shouldRetry))
	return retry.NewHTTPClient(nil, ropts...)
}

// shouldRetry is the retry policy derived from the curl-style options. Exactly
// one of resp/err is non-nil. Transport errors retry on timeouts (and, when
// opted in, connection-refused or any error); HTTP responses retry on the curl
// transient set (5xx/408/429) unless retry_all_errors widens it or fail_early
// disables status retries entirely.
func (o httpOpts) shouldRetry(resp *http.Response, err error) bool {
	if err != nil {
		switch {
		case o.retryAllErrs:
			return true
		case isConnRefused(err):
			return o.retryConnRef
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
	if o.retryAllErrs {
		return true
	}
	return code >= 500 || code == http.StatusRequestTimeout || code == http.StatusTooManyRequests
}

// parseHTTPOpts reads the curl-style options map. Unknown keys are ignored;
// durations are read in seconds (curl's unit) and may be fractional.
func parseHTTPOpts(m map[string]any) httpOpts {
	o := httpOpts{retry: -1}
	if m == nil {
		return o
	}
	o.fail = httpBool(m, "fail")
	o.failWithBody = httpBool(m, "fail_with_body")
	o.failEarly = httpBool(m, "fail_early")
	o.retryAllErrs = httpBool(m, "retry_all_errors")
	o.retryConnRef = httpBool(m, "retry_connrefused")
	if v, ok := m["retry"]; ok {
		if f, ok := retryFloat(v); ok && f >= 0 {
			o.retry = int(f)
		}
	}
	if d, ok := httpSeconds(m, "retry_delay"); ok {
		o.retryDelay = d
	}
	if d, ok := httpSeconds(m, "retry_max_time"); ok {
		o.retryMaxTime = d
	}
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
