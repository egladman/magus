package status

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/egladman/magus/internal/handler"
	"github.com/egladman/magus/types"
)

// statusSource is the narrow consumer contract these handlers need from the console
// service: assemble the current domain status report. It is satisfied by
// *console.Service; the handler package never imports the service concretely.
type statusSource interface {
	StatusReport(context.Context) types.StatusReport
}

// StatusHandler serves GET /api/v1/status: the same JSON as `magus status -o json`
// (types.StatusReport). The telemetry/cache/build fields come from the service's static
// base; pool and pool_error are live so the response reflects current daemon state.
type StatusHandler struct {
	handler.Base
	src   statusSource
	build types.BuildInfo
}

// NewStatusHandler returns the GET /api/v1/status handler reading from src. build stamps the
// reporting binary's identity onto every response.
func NewStatusHandler(src statusSource, build types.BuildInfo, log *slog.Logger) *StatusHandler {
	h := &StatusHandler{src: src, build: build}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *StatusHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	report := h.src.StatusReport(r.Context())
	report.BuildInfo = h.build
	body, err := json.Marshal(report)
	if err != nil {
		http.Error(w, "marshal error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(body)
}

// EventsHandler serves GET /api/v1/events as a Server-Sent Events stream.
//
// Events:
//   - event: graph,  data: {"seq": N}       -- workspace graph changed (N is monotonic)
//   - event: status, data: <base64 proto>   -- pool state changed; payload is a base64
//     magus.status.v1.Status (see EncodeStatusEvent), pushed on connect and on change.
//   - event: metrics, data: <base64 proto>  -- current metrics as base64 OTLP
//     (ExportMetricsServiceRequest); pushed on connect and on change when metrics is set.
//   - comment-line heartbeat every heartbeat interval.
//
// Clients refetch /api/v1/graph on a graph event; no diff is embedded. The graph channel is
// driven by invalidate; when it is nil only heartbeats (and status/metrics) are emitted.
// Status streaming is active only when version is non-empty; metrics only when metrics != nil.
type EventsHandler struct {
	handler.Base
	src            statusSource
	build          types.BuildInfo
	metrics        func(ctx context.Context) ([]byte, error)
	invalidate     <-chan struct{}
	heartbeat      time.Duration
	statusInterval time.Duration
}

// NewEventsHandler returns the GET /api/v1/events SSE handler. build stamps (and gates via its
// version) the status frames; metrics, when non-nil, feeds the metrics channel; invalidate
// drives the graph channel; heartbeat and statusInterval override the default 25s / 2s periods
// (zero uses the defaults).
func NewEventsHandler(src statusSource, build types.BuildInfo, metrics func(ctx context.Context) ([]byte, error), invalidate <-chan struct{}, heartbeat, statusInterval time.Duration, log *slog.Logger) *EventsHandler {
	h := &EventsHandler{
		src:            src,
		build:          build,
		metrics:        metrics,
		invalidate:     invalidate,
		heartbeat:      heartbeat,
		statusInterval: statusInterval,
	}
	h.Base = handler.New(h.serve, log)
	return h
}

func (h *EventsHandler) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx proxy buffering

	var seq atomic.Int64

	hbInterval := h.heartbeat
	if hbInterval <= 0 {
		hbInterval = 25 * time.Second
	}
	heartbeat := time.NewTicker(hbInterval)
	defer heartbeat.Stop()

	inv := h.invalidate

	// Status + metrics streaming: push an initial snapshot on connect, then poll on a shared
	// ticker and re-push each channel only when its encoded payload changes. Status is gated on
	// a version to stamp; metrics on the snapshot fn. A bare handler stays heartbeat/graph-only.
	var lastStatus string
	pushStatus := func() {
		enc, err := EncodeStatusEvent(h.src.StatusReport(r.Context()), h.build)
		if err != nil || enc == lastStatus {
			return
		}
		lastStatus = enc
		fmt.Fprintf(w, "event: status\ndata: %s\n\n", enc)
		flusher.Flush()
	}
	var lastMetrics string
	pushMetrics := func() {
		raw, err := h.metrics(r.Context())
		if err != nil || len(raw) == 0 {
			return
		}
		enc := base64.StdEncoding.EncodeToString(raw)
		if enc == lastMetrics {
			return
		}
		lastMetrics = enc
		fmt.Fprintf(w, "event: metrics\ndata: %s\n\n", enc)
		flusher.Flush()
	}

	statusOn := h.build.Version != ""
	metricsOn := h.metrics != nil
	var pollTick <-chan time.Time
	if statusOn || metricsOn {
		interval := h.statusInterval
		if interval <= 0 {
			interval = 2 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		pollTick = ticker.C
		if statusOn {
			pushStatus()
		}
		if metricsOn {
			pushMetrics()
		}
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			fmt.Fprintf(w, ": heartbeat\n\n")
			flusher.Flush()
		case <-pollTick:
			if statusOn {
				pushStatus()
			}
			if metricsOn {
				pushMetrics()
			}
		case _, ok := <-inv:
			if !ok {
				// Channel closed; keep sending heartbeats but no more graph events.
				inv = nil
				continue
			}
			n := seq.Add(1)
			fmt.Fprintf(w, "event: graph\ndata: {\"seq\": %d}\n\n", n)
			flusher.Flush()
		}
	}
}
