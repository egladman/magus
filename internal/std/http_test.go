package std

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
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
	if !o.fail || !o.failWithBody || !o.failEarly || !o.retryAllErrs || !o.retryConnRef {
		t.Fatalf("bool options not all set: %+v", o)
	}
	if o.retry != 4 {
		t.Errorf("retry = %d, want 4", o.retry)
	}
	if o.retryDelay != 1500*time.Millisecond {
		t.Errorf("retryDelay = %v, want 1.5s", o.retryDelay)
	}
	if o.retryMaxTime != 10*time.Second {
		t.Errorf("retryMaxTime = %v, want 10s", o.retryMaxTime)
	}
	if !o.custom() {
		t.Error("custom() = false, want true with retry options set")
	}

	// An empty/nil map is the default (non-custom) request.
	if parseHTTPOpts(nil).custom() {
		t.Error("nil opts should not be custom")
	}
	if (httpOpts{retry: -1}).custom() {
		t.Error("zero opts should not be custom")
	}
}

// TestHTTPOptsShouldRetry exercises the retry policy decider directly.
func TestHTTPOptsShouldRetry(t *testing.T) {
	t.Parallel()
	resp := func(code int) *http.Response { return &http.Response{StatusCode: code} }
	connRefused := fmt.Errorf("dial: %w", syscall.ECONNREFUSED)
	timeoutErr := timeoutError{}

	cases := []struct {
		name string
		opts httpOpts
		resp *http.Response
		err  error
		want bool
	}{
		{"5xx default retries", httpOpts{retry: -1}, resp(503), nil, true},
		{"408 retries", httpOpts{retry: -1}, resp(408), nil, true},
		{"429 retries", httpOpts{retry: -1}, resp(429), nil, true},
		{"404 default no retry", httpOpts{retry: -1}, resp(404), nil, false},
		{"404 retry_all_errors", httpOpts{retry: -1, retryAllErrs: true}, resp(404), nil, true},
		{"2xx never retries", httpOpts{retry: -1, retryAllErrs: true}, resp(200), nil, false},
		{"fail_early skips 503", httpOpts{retry: -1, failEarly: true}, resp(503), nil, false},
		{"timeout retries", httpOpts{retry: -1}, nil, timeoutErr, true},
		{"connrefused off by default", httpOpts{retry: -1}, nil, connRefused, false},
		{"connrefused opted in", httpOpts{retry: -1, retryConnRef: true}, nil, connRefused, true},
		{"connrefused via all_errors", httpOpts{retry: -1, retryAllErrs: true}, nil, connRefused, true},
		{"other transport err no retry", httpOpts{retry: -1}, nil, errors.New("boom"), false},
	}
	for _, c := range cases {
		if got := c.opts.shouldRetry(c.resp, c.err); got != c.want {
			t.Errorf("%s: shouldRetry = %v, want %v", c.name, got, c.want)
		}
	}
}

// timeoutError is a net.Error reporting Timeout()==true.
type timeoutError struct{}

func (timeoutError) Error() string   { return "i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

func TestHTTPFail(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	// Without fail: a 404 is a normal result map.
	res, err := HTTPGet(context.Background(), srv.URL, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error without fail: %v", err)
	}
	if res["status"] != http.StatusNotFound {
		t.Fatalf("status = %v, want 404", res["status"])
	}

	// With fail: a 404 becomes an error, no body in the message.
	_, err = HTTPGet(context.Background(), srv.URL, nil, map[string]any{"fail": true})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("fail: err = %v, want a 404 error", err)
	}
	if strings.Contains(err.Error(), "nope") {
		t.Errorf("fail: body leaked into error: %v", err)
	}

	// With fail_with_body: the body is surfaced in the error.
	_, err = HTTPGet(context.Background(), srv.URL, nil, map[string]any{"fail_with_body": true})
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("fail_with_body: err = %v, want body in error", err)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res["status"] != http.StatusOK || res["body"] != "ok" {
		t.Fatalf("res = %v, want 200/ok", res)
	}
	if got := atomic.LoadInt32(&hits); got != 3 {
		t.Fatalf("server hits = %d, want 3", got)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res["status"] != http.StatusServiceUnavailable {
		t.Fatalf("status = %v, want 503", res["status"])
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("server hits = %d, want 1 (fail_early)", got)
	}
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
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res["status"] != http.StatusOK {
		t.Fatalf("status = %v, want 200", res["status"])
	}
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("server hits = %d, want 2", got)
	}
}
