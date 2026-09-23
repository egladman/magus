package magus

import (
	"io"
	"log/slog"
	"time"

	"github.com/egladman/magus/internal/report"
	"github.com/egladman/magus/types"
)

// ReportWriter is a JSONL event sink for run telemetry.
// Create one with [NewReportWriter], hand it to a run through [Magus.JSONLSink] and
// [WithSink], and close it after the run completes.
type ReportWriter struct{ w *report.Writer }

// NewReportWriter constructs a ReportWriter that writes JSONL events to dst.
// filter is an optional list of event-type terms; an empty or nil slice disables
// filtering (all events pass through).
//
// A record never waits in a full queue to be dropped: the producer blocks instead, so a
// failed target's result reaches dst however far the reader falls behind.
func NewReportWriter(dst io.Writer, filter []string) (*ReportWriter, error) {
	opts := []report.Option{report.WithBlockOnFull()}
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
//
// Written, not yet read: nothing ingests the shard.total JSONL line back into a
// forecast.History, so it does not (yet) feed the SetupP50Ms/AlphaMs fit described
// at forecast.DefaultSetupMs.
func (rw *ReportWriter) RecordShardTotal(shardID string, nShards int, duration time.Duration) error {
	return report.Record(rw.w, report.ShardTotal{
		Shard:      shardID,
		NShards:    nShards,
		DurationMs: duration.Milliseconds(),
	})
}

// RecordNotice appends a free-form advisory line (a hint, warning, or one-time
// banner) that has no dedicated event type of its own. code is the diagnostic code
// (e.g. an MGS####) when the notice carries one; message does not repeat it.
func (rw *ReportWriter) RecordNotice(level slog.Level, code types.DiagnosticCode, message string) error {
	return report.Record(rw.w, report.Notice{Level: level, Code: string(code), Message: message})
}
