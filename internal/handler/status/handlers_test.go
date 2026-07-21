package status

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	statusv1 "github.com/egladman/magus/proto/gen/go/magus/status/v1"
	"github.com/egladman/magus/types"
	"google.golang.org/protobuf/proto"
)

// fakeSource is a statusSource returning a canned report.
type fakeSource struct {
	report types.StatusReport
}

func (f fakeSource) StatusReport(context.Context) types.StatusReport { return f.report }

func TestEventsHandler_OptionsNoContent(t *testing.T) {
	h := NewEventsHandler(fakeSource{}, types.BuildInfo{}, nil, nil, 0, 0, nil)
	r := httptest.NewRequest(http.MethodOptions, "/api/v1/events", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNoContent {
		t.Errorf("want 204 for preflight, got %d", w.Code)
	}
}

func TestEventsHandler_MethodNotAllowed(t *testing.T) {
	h := NewEventsHandler(fakeSource{}, types.BuildInfo{}, nil, nil, 0, 0, nil)
	r := httptest.NewRequest(http.MethodPost, "/api/v1/events", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("want 405, got %d", w.Code)
	}
}

// nonFlusherWriter is an http.ResponseWriter that is deliberately NOT an http.Flusher, so the
// events handler takes its "streaming not supported" branch.
type nonFlusherWriter struct {
	header http.Header
	code   int
}

func (n *nonFlusherWriter) Header() http.Header         { return n.header }
func (n *nonFlusherWriter) Write(b []byte) (int, error) { return len(b), nil }
func (n *nonFlusherWriter) WriteHeader(code int)        { n.code = code }

func TestEventsHandler_StreamingUnsupported(t *testing.T) {
	h := NewEventsHandler(fakeSource{}, types.BuildInfo{}, nil, nil, 0, 0, nil)
	r := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	w := &nonFlusherWriter{header: make(http.Header)}
	h.ServeHTTP(w, r)
	if w.code != http.StatusInternalServerError {
		t.Errorf("want 500 when ResponseWriter is not a Flusher, got %d", w.code)
	}
}

// --- eventsHandler ---

func TestEventsHandler_HeartbeatOnly(t *testing.T) {
	h := NewEventsHandler(fakeSource{}, types.BuildInfo{}, nil, nil, 50*time.Millisecond, 0, nil)
	data := drainSSE(t, h, "/api/v1/events", ": heartbeat")
	if !strings.Contains(data, ": heartbeat") {
		t.Errorf("want heartbeat, got %q", data)
	}
}

func TestEventsHandler_GraphEvent(t *testing.T) {
	inv := make(chan struct{}, 1)
	h := NewEventsHandler(fakeSource{}, types.BuildInfo{}, nil, inv, 0, 0, nil)
	inv <- struct{}{}
	data := drainSSE(t, h, "/api/v1/events", "event: graph")
	if !strings.Contains(data, "event: graph") || !strings.Contains(data, `"seq":`) {
		t.Errorf("want graph seq event, got %q", data)
	}
}

func TestEventsHandler_StatusEvent(t *testing.T) {
	src := fakeSource{report: types.StatusReport{Pool: &types.StatusOutput{Mode: "daemon", Capacity: 4, Running: 1}}}
	h := NewEventsHandler(src, types.BuildInfo{Version: "1.2.3"}, nil, nil, 0, 50*time.Millisecond, nil)
	data := drainSSE(t, h, "/api/v1/events", "event: status")

	raw, err := base64.StdEncoding.DecodeString(sseDataLine(t, data))
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	var st statusv1.Status
	if err := proto.Unmarshal(raw, &st); err != nil {
		t.Fatalf("unmarshal Status: %v", err)
	}
	if st.GetBuild().GetVersion() != "1.2.3" {
		t.Errorf("want magus_version 1.2.3, got %q", st.GetBuild().GetVersion())
	}
	if st.Health != statusv1.Health_HEALTH_HEALTHY {
		t.Errorf("want HEALTH_HEALTHY, got %v", st.Health)
	}
	if st.Pool == nil || st.Pool.Capacity != 4 || st.Pool.Running != 1 {
		t.Errorf("want pool capacity=4 running=1, got %+v", st.Pool)
	}
}

// TestEventsHandler_InvalidateClosed confirms a closed graph channel does not end the stream:
// the handler drops the channel and keeps emitting heartbeats.
func TestEventsHandler_InvalidateClosed(t *testing.T) {
	inv := make(chan struct{})
	close(inv)
	h := NewEventsHandler(fakeSource{}, types.BuildInfo{}, nil, inv, 30*time.Millisecond, 0, nil)
	data := drainSSE(t, h, "/api/v1/events", ": heartbeat")
	if !strings.Contains(data, ": heartbeat") {
		t.Errorf("want heartbeat after invalidate close, got %q", data)
	}
}

// TestEventsHandler_MetricsError confirms a metrics-snapshot error suppresses the metrics frame
// (no event: metrics) while heartbeats still flow.
func TestEventsHandler_MetricsError(t *testing.T) {
	h := NewEventsHandler(fakeSource{}, types.BuildInfo{},
		func(context.Context) ([]byte, error) { return nil, io.ErrUnexpectedEOF },
		nil, 30*time.Millisecond, 30*time.Millisecond, nil)
	data := drainSSE(t, h, "/api/v1/events", ": heartbeat")
	if strings.Contains(data, "event: metrics") {
		t.Errorf("errored metrics snapshot must not emit a metrics frame, got %q", data)
	}
}

func TestEventsHandler_MetricsEvent(t *testing.T) {
	want := []byte{0x0a, 0x02, 0x08, 0x01}
	h := NewEventsHandler(fakeSource{}, types.BuildInfo{}, func(context.Context) ([]byte, error) { return want, nil }, nil, 0, 50*time.Millisecond, nil)
	data := drainSSE(t, h, "/api/v1/events", "event: metrics")

	raw, err := base64.StdEncoding.DecodeString(sseDataLine(t, data))
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	if !bytes.Equal(raw, want) {
		t.Errorf("metrics payload = %x, want %x", raw, want)
	}
}

// --- SSE test helpers ---

// drainSSE serves h against an SSE request and returns the output once it contains marker,
// then cancels the stream.
func drainSSE(t *testing.T, h http.Handler, path, marker string) string {
	t.Helper()
	pr, pw := io.Pipe()
	rr := &pipeResponseWriter{header: make(http.Header), pw: pw}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		h.ServeHTTP(rr, req)
	}()

	var sb strings.Builder
	buf := make([]byte, 512)
	for i := 0; i < 64; i++ {
		n, err := pr.Read(buf)
		sb.Write(buf[:n])
		if strings.Contains(sb.String(), marker) {
			break
		}
		if err != nil {
			break
		}
	}
	cancel()
	<-done
	if !strings.Contains(sb.String(), marker) {
		t.Fatalf("marker %q not seen in SSE output: %q", marker, sb.String())
	}
	return sb.String()
}

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

// pipeResponseWriter is a minimal http.ResponseWriter + http.Flusher writing to a pipe so
// the SSE tests can read partial output.
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
