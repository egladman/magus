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
	"sort"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	buzz "github.com/egladman/gopherbuzz"
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

func TestHTTPFail(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer srv.Close()

	// Without fail: a 404 is a normal result map.
	res, err := HTTPGet(context.Background(), srv.URL, nil, nil)
	require.NoError(t, err, "unexpected error without fail")
	assert.Equal(t, http.StatusNotFound, res["status"])

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
	assert.Equal(t, http.StatusOK, res["status"])
	assert.Equal(t, "ok", res["body"])
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
	assert.Equal(t, http.StatusServiceUnavailable, res["status"])
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
	assert.Equal(t, http.StatusOK, res["status"])
	assert.Equal(t, int32(2), atomic.LoadInt32(&hits), "server hits")
}

// blob deliberately includes NUL and 0xFF bytes — invalid UTF-8 — to prove the
// extra/http primitives move bytes opaquely and never round-trip a payload
// through a rune-oriented Buzz string.
var blob = []byte{0x00, 0x01, 0xff, 0xfe, 'h', 'i', 0x00, 0x80, 0x7f, 0xff, 'b', 'y', 'e', 0x00}

func newHTTPSession(t *testing.T) *buzz.Session {
	t.Helper()
	sess := buzz.NewSession(context.Background(), buzz.WithEmbedded())
	sess.SetSyntheticModule("magus/extra/http", RegisterExtraHTTP(context.Background(), sess))
	return sess
}

// callExport execs src, then invokes the exported function name with args.
func callExport(t *testing.T, sess *buzz.Session, src, name string, args ...buzz.Value) buzz.Value {
	t.Helper()
	require.NoError(t, sess.Exec(context.Background(), src), "Exec")
	fn, ok := sess.Exports()[name]
	require.True(t, ok, "export %q not found", name)
	v, err := sess.CallValue(context.Background(), fn, args)
	require.NoError(t, err, "call %q", name)
	return v
}

func TestDownloadStreamsBinaryToFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "yes", r.Header.Get("X-Test"), "header not forwarded")
		_, _ = w.Write(blob)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	src := `
import "magus/extra/http" as xhttp
export fun dl(url: str, dest: str) > int {
    return xhttp.download(url, dest, {"X-Test": "yes"});
}`
	got := callExport(t, newHTTPSession(t), src, "dl", buzz.StrValue(srv.URL), buzz.StrValue(dest))
	require.True(t, got.IsInt(), "status not an int: %v", got)
	assert.Equal(t, int64(200), got.AsInt())
	data, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, blob, data)
}

func TestDownloadNon2xxWritesNoFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	dest := filepath.Join(t.TempDir(), "missing.bin")
	src := `
import "magus/extra/http" as xhttp
export fun dl(url: str, dest: str) > int { return xhttp.download(url, dest, {}); }`
	got := callExport(t, newHTTPSession(t), src, "dl", buzz.StrValue(srv.URL), buzz.StrValue(dest))
	assert.Equal(t, int64(204), got.AsInt())
	_, err := os.Stat(dest)
	assert.True(t, os.IsNotExist(err), "expected no file at %s, stat err = %v", dest, err)
}

func TestSizeReportsByteLength(t *testing.T) {
	p := filepath.Join(t.TempDir(), "blob.bin")
	require.NoError(t, os.WriteFile(p, blob, 0o644))
	src := `
import "magus/extra/http" as xhttp
export fun sz(p: str) > int { return xhttp.byteSize(p); }`
	got := callExport(t, newHTTPSession(t), src, "sz", buzz.StrValue(p))
	assert.Equal(t, int64(len(blob)), got.AsInt())
}

func TestUploadChunkedReassembles(t *testing.T) {
	var mu sync.Mutex
	chunks := map[int64][]byte{}
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cr := r.Header.Get("Content-Range")
		var off, end, total int64
		_, err := fmt.Sscanf(cr, "bytes %d-%d/%d", &off, &end, &total)
		assert.NoError(t, err, "bad Content-Range %q", cr)
		mu.Lock()
		chunks[off] = body
		ranges = append(ranges, cr)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	srcFile := filepath.Join(t.TempDir(), "upload.bin")
	require.NoError(t, os.WriteFile(srcFile, blob, 0o644))

	// chunk_size 4 over a 14-byte blob forces 4 chunks (4+4+4+2).
	src := `
import "magus/extra/http" as xhttp
export fun up(url: str, src: str, chunk: int) > any {
    return xhttp.upload_chunked("PATCH", url, src, chunk, {});
}`
	got := callExport(t, newHTTPSession(t), src, "up", buzz.StrValue(srv.URL), buzz.StrValue(srcFile), buzz.IntValue(4))
	require.True(t, got.IsList(), "upload return = %v, want [status, body]", got)
	require.Len(t, got.ListItems(), 2, "upload return = %v, want [status, body]", got)
	assert.Equal(t, int64(204), got.ListItems()[0].AsInt(), "final status")

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, ranges, 4, "got chunks %v", ranges)
	// Reassemble in offset order and compare to the original.
	offsets := make([]int64, 0, len(chunks))
	for off := range chunks {
		offsets = append(offsets, off)
	}
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	var got2 []byte
	for _, off := range offsets {
		got2 = append(got2, chunks[off]...)
	}
	assert.Equal(t, blob, got2, "reassembled")
}

func TestUploadSingleShotNoContentRange(t *testing.T) {
	var gotRange string
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Content-Range")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	srcFile := filepath.Join(t.TempDir(), "upload.bin")
	require.NoError(t, os.WriteFile(srcFile, blob, 0o644))
	src := `
import "magus/extra/http" as xhttp
export fun up(url: str, src: str) > any {
    return xhttp.upload_chunked("PUT", url, src, 0, {});
}`
	got := callExport(t, newHTTPSession(t), src, "up", buzz.StrValue(srv.URL), buzz.StrValue(srcFile))
	assert.Equal(t, int64(200), got.ListItems()[0].AsInt(), "status")
	assert.Empty(t, gotRange, "single-shot upload sent Content-Range %q, want none", gotRange)
	assert.Equal(t, blob, gotBody, "uploaded bytes")
}
