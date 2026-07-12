package otlp

import (
	"context"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/egladman/magus/internal/observability"
)

// Collector is a narrow, proto-free accessor over an in-process metrics ManualReader.
// It exists so a caller (the daemon's dashboard aggregation) can read raw OTel
// metricdata - histogram buckets and counters - without a network hop and without this
// package importing the generated dashboard proto. It is deliberately NOT a method on the
// fat Provider interface: only the local-collect daemon path needs it, so it is reached by
// a concrete type assertion via CollectorFrom rather than by widening every Provider.
type Collector struct {
	reader *sdkmetric.ManualReader
}

// Collect gathers the current metricdata from the underlying ManualReader. The
// ManualReader's default temporality is cumulative, so counters and histogram bucket
// counts are totals since process start.
func (c *Collector) Collect(ctx context.Context) (metricdata.ResourceMetrics, error) {
	var rm metricdata.ResourceMetrics
	err := c.reader.Collect(ctx, &rm)
	return rm, err
}

// CollectorFrom returns a Collector over p's in-process ManualReader, or (nil, false) when
// p is not an otelProvider that collects locally (the disabled CLI no-op, or an
// export-only provider built without a manual reader). Callers gate the dashboard mount on
// the bool.
func CollectorFrom(p observability.Provider) (*Collector, bool) {
	op, ok := p.(*otelProvider)
	if !ok || op.manual == nil {
		return nil, false
	}
	return &Collector{reader: op.manual}, true
}
