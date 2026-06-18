package std

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	buzz "github.com/egladman/gopherbuzz"
	"github.com/egladman/magus/internal/retry"
	"github.com/egladman/magus/internal/sandbox"
)

//go:generate go run ../../cmd/magus-bindings-gen -module http -lang buzz -out gen/buzz/http.go

func init() { Register(HTTP) }

// optsDoc is the trailing line shared by every method's Doc describing the
// optional curl-style options map (last argument).
const optsDoc = " opts (curl-style): fail, fail_with_body, fail_early (bool); " +
	"retry (int), retry_delay, retry_max_time (seconds); retry_all_errors, retry_connrefused (bool)."

// HTTP is the "http" host module: an HTTP client with automatic retry on
// transient errors and curl-style per-request control over retry and failure.
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
)

// HTTPGet sends a GET request to url with optional headers and curl-style opts,
// returning {status, body, headers}.
func HTTPGet(ctx context.Context, url string, headers map[string]string, opts map[string]any) (map[string]any, error) {
	return doRequest(ctx, http.MethodGet, url, "", headers, opts)
}

// HTTPPost sends a POST of body to url with optional headers and curl-style opts,
// returning {status, body, headers}.
func HTTPPost(ctx context.Context, url, body string, headers map[string]string, opts map[string]any) (map[string]any, error) {
	return doRequest(ctx, http.MethodPost, url, body, headers, opts)
}

// HTTPRequest sends a request with the given method to url and returns
// {status, body, headers}.
func HTTPRequest(ctx context.Context, method, url, body string, headers map[string]string, opts map[string]any) (map[string]any, error) {
	return doRequest(ctx, method, url, body, headers, opts)
}

func doRequest(ctx context.Context, method, url, body string, headers map[string]string, opts map[string]any) (map[string]any, error) {
	// Audit log every outbound request when the sandbox is active.
	// Not yet blocked; this gives operators visibility before enforcement lands.
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
		return nil, fmt.Errorf("http.%s: %w", strings.ToLower(method), err)
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
		return nil, fmt.Errorf("http.%s %s: %w", strings.ToLower(method), url, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("http.%s %s: read body: %w", strings.ToLower(method), url, err)
	}

	// curl --fail / --fail-with-body: a >= 400 status is reported as an error
	// rather than a result map. --fail-with-body still surfaces the body (in the
	// error message), --fail omits it.
	if (o.fail || o.failWithBody) && resp.StatusCode >= 400 {
		if o.failWithBody {
			return nil, fmt.Errorf("http.%s %s: server returned %d: %s",
				strings.ToLower(method), url, resp.StatusCode, string(raw))
		}
		return nil, fmt.Errorf("http.%s %s: server returned %d",
			strings.ToLower(method), url, resp.StatusCode)
	}

	// Collect response headers; where a name has multiple values, take the first.
	respHeaders := make(map[string]any, len(resp.Header))
	for k, vs := range resp.Header {
		if len(vs) > 0 {
			respHeaders[k] = vs[0]
		}
	}
	return map[string]any{
		"status":  resp.StatusCode,
		"body":    string(raw),
		"headers": respHeaders,
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
}

// custom reports whether any retry-affecting option was set, i.e. whether the
// request needs a per-call client instead of the shared default. fail and
// fail_with_body alone do not — they are applied after the response.
func (o httpOpts) custom() bool {
	return o.failEarly || o.retry >= 0 || o.retryDelay > 0 || o.retryMaxTime > 0 ||
		o.retryAllErrs || o.retryConnRef
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

// maxChunk caps a single upload chunk; callers may request less but not more.
// 32 MiB matches the GitHub Actions Cache per-PATCH ceiling, the tightest of the
// providers this module targets.
const maxChunk = 32 * 1024 * 1024

// RegisterExtraHTTP builds the "magus/extra/http" module map: the byte-level HTTP
// primitives the methods above deliberately omit — streaming a response body to a
// file, reading a file's byte length, and uploading a file in Content-Range
// chunks. They can't be declarative Methods (Buzz strings are rune-oriented:
// len() counts runes, strings can't be byte-indexed), so a magusfile cannot
// compute a binary blob's byte size or slice it into upload chunks on its own.
// These three keep the bytes in Go while leaving all protocol decisions — URLs,
// headers, auth, chunk size — to the calling Buzz script. That split is what lets
// a remote cache backend (e.g. GitHub Actions Cache) be written entirely in Buzz
// with no provider-specific Go code: see the cache RemoteBackend bridge that
// imports this. The module is hand-written against the gopherbuzz value API and
// merged into the generated http map at bind time; the host installs it with
// sess.SetSyntheticModule("magus/extra/http", RegisterExtraHTTP(ctx, sess)) so a
// script reaches it via `import "magus/extra/http"`.
func RegisterExtraHTTP(_ context.Context, _ *buzz.Session) buzz.Value {
	m := buzz.NewMap()

	// download(url, dest, headers?) -> int
	// GET url, streaming the response body to dest (created/truncated). Returns
	// the HTTP status code; the body is never materialised as a Buzz string, so
	// arbitrary binary survives intact. A non-2xx status writes no file.
	m.MapSet("download", buzz.DirectValue("extra/http.download", func(ctx context.Context, args []buzz.Value) (buzz.Value, error) {
		url := strArg(args, 0)
		dest := strArg(args, 1)
		headers := mapArg(args, 2)
		status, err := download(ctx, url, dest, headers)
		if err != nil {
			return buzz.Null, err
		}
		return buzz.IntValue(int64(status)), nil
	}))

	// byteSize(path) -> int
	// Byte length of the file at path. The companion to upload_chunked: a script
	// needs the true byte count for a Content-Range total or a commit "size",
	// which len() on a Buzz string cannot give for binary data. Named byteSize
	// (not size) because a module map's built-in .size() method — its entry count —
	// shadows a stored key of the same name.
	m.MapSet("byteSize", buzz.DirectValue("extra/http.byteSize", func(_ context.Context, args []buzz.Value) (buzz.Value, error) {
		fi, err := os.Stat(strArg(args, 0))
		if err != nil {
			return buzz.Null, fmt.Errorf("extra/http.byteSize: %w", err)
		}
		return buzz.IntValue(fi.Size()), nil
	}))

	// upload_chunked(method, url, src, chunk_size, headers?) -> [int, str]
	// Send the file at src as the request body using method. When chunk_size > 0
	// the file is sent in chunk_size-byte slices (capped at 32 MiB), each carrying
	// a `Content-Range: bytes a-b/total` header — the resumable-upload convention
	// GitHub Actions Cache (and RFC 7233 servers) expect. chunk_size <= 0 sends
	// the whole file in one request with no Content-Range. Returns the final
	// [status, body]; body is small (servers ack chunks with empty/JSON bodies).
	m.MapSet("upload_chunked", buzz.DirectValue("extra/http.upload_chunked", func(ctx context.Context, args []buzz.Value) (buzz.Value, error) {
		method := strArg(args, 0)
		url := strArg(args, 1)
		src := strArg(args, 2)
		chunk := intArg(args, 3)
		headers := mapArg(args, 4)
		status, body, err := uploadChunked(ctx, method, url, src, chunk, headers)
		if err != nil {
			return buzz.Null, err
		}
		return buzz.ListValue([]buzz.Value{buzz.IntValue(int64(status)), buzz.StrValue(body)}), nil
	}))

	return m
}

func download(ctx context.Context, url, dest string, headers map[string]string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, fmt.Errorf("extra/http.download: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("extra/http.download %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Only a 200 carries a full body worth persisting; for anything else
		// (204 miss, 4xx, redirects) write nothing and let the caller branch on
		// the returned status. Avoids leaving stray empty files on a miss.
		return resp.StatusCode, nil
	}

	// Write atomically: temp + rename, so a reader never sees a partial file.
	tmp := dest + ".dl.tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("extra/http.download: create: %w", err)
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return resp.StatusCode, fmt.Errorf("extra/http.download: write: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return resp.StatusCode, err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return resp.StatusCode, err
	}
	return resp.StatusCode, nil
}

func uploadChunked(ctx context.Context, method, url, src string, chunkSize int64, headers map[string]string) (int, string, error) {
	f, err := os.Open(src)
	if err != nil {
		return 0, "", fmt.Errorf("extra/http.upload_chunked: open: %w", err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return 0, "", fmt.Errorf("extra/http.upload_chunked: stat: %w", err)
	}
	total := fi.Size()

	// Single-shot: whole file as the body, no Content-Range.
	if chunkSize <= 0 {
		return sendChunk(ctx, method, url, f, total, headers, "")
	}
	if chunkSize > maxChunk {
		chunkSize = maxChunk
	}

	var status int
	var body string
	for offset := int64(0); offset < total; offset += chunkSize {
		end := offset + chunkSize
		if end > total {
			end = total
		}
		section := io.NewSectionReader(f, offset, end-offset)
		rng := fmt.Sprintf("bytes %d-%d/%d", offset, end-1, total)
		var err error
		status, body, err = sendChunk(ctx, method, url, section, end-offset, headers, rng)
		if err != nil {
			return status, body, err
		}
		// Stop at the first chunk the server rejects rather than uploading the
		// rest against an entry it has already refused; return its status so the
		// caller branches. Not a Go error — a 4xx/5xx is a server decision.
		if status < 200 || status >= 300 {
			return status, body, nil
		}
	}
	return status, body, nil
}

func sendChunk(ctx context.Context, method, url string, body io.Reader, length int64, headers map[string]string, contentRange string) (int, string, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return 0, "", fmt.Errorf("extra/http.upload_chunked: request: %w", err)
	}
	req.ContentLength = length
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if contentRange != "" {
		req.Header.Set("Content-Range", contentRange)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("extra/http.upload_chunked %s: %w", url, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	return resp.StatusCode, string(rb), nil
}

func strArg(args []buzz.Value, i int) string {
	if i < len(args) && args[i].IsStr() {
		return args[i].AsString()
	}
	return ""
}

func intArg(args []buzz.Value, i int) int64 {
	if i < len(args) && args[i].IsInt() {
		return args[i].AsInt()
	}
	return 0
}

func mapArg(args []buzz.Value, i int) map[string]string {
	if i >= len(args) || !args[i].IsMap() {
		return nil
	}
	out := map[string]string{}
	for _, k := range args[i].MapKeys() {
		if v, ok := args[i].MapGet(k); ok && v.IsStr() {
			out[k] = v.AsString()
		}
	}
	return out
}
