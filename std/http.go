package std

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"

	"github.com/egladman/magus/internal/retry"
	"github.com/egladman/magus/internal/sandbox"
	"github.com/egladman/magus/types"
)

//go:generate go run ../cmd/magus-bindings-gen -module http -lang buzz -out ../host/gen/http.go

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
// is active. There is no SSRF guard — any URL a magusfile passes is fetched,
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

func doRequest(ctx context.Context, method, url, body string, headers map[string]string, opts map[string]any) (types.HTTPResponse, error) {
	// Outbound egress is AUDITED BUT NOT BLOCKED: when the sandbox is active the
	// request is logged, but every URL is still fetched — including localhost,
	// RFC1918 ranges, and the cloud metadata endpoint (169.254.169.254). There is
	// no SSRF allow/deny enforcement here yet; treat URLs reachable from a
	// magusfile as trusted. See the http module Doc for the current guarantee.
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

	// curl --fail / --fail-with-body: a >= 400 status is reported as an error
	// rather than a result. --fail-with-body still surfaces the body (in the
	// error message), --fail omits it.
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
// default client" — see custom.
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
// fail_with_body alone do not — they are applied after the response.
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
