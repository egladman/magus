package std

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
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
