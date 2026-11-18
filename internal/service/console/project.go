package console

import (
	"strings"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/libs/safetyscissors/journal"
)

// StitchDisplayEvents assembles a display journal for the viewer: the verbatim output blob split
// into per-line events (splitOutputLines), each stamped with the run's identity and timestamp,
// then a trailing result event carrying its outcome. The stdout/stderr split and per-line
// timestamps the live capture had are gone from the interleaved blob, so every line is stdout at
// the run's timestamp and the result shares it: a render aid, never a source of truth. The
// verbatim blob (cache.OutputStore.ByRef) stays the source of truth; these events exist only so
// the handler can map them onto the wire proto the viewer renders.
func StitchDisplayEvents(output []byte, d cache.OutputDescriptor) []journal.Record {
	var at time.Time
	if d.TimestampMs != 0 {
		at = time.UnixMilli(d.TimestampMs)
	}
	var records []journal.Record
	for _, line := range splitOutputLines(output) {
		records = append(records, journal.NewRecord(at, d.Inv, journal.Output{
			Project: d.Project, Target: d.Target, Stream: journal.Stdout, Text: line,
		}))
	}
	status := journal.StatusPass
	if d.Failed {
		status = journal.StatusFail
	}
	return append(records, journal.NewRecord(at, d.Inv, journal.Run{
		Project: d.Project, Target: d.Target, Status: status, Ref: d.Ref,
		Duration: time.Duration(d.DurationMs) * time.Millisecond, Error: d.ErrMsg,
	}))
}

// splitOutputLines splits a verbatim output blob into its lines, newline
// stripped: a rendering aid, never a source of truth. Empty input yields none.
func splitOutputLines(output []byte) []string {
	if len(output) == 0 {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
}
