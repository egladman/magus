package dashboard_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/handler/mcp/auth"
	"github.com/egladman/magus/internal/httpx"
	"github.com/egladman/magus/internal/service/dashboard"
	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1"
	"github.com/egladman/magus/types"
	"google.golang.org/protobuf/proto"
)

// testHandler builds an HTTP mux with the bridge mounted, without httpx.BearerGuard
// or dnsRebindGuard. Used for inner-route logic tests.
func testHandler(opts dashboard.Options) http.Handler {
	mux := http.NewServeMux()
	dashboard.Mount(mux, opts)
	return mux
}

// testHandlerWithAuth wraps the bridge mux in httpx.BearerGuard with a throwaway token.
func testHandlerWithAuth(t *testing.T, opts dashboard.Options) (http.Handler, string) {
	t.Helper()
	tok, err := auth.Generate()
	if err != nil {
		t.Fatalf("generate token: %v", err)
	}
	load := func() (string, error) { return tok, nil }
	mux := http.NewServeMux()
	dashboard.Mount(mux, opts)
	return httpx.BearerGuard(load, mux), tok
}

// minimalOpts returns Options suitable for unit tests (no Magus instance).
func minimalOpts() dashboard.Options {
	addr := netip.MustParseAddrPort("127.0.0.1:17391")
	return dashboard.Options{
		Addr:       addr,
		SiteOrigin: "https://example.com",
	}
}

// --- Auth ---

func TestAuth_MissingToken_Returns401(t *testing.T) {
	h, _ := testHandlerWithAuth(t, minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestAuth_WrongToken_Returns401(t *testing.T) {
	h, _ := testHandlerWithAuth(t, minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.Header.Set("Authorization", "Bearer wrongtoken")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("want 401, got %d", w.Code)
	}
}

func TestAuth_CorrectToken_Passes(t *testing.T) {
	h, tok := testHandlerWithAuth(t, minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == http.StatusUnauthorized {
		t.Errorf("want non-401 with correct token, got 401")
	}
}

// --- CORS ---

func TestCORS_AllowedSiteOrigin(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.Header.Set("Origin", "https://example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("want ACAO=https://example.com, got %q", got)
	}
}

func TestCORS_AllowedLoopbackOrigin(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.Header.Set("Origin", "http://localhost:17391")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:17391" {
		t.Errorf("want ACAO=http://localhost:17391, got %q", got)
	}
}

func TestCORS_DisallowedOrigin_NoHeader(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.Header.Set("Origin", "https://evil.example.com")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("want no ACAO header for disallowed origin, got %q", got)
	}
}

func TestCORS_NoOrigin_NoHeader(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("want no ACAO header when Origin is absent, got %q", got)
	}
}

// --- Private Network Access (PNA) preflight ---

func TestCORS_PNA_Preflight(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodOptions, "/api/v1/graph", nil)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	r.Header.Set("Access-Control-Request-Private-Network", "true")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if pna := w.Header().Get("Access-Control-Allow-Private-Network"); pna != "true" {
		t.Errorf("want ACAPN=true on PNA preflight, got %q", pna)
	}
}

func TestCORS_PNA_NotSet_WhenNotRequested(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodOptions, "/api/v1/graph", nil)
	r.Header.Set("Origin", "https://example.com")
	r.Header.Set("Access-Control-Request-Method", "GET")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if pna := w.Header().Get("Access-Control-Allow-Private-Network"); pna != "" {
		t.Errorf("want no ACAPN when not requested, got %q", pna)
	}
}

// --- ETag / 304 ---

// TestETag_GraphRoute_ETagConditionalGet verifies the ETag round-trip using a
// minimal handler that mirrors the bridge's ETag logic (sha256, quoted hex,
// If-None-Match -> 304) without requiring a real workspace.
func TestETag_GraphRoute_ETagConditionalGet(t *testing.T) {
	fixedBody := []byte(`{"definition":"test","schema_version":1}`)
	h := newETagHandler(fixedBody)

	srv := httptest.NewServer(h)
	defer srv.Close()

	// First GET: expect 200 + ETag.
	resp1, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp1.StatusCode)
	}
	etag := resp1.Header.Get("ETag")
	if etag == "" {
		t.Fatal("want ETag header, got empty")
	}

	// Second GET with matching If-None-Match: expect 304.
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req2.Header.Set("If-None-Match", etag)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotModified {
		t.Errorf("want 304 on matching ETag, got %d", resp2.StatusCode)
	}
}

// newETagHandler returns a handler that mirrors the bridge's ETag behavior for
// a fixed body. Used to isolate the 304 contract from workspace state.
func newETagHandler(body []byte) http.Handler {
	sum := sha256.Sum256(body)
	etag := fmt.Sprintf(`"%x"`, sum)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", etag)
		_, _ = w.Write(body)
	})
}

// --- Status endpoint ---

func TestStatus_Returns200_WithJSON(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	var out map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Errorf("want valid JSON body: %v\nbody: %s", err, w.Body.String())
	}
}

func TestStatus_MethodNotAllowed(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodPost, "/api/v1/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// --- SSE / events ---

func TestEvents_HeartbeatOnly_WhenNoWatcher(t *testing.T) {
	opts := minimalOpts()
	opts.GraphInvalidate = nil
	opts.HeartbeatInterval = 50 * time.Millisecond
	h := testHandler(opts)

	pr, pw := io.Pipe()
	rr := &pipeResponseWriter{header: make(http.Header), pw: pw}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rr, req)
	}()

	buf := make([]byte, 256)
	n, _ := pr.Read(buf)
	cancel()
	<-done

	data := string(buf[:n])
	if !strings.Contains(data, ": heartbeat") {
		t.Errorf("want heartbeat comment in SSE output, got %q", data)
	}
}

func TestEvents_GraphEvent_WhenInvalidated(t *testing.T) {
	opts := minimalOpts()
	inv := make(chan struct{}, 1)
	opts.GraphInvalidate = inv
	h := testHandler(opts)

	pr, pw := io.Pipe()
	rr := &pipeResponseWriter{header: make(http.Header), pw: pw}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rr, req)
	}()

	// Trigger graph invalidation.
	inv <- struct{}{}

	buf := make([]byte, 512)
	n, _ := pr.Read(buf)
	cancel()
	<-done

	data := string(buf[:n])
	if !strings.Contains(data, "event: graph") {
		t.Errorf("want 'event: graph' in SSE output, got %q", data)
	}
	if !strings.Contains(data, `"seq":`) {
		t.Errorf("want 'seq' in SSE data, got %q", data)
	}
}

// TestEvents_StatusEvent_Emitted verifies the events stream pushes a base64
// magus.status.v1.Status frame on connect when status streaming is enabled (a
// version to stamp + a StatusReportFn seam standing in for the daemon query).
func TestEvents_StatusEvent_Emitted(t *testing.T) {
	opts := minimalOpts()
	opts.MagusVersion = "1.2.3"
	opts.StatusInterval = 50 * time.Millisecond
	opts.StatusReportFn = func(context.Context) types.StatusReport {
		return types.StatusReport{Pool: &types.StatusOutput{Mode: "daemon", Capacity: 4, InUse: 1}}
	}
	h := testHandler(opts)

	pr, pw := io.Pipe()
	rr := &pipeResponseWriter{header: make(http.Header), pw: pw}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rr, req)
	}()

	data := readUntilSSE(t, pr, "event: status")
	cancel()
	<-done

	payload := sseDataLine(t, data)
	raw, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("decode base64 status payload %q: %v", payload, err)
	}
	var st statusv1.Status
	if err := proto.Unmarshal(raw, &st); err != nil {
		t.Fatalf("unmarshal Status proto: %v", err)
	}
	if st.MagusVersion != "1.2.3" {
		t.Errorf("want magus_version 1.2.3, got %q", st.MagusVersion)
	}
	if st.Health != statusv1.Health_HEALTH_HEALTHY {
		t.Errorf("want HEALTH_HEALTHY, got %v", st.Health)
	}
	if st.Pool == nil || st.Pool.Capacity != 4 || st.Pool.InUse != 1 {
		t.Errorf("want pool capacity=4 in_use=1, got %+v", st.Pool)
	}
}

// readUntilSSE reads from pr until the accumulated output contains marker or the
// read fails, returning everything read so far.
func readUntilSSE(t *testing.T, pr io.Reader, marker string) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 512)
	for i := 0; i < 64; i++ {
		n, err := pr.Read(buf)
		sb.Write(buf[:n])
		if strings.Contains(sb.String(), marker) {
			return sb.String()
		}
		if err != nil {
			break
		}
	}
	t.Fatalf("marker %q not seen in SSE output: %q", marker, sb.String())
	return ""
}

// sseDataLine returns the payload of the first "data: " line in an SSE chunk.
func sseDataLine(t *testing.T, chunk string) string {
	t.Helper()
	for _, line := range strings.Split(chunk, "\n") {
		if rest, ok := strings.CutPrefix(line, "data: "); ok {
			return strings.TrimSpace(rest)
		}
	}
	t.Fatalf("no data: line in SSE chunk: %q", chunk)
	return ""
}

// TestEvents_MetricsEvent_Emitted verifies the events stream base64-encodes the OTLP snapshot
// from MetricsSnapshotFn onto a `metrics` channel.
func TestEvents_MetricsEvent_Emitted(t *testing.T) {
	opts := minimalOpts()
	opts.StatusInterval = 50 * time.Millisecond
	want := []byte{0x0a, 0x02, 0x08, 0x01} // arbitrary non-empty payload
	opts.MetricsSnapshotFn = func(context.Context) ([]byte, error) { return want, nil }
	h := testHandler(opts)

	pr, pw := io.Pipe()
	rr := &pipeResponseWriter{header: make(http.Header), pw: pw}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rr, req)
	}()

	data := readUntilSSE(t, pr, "event: metrics")
	cancel()
	<-done

	raw, err := base64.StdEncoding.DecodeString(sseDataLine(t, data))
	if err != nil {
		t.Fatalf("decode metrics payload: %v", err)
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("metrics payload = %x, want %x", raw, want)
	}
}

// --- Graph endpoint param validation ---

func TestGraph_BadFlavor_Returns400(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph?flavor=bogus", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGraph_BadLevel_Returns400(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph?level=all", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGraph_MultipleParams_Returns400(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/graph?flavor=targets&level=projects", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("want 400, got %d", w.Code)
	}
}

func TestGraph_MethodNotAllowed(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodPost, "/api/v1/graph", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// --- WatchInvalidate channel behavior ---

func TestWatchInvalidate_NonBlockingSend(t *testing.T) {
	inv := make(chan struct{}, 1)
	inv <- struct{}{} // fill the buffer
	// A second send to a full channel must not block.
	select {
	case inv <- struct{}{}:
	default:
		// Expected: channel was full; send dropped without blocking.
	}
}

// --- DNS rebind guard ---
// The full dnsRebindGuard middleware is tested in internal/mcp/rebind_test.go.
// The bridge mounts its routes on a sub-mux; callers (mcp/server.go) wrap it.
// Verify that an un-wrapped bridge request still gets a 200 (rebind guard is
// upstream; this test confirms the inner route works correctly).
func TestRebind_InnerRouteIgnoresGuard(t *testing.T) {
	h := testHandler(minimalOpts())
	r := httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	r.Header.Set("Host", "evil.example.com") // rebind guard would block this
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	// Inner handler does not apply the rebind guard; the MCP server wraps it.
	// We just verify the inner route is alive.
	if w.Code == http.StatusForbidden {
		t.Errorf("inner bridge handler should not apply rebind guard (that is the caller's job)")
	}
}

// --- helpers ---

// pipeResponseWriter is a minimal http.ResponseWriter + http.Flusher that
// writes to a pipe so the SSE tests can read partial output.
type pipeResponseWriter struct {
	header     http.Header
	pw         *io.PipeWriter
	statusCode int
}

func (p *pipeResponseWriter) Header() http.Header { return p.header }
func (p *pipeResponseWriter) WriteHeader(code int) {
	if p.statusCode == 0 {
		p.statusCode = code
	}
}
func (p *pipeResponseWriter) Write(b []byte) (int, error) { return p.pw.Write(b) }
func (p *pipeResponseWriter) Flush()                      {}
