package std

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseHTTPOpts checks the curl-style options map is decoded, including the
// seconds-to-duration conversion and the retry=-1 "unset" sentinel.
func TestParseHTTPOpts(t *testing.T) {
	t.Parallel()
	o := parseHTTPOpts(map[string]any{
		"fail":              true,
		"fail_with_body":    true,
		"fail_early":        true,
		"retry":             int64(4),
		"retry_delay":       float64(1.5),
		"retry_max_time":    int64(10),
		"retry_all_errors":  true,
		"retry_connrefused": true,
	})
	assert.True(t, o.fail, "fail not set")
	assert.True(t, o.failWithBody, "failWithBody not set")
	assert.True(t, o.failEarly, "failEarly not set")
	assert.True(t, o.retryAllErrs, "retryAllErrs not set")
	assert.True(t, o.retryConnRef, "retryConnRef not set")
	assert.Equal(t, 4, o.retry)
	assert.Equal(t, 1500*time.Millisecond, o.retryDelay)
	assert.Equal(t, 10*time.Second, o.retryMaxTime)
	assert.True(t, o.custom(), "custom() should be true with retry options set")

	// An empty/nil map is the default (non-custom) request.
	assert.False(t, parseHTTPOpts(nil).custom(), "nil opts should not be custom")
	assert.False(t, (httpOpts{retry: -1}).custom(), "zero opts should not be custom")
}

// TestHTTPOptsShouldRetry exercises the retry policy decider directly.
func TestHTTPOptsShouldRetry(t *testing.T) {
	t.Parallel()
	resp := func(code int) *http.Response { return &http.Response{StatusCode: code} }
	connRefused := fmt.Errorf("dial: %w", syscall.ECONNREFUSED)
	timeoutErr := timeoutError{}

	assertRetry := func(want bool, opts httpOpts, r *http.Response, err error) {
		t.Helper()
		assert.Equal(t, want, opts.shouldRetry(r, err))
	}

	t.Run("5xx default retries", func(t *testing.T) {
		assertRetry(true, httpOpts{retry: -1}, resp(503), nil)
	})
	t.Run("408 retries", func(t *testing.T) {
		assertRetry(true, httpOpts{retry: -1}, resp(408), nil)
	})
	t.Run("429 retries", func(t *testing.T) {
		assertRetry(true, httpOpts{retry: -1}, resp(429), nil)
	})
	t.Run("404 default no retry", func(t *testing.T) {
		assertRetry(false, httpOpts{retry: -1}, resp(404), nil)
	})
	t.Run("404 retry_all_errors", func(t *testing.T) {
		assertRetry(true, httpOpts{retry: -1, retryAllErrs: true}, resp(404), nil)
	})
	t.Run("2xx never retries", func(t *testing.T) {
		assertRetry(false, httpOpts{retry: -1, retryAllErrs: true}, resp(200), nil)
	})
	t.Run("fail_early skips 503", func(t *testing.T) {
		assertRetry(false, httpOpts{retry: -1, failEarly: true}, resp(503), nil)
	})
	t.Run("timeout retries", func(t *testing.T) {
		assertRetry(true, httpOpts{retry: -1}, nil, timeoutErr)
	})
	t.Run("connrefused off by default", func(t *testing.T) {
		assertRetry(false, httpOpts{retry: -1}, nil, connRefused)
	})
	t.Run("connrefused opted in", func(t *testing.T) {
		assertRetry(true, httpOpts{retry: -1, retryConnRef: true}, nil, connRefused)
	})
	t.Run("connrefused via all_errors", func(t *testing.T) {
		assertRetry(true, httpOpts{retry: -1, retryAllErrs: true}, nil, connRefused)
	})
	t.Run("other transport err no retry", func(t *testing.T) {
		assertRetry(false, httpOpts{retry: -1}, nil, errors.New("boom"))
	})
}

// timeoutError is a net.Error reporting Timeout()==true.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// TestHTTPClientTimeout checks that both the shared default client and a
// per-call client built from opts carry a non-zero overall Timeout, so a
// stalled server cannot hang a request indefinitely.
func TestHTTPClientTimeout(t *testing.T) {
	t.Parallel()
	assert.Equal(t, defaultHTTPTimeout, defaultHTTPClient.Timeout, "default client timeout")

	// A custom client (retry opt set) still carries the default timeout.
	o := parseHTTPOpts(map[string]any{"retry": int64(2)})
	assert.Equal(t, defaultHTTPTimeout, o.client().Timeout, "custom client default timeout")

	// An explicit timeout opt overrides the default.
	o = parseHTTPOpts(map[string]any{"timeout": float64(0.25)})
	assert.True(t, o.custom(), "timeout opt should make the request custom")
	assert.Equal(t, 250*time.Millisecond, o.client().Timeout, "explicit timeout opt")
}

// TestHTTPTimeoutBoundsSlowServer checks that a server which accepts the
// connection then stalls is bounded by the per-call timeout rather than
// hanging forever.
func TestHTTPTimeoutBoundsSlowServer(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-block // never write; hold the request open until the test ends
	}))
	defer srv.Close()
	defer close(block)

	start := time.Now()
	_, err := HTTPGet(context.Background(), srv.URL, nil, map[string]any{
		"timeout": float64(0.05), // 50ms
		"retry":   int64(0),      // no retries: bound a single attempt
	})
	require.Error(t, err, "want a timeout error, not a hang")
	assert.Less(t, time.Since(start), 5*time.Second, "request was not bounded by timeout")
}

func TestHTTPFail(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	// Without fail: a 404 is a normal result map.
	res, err := HTTPGet(context.Background(), srv.URL, nil, nil)
	require.NoError(t, err, "unexpected error without fail")
	assert.Equal(t, http.StatusNotFound, res.Status)

	// With fail: a 404 becomes an error, no body in the message.
	_, err = HTTPGet(context.Background(), srv.URL, nil, map[string]any{"fail": true})
	require.Error(t, err, "fail: want a 404 error")
	assert.Contains(t, err.Error(), "404", "fail: want a 404 error")
	assert.NotContains(t, err.Error(), "nope", "fail: body leaked into error")

	// With fail_with_body: the body is surfaced in the error.
	_, err = HTTPGet(context.Background(), srv.URL, nil, map[string]any{"fail_with_body": true})
	require.Error(t, err, "fail_with_body: want body in error")
	assert.Contains(t, err.Error(), "nope", "fail_with_body: want body in error")
}

func TestHTTPRetryThenSucceeds(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			http.Error(w, "later", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	res, err := HTTPGet(context.Background(), srv.URL, nil, map[string]any{
		"retry":       int64(3),
		"retry_delay": float64(0.001), // 1ms fixed, keep the test fast
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.Status)
	assert.Equal(t, "ok", res.Body)
	assert.Equal(t, int32(3), atomic.LoadInt32(&hits), "server hits")
}

func TestHTTPFailEarlyDisablesStatusRetry(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		http.Error(w, "later", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	// retry would normally retry a 503, but fail_early stops it at the first try.
	res, err := HTTPGet(context.Background(), srv.URL, nil, map[string]any{
		"retry":      int64(5),
		"fail_early": true,
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusServiceUnavailable, res.Status)
	assert.Equal(t, int32(1), atomic.LoadInt32(&hits), "server hits (fail_early)")
}

func TestHTTPRetryAllErrors(t *testing.T) {
	t.Parallel()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&hits, 1) < 2 {
			http.Error(w, "bad", http.StatusBadRequest) // 400: not in the default transient set
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer srv.Close()

	res, err := HTTPGet(context.Background(), srv.URL, nil, map[string]any{
		"retry":            int64(3),
		"retry_delay":      float64(0.001),
		"retry_all_errors": true,
	})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, res.Status)
	assert.Equal(t, int32(2), atomic.LoadInt32(&hits), "server hits")
}

// TestPathHasPrefix checks the URL-subtree matching that drives mount routing:
// "/" matches everything, a subtree prefix matches its bare form and anything
// under it, and it does not spuriously match a sibling path.
func TestPathHasPrefix(t *testing.T) {
	t.Parallel()
	assert.True(t, pathHasPrefix("/anything", "/"), "root prefix matches all")
	assert.True(t, pathHasPrefix("/console/", "/console/"), "trailing-slash path under subtree")
	assert.True(t, pathHasPrefix("/console/app.js", "/console/"), "file under subtree")
	assert.True(t, pathHasPrefix("/console", "/console/"), "bare form of subtree")
	assert.False(t, pathHasPrefix("/console-x/y", "/console/"), "sibling path not matched")
	assert.False(t, pathHasPrefix("/other", "/console/"), "unrelated path not matched")
}

// mountResp is a GET result captured for whole-struct assertions.
type mountResp struct {
	Status int
	Body   string
}

// getMount performs a GET against a localhost port+path and returns the status
// and body as a single struct.
func getMount(t *testing.T, port int, path string) mountResp {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, path)
	resp, err := http.Get(url) //nolint:noctx // short-lived localhost test request
	require.NoError(t, err, "GET %s", path)
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "read body %s", path)
	return mountResp{Status: resp.StatusCode, Body: string(raw)}
}

// TestHTTPServeSingleDir checks the single-root mode: a "dir" opt serves files
// from that directory.
func TestHTTPServeSingleDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "index.html"), []byte("home page"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.txt"), []byte("a note"), 0o600))

	port, err := HTTPServe(context.Background(), map[string]any{"dir": dir})
	require.NoError(t, err)
	require.NotZero(t, port)

	assert.Equal(t, mountResp{Status: http.StatusOK, Body: "home page"}, getMount(t, port, "/"))
	assert.Equal(t, mountResp{Status: http.StatusOK, Body: "a note"}, getMount(t, port, "/note.txt"))
	assert.Equal(t, http.StatusNotFound, getMount(t, port, "/missing").Status)
}

// TestHTTPServeMounts stands up two roots under a "mounts" prefix map and checks
// that "/" serves the docs root, "/console/" serves the console root, longest-prefix
// precedence lets "/console/" shadow "/" for a shared filename, and an unmounted
// path 404s.
func TestHTTPServeMounts(t *testing.T) {
	t.Parallel()
	docsDir := t.TempDir()
	consoleDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(docsDir, "index.html"), []byte("docs home"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(consoleDir, "index.html"), []byte("console home"), 0o600))
	// A filename that exists in BOTH roots proves longest-prefix routing: served
	// from the console root for a /console/ path, the docs root otherwise.
	require.NoError(t, os.WriteFile(filepath.Join(docsDir, "shared.txt"), []byte("from docs"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(consoleDir, "shared.txt"), []byte("from console"), 0o600))

	// mounts arrives from Buzz as map[string]any (host.AnyMap), so exercise that
	// exact shape here rather than a map[string]string.
	port, err := HTTPServe(context.Background(), map[string]any{
		"mounts": map[string]any{
			"/":         docsDir,
			"/console/": consoleDir,
		},
	})
	require.NoError(t, err)
	require.NotZero(t, port)

	assert.Equal(t, mountResp{Status: http.StatusOK, Body: "docs home"}, getMount(t, port, "/"))
	assert.Equal(t, mountResp{Status: http.StatusOK, Body: "console home"}, getMount(t, port, "/console/"))
	// Shared filename: the "/console/" mount wins for a /console/ path (longest
	// prefix), while "/" falls through to the docs root.
	assert.Equal(t, mountResp{Status: http.StatusOK, Body: "from console"}, getMount(t, port, "/console/shared.txt"))
	assert.Equal(t, mountResp{Status: http.StatusOK, Body: "from docs"}, getMount(t, port, "/shared.txt"))
	// A path with no matching file under the docs root 404s.
	assert.Equal(t, http.StatusNotFound, getMount(t, port, "/nope-missing").Status)
}

// TestParseServerOpts exercises the options-bag validation directly: the valid
// single-dir and mounts shapes decode, and every rejection path (unknown key,
// neither/both of dir/mounts, mistyped values) errors with a clear message.
func TestParseServerOpts(t *testing.T) {
	t.Parallel()

	t.Run("dir only", func(t *testing.T) {
		dir, mounts, port, err := parseServerOpts(map[string]any{"dir": "site"})
		require.NoError(t, err)
		assert.Equal(t, "site", dir)
		assert.Nil(t, mounts)
		assert.Equal(t, 0, port)
	})
	t.Run("mounts with port", func(t *testing.T) {
		dir, mounts, port, err := parseServerOpts(map[string]any{
			"mounts": map[string]any{"/": "docs/gen", "/console/": "console/gen"},
			"port":   int64(9001),
		})
		require.NoError(t, err)
		assert.Empty(t, dir)
		assert.Equal(t, map[string]string{"/": "docs/gen", "/console/": "console/gen"}, mounts)
		assert.Equal(t, 9001, port)
	})
	t.Run("plain int port", func(t *testing.T) {
		_, _, port, err := parseServerOpts(map[string]any{"dir": "site", "port": 8080})
		require.NoError(t, err)
		assert.Equal(t, 8080, port)
	})

	t.Run("unknown key", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{"mport": 8080})
		require.Error(t, err)
		assert.Equal(t, `http.server: unknown option "mport" (known: dir, mounts, port)`, err.Error())
	})
	t.Run("unknown keys sorted", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{"dir": "x", "zed": 1, "abc": 2})
		require.Error(t, err)
		assert.Equal(t, `http.server: unknown options "abc", "zed" (known: dir, mounts, port)`, err.Error())
	})
	t.Run("neither dir nor mounts", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "one of")
	})
	t.Run("both dir and mounts", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{"dir": "x", "mounts": map[string]any{"/": "y"}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not both")
	})
	t.Run("dir wrong type", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{"dir": 42})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"dir" must be a string`)
	})
	t.Run("empty dir", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{"dir": ""})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"dir" must not be empty`)
	})
	t.Run("mounts wrong type", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{"mounts": "not-a-map"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"mounts" must be a map`)
	})
	t.Run("empty mounts", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{"mounts": map[string]any{}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"mounts" must not be empty`)
	})
	t.Run("mount value wrong type", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{"mounts": map[string]any{"/": 7}})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must map to a string dir")
	})
	t.Run("port wrong type", func(t *testing.T) {
		_, _, _, err := parseServerOpts(map[string]any{"dir": "x", "port": "8080"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `"port" must be an int`)
	})
}

// TestHTTPServeRejectsBadOpts checks the top-level HTTPServe surfaces a validation
// error (from parseServerOpts) rather than binding a server.
func TestHTTPServeRejectsBadOpts(t *testing.T) {
	t.Parallel()
	_, err := HTTPServe(context.Background(), map[string]any{"mport": 8080})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `unknown option "mport"`)
}
