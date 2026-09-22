package magus

import (
	"io"
	"time"

	"github.com/egladman/magus/internal/report"
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

// RecordDiagnostic appends one coded diagnostic raised ABOUT a run rather than by an
// executed target, so a consumer reads it as the same run.diagnostic event the engine's
// own sink emits. unit is a project path or "<project>:<target>".
func (rw *ReportWriter) RecordDiagnostic(unit string, code types.DiagnosticCode, message string) error {
	return report.Record(rw.w, report.DiagnosticEmitted{Unit: unit, Code: string(code), Message: message})
}

// RecordRunScope appends the run's project-selection header (the "projects: ..."
// line a text run prints) as a typed event, for a caller rendering its own header
// in place of the cache logger's prose (see [Magus.LogScope]).
func (rw *ReportWriter) RecordRunScope(label, source string) error {
	return report.Record(rw.w, report.RunScope{Label: label, Source: source})
}

// RecordRunCharms appends the active-charm header (see [Magus.LogCharms]) as a typed event.
func (rw *ReportWriter) RecordRunCharms(charms string) error {
	return report.Record(rw.w, report.RunCharms{Charms: charms})
}

// RecordRunCache appends the cache-tier header (see [Magus.LogCache] and
// [Magus.CacheDescription]) as a typed event.
func (rw *ReportWriter) RecordRunCache(tier, mode string) error {
	return report.Record(rw.w, report.RunCache{Tier: tier, Mode: mode})
}

// RecordRunBase appends the affected-set base header (see [Magus.LogBase]) as a typed event.
func (rw *ReportWriter) RecordRunBase(base, vcs string) error {
	return report.Record(rw.w, report.RunBase{Base: base, VCS: vcs})
}

// RecordNotice appends a free-form advisory line (a hint, warning, or one-time
// banner) that has no dedicated event type of its own. level is "info" or "warn";
// code is the diagnostic code (e.g. an MGS####) when the notice carries one.
func (rw *ReportWriter) RecordNotice(level, code, msg string) error {
	return report.Record(rw.w, report.Notice{Level: level, Code: code, Msg: msg})
}

// WithReport attaches rw to receive one JSONL event per executed target.
// Mutually exclusive with [WithReportWriter].
func WithReport(rw *ReportWriter) RunOption {
	return func(o *run) { o.Report = rw.w }
}
