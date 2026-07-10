package console

import (
	"strings"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/journal"
)

// StitchDisplayEvents assembles a display journal for the viewer: the verbatim output blob split
// into per-line events (splitOutputLines), each stamped with the run's identity and timestamp,
// then a trailing result event carrying its outcome. The stdout/stderr split and per-line
// timestamps the live capture had are gone from the interleaved blob, so every line is stdout at
// the run's timestamp and the result shares it - a render aid, never a source of truth. The
// verbatim blob (cache.OutputStore.ByRef) stays the source of truth; these events exist only so
// the handler can map them onto the wire proto the viewer renders.
func StitchDisplayEvents(output []byte, d cache.OutputDescriptor) []journal.Event {
	events := splitOutputLines(output)
	for i := range events {
		events[i].Project = d.Project
		events[i].Target = d.Target
		events[i].Inv = d.Inv
		events[i].Ts = d.TimestampMs
	}
	status := journal.StatusPass
	if d.Failed {
		status = journal.StatusFail
	}
	return append(events, journal.Event{
		Kind: journal.KindResult, Project: d.Project, Target: d.Target,
		Status: status, Ref: d.Ref, Inv: d.Inv, DurMs: d.DurationMs,
		Ts: d.TimestampMs, Text: d.ErrMsg,
	})
}

// splitOutputLines splits a verbatim output blob into one stdout event per line (newline
// stripped). It is the display-only inverse of the store's verbatim write: the per-line
// stdout/stderr split and timestamps the live capture had are gone, so this is a rendering aid,
// never a source of truth. Empty input yields no events.
func splitOutputLines(output []byte) []journal.Event {
	if len(output) == 0 {
		return nil
	}
	text := strings.TrimSuffix(string(output), "\n")
	var evs []journal.Event
	for _, line := range strings.Split(text, "\n") {
		evs = append(evs, journal.Event{Kind: journal.KindOutput, Stream: journal.StreamStdout, Text: line})
	}
	return evs
}
