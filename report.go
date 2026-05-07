package magus

import (
	"io"
	"time"

	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/internal/wire"
	"github.com/egladman/magus/types"
)

// ReportWriter is an async JSONL event sink for run telemetry.
// Create one with [NewReportWriter], pass it to Run via [WithReport], and close
// it after the run completes.
type ReportWriter struct{ w *report.Writer }

// NewReportWriter constructs a ReportWriter that writes JSONL events to dst.
// filter is an optional list of event-type terms; an empty or nil slice disables
// filtering (all events pass through).
func NewReportWriter(dst io.Writer, filter []string) (*ReportWriter, error) {
	var opts []report.Option
	if len(filter) > 0 {
		f, err := report.ParseFilter(filter)
		if err != nil {
			return nil, err
		}
		if f != nil {
			opts = append(opts, report.WithFilter(f))
		}
	}
	return &ReportWriter{w: report.NewWriter(dst, opts...)}, nil
}

// Close flushes and closes the writer. Must be called after the run finishes.
func (rw *ReportWriter) Close() error { return rw.w.Close() }

// GraphObserver returns an [types.Observer] that records graph-traversal events
// to this writer. Pass the result to [Magus.SetGraphObserver].
func (rw *ReportWriter) GraphObserver() types.Observer { return report.GraphObserver(rw.w) }

// RecordShardTotal appends a shard-level wall-clock observation (job start → last
// project end) for adaptive CI forecast. Call after the run completes when running
// in a CI matrix; shardID and nShards come from --shard / --n-shards.
func (rw *ReportWriter) RecordShardTotal(shardID string, nShards int, duration time.Duration) error {
	return report.Record(rw.w, report.ShardTotal{
		Shard:      shardID,
		NShards:    nShards,
		DurationMs: duration.Milliseconds(),
	})
}

// WithReport attaches rw to receive one JSONL event per executed target.
// Mutually exclusive with [WithReportWriter].
func WithReport(rw *ReportWriter) RunOption {
	return wire.WithReport(rw.w)
}
