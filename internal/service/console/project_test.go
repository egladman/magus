package console

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/journal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStitchDisplayEvents pins the viewer display projection: the verbatim blob becomes one
// stdout event per line, each stamped with the run's identity/timestamp, then a trailing result
// event carrying the outcome. This moved out of the cache store (a storage concern) into the
// presentation layer, so it is asserted here against a hand-built descriptor + raw bytes.
func TestStitchDisplayEvents(t *testing.T) {
	d := cache.OutputDescriptor{
		Ref: "ref1a2b3c", Project: "svc/api", Target: "test", Inv: "inv123",
		Failed: true, ErrMsg: "boom", TimestampMs: 1_700_000_000_000, DurationMs: 1200,
	}
	events := StitchDisplayEvents([]byte("lint: undefined symbol foo\n"), d)

	require.Len(t, events, 2, "one output event + one result event")
	assert.Equal(t, journal.KindOutput, events[0].Kind)
	assert.Equal(t, journal.StreamStdout, events[0].Stream)
	assert.Equal(t, "lint: undefined symbol foo", events[0].Text)
	assert.Equal(t, "svc/api", events[0].Project)
	assert.Equal(t, "test", events[0].Target)
	assert.Equal(t, "inv123", events[0].Inv)
	assert.Equal(t, int64(1_700_000_000_000), events[0].Ts)

	assert.Equal(t, journal.Event{
		Kind: journal.KindResult, Project: "svc/api", Target: "test",
		Status: journal.StatusFail, Ref: "ref1a2b3c", Inv: "inv123", DurMs: 1200,
		Ts: 1_700_000_000_000, Text: "boom",
	}, events[1])
}

// TestStitchDisplayEventsPass covers the passing-outcome result status.
func TestStitchDisplayEventsPass(t *testing.T) {
	events := StitchDisplayEvents([]byte("build ok\n"), cache.OutputDescriptor{Project: "p", Target: "build"})
	require.Len(t, events, 2)
	assert.Equal(t, journal.StatusPass, events[1].Status)
}

// TestSplitOutputLines pins the display-only inverse of the verbatim write: one stdout event
// per line (newline stripped), empty input yields no events, and the trailing newline is not
// turned into a spurious empty line.
func TestSplitOutputLines(t *testing.T) {
	assert.Nil(t, splitOutputLines(nil), "empty input yields no events")
	assert.Nil(t, splitOutputLines([]byte("")), "empty input yields no events")

	evs := splitOutputLines([]byte("a\nb\nc\n"))
	require.Len(t, evs, 3)
	for i, want := range []string{"a", "b", "c"} {
		assert.Equal(t, journal.KindOutput, evs[i].Kind)
		assert.Equal(t, journal.StreamStdout, evs[i].Stream)
		assert.Equal(t, want, evs[i].Text)
	}

	// No trailing newline: the final partial line is still one event.
	evs = splitOutputLines([]byte("done"))
	require.Len(t, evs, 1)
	assert.Equal(t, "done", evs[0].Text)
}

// benchRaw builds a realistic target log: n lines (~80 bytes each) as verbatim bytes.
func benchRaw(n int) []byte {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "[%04d] go: downloading example.com/some/module v1.%d.0 (cached, verified)\n", i, i%9)
	}
	return []byte(b.String())
}

// BenchmarkSplitOutputLines measures the display projection (blob -> per-line events) the viewer
// path (StitchDisplayEvents) performs. Moved here with the projection out of the cache store.
func BenchmarkSplitOutputLines(b *testing.B) {
	raw := benchRaw(200)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		_ = splitOutputLines(raw)
	}
}
