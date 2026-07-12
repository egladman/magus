package otlp

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// captureTransport is an http.RoundTripper that intercepts the OTLP export POST and keeps its
// body instead of sending it. Pairing it with an otlpmetrichttp exporter lets us reuse OTel's
// OWN metricdata->OTLP-protobuf serialization (that transform lives in the exporter's internal
// package and is not importable) to produce a standard OTLP snapshot on demand - the wire the
// /dashboard reads. Proven end-to-end in otlp_spike_test.go.
type captureTransport struct {
	mu   sync.Mutex
	body []byte
}

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.body = b
	c.mu.Unlock()
	// OTLP/HTTP success is a 200 with an (optionally empty) ExportMetricsServiceResponse.
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Header:     make(http.Header),
	}, nil
}

func (c *captureTransport) snapshot() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.body == nil {
		return nil
	}
	out := make([]byte, len(c.body))
	copy(out, c.body)
	return out
}

// newCaptureReader builds an SDK metric Reader that "exports" through a captureTransport,
// yielding standard OTLP protobuf without a network hop. The long interval means it only
// produces bytes when explicitly flushed - Snapshot drives that via MeterProvider.ForceFlush.
func newCaptureReader(ctx context.Context) (sdkmetric.Reader, *captureTransport, error) {
	capT := &captureTransport{}
	exp, err := otlpmetrichttp.New(ctx,
		otlpmetrichttp.WithEndpoint("127.0.0.1:4318"), // never dialed; the transport short-circuits
		otlpmetrichttp.WithInsecure(),
		otlpmetrichttp.WithCompression(otlpmetrichttp.NoCompression),
		otlpmetrichttp.WithHTTPClient(&http.Client{Transport: capT}),
	)
	if err != nil {
		return nil, nil, err
	}
	return sdkmetric.NewPeriodicReader(exp, sdkmetric.WithInterval(24*time.Hour)), capT, nil
}

// Snapshot returns a fresh OTLP-protobuf metrics payload (an
// otlp/collector/metrics/v1.ExportMetricsServiceRequest) for the current instrument values, or
// nil when this provider has no capturing reader. It flushes the meter provider - collecting
// the current values - then reads the bytes the capturing exporter recorded.
func (p *otelProvider) Snapshot(ctx context.Context) ([]byte, error) {
	if p.capture == nil || p.mp == nil {
		return nil, nil
	}
	if err := p.mp.ForceFlush(ctx); err != nil {
		return nil, err
	}
	return p.capture.snapshot(), nil
}
