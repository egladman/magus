package eventstream

import (
	"bytes"
	"strings"
	"sync"
	"testing"

	"github.com/egladman/magus/internal/journal"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromJournalMapsLifecycleAndResults(t *testing.T) {
	cases := []struct {
		name string
		in   journal.Event
		want types.StreamEvent
	}{
		{
			name: "started carries command lineage",
			in: journal.Event{
				Ts: 100, Inv: "inv1", Kind: journal.KindStarted, MagusVersion: "0.5.0",
				Command: &journal.Command{Arguments: []string{"run", "build"}, Trigger: journal.TriggerRun},
			},
			want: types.StreamEvent{Ts: 100, Workspace: "/repo", Inv: "inv1", Body: types.StreamRun{
				Phase: "started", Command: []string{"run", "build"}, Trigger: "run", MagusVersion: "0.5.0",
			}},
		},
		{
			name: "finished carries the outcome",
			in:   journal.Event{Ts: 200, Inv: "inv1", Kind: journal.KindFinished, Status: journal.StatusFail},
			want: types.StreamEvent{Ts: 200, Workspace: "/repo", Inv: "inv1", Body: types.StreamRun{
				Phase: "finished", Status: "fail",
			}},
		},
		{
			name: "result carries the fetchable ref",
			in: journal.Event{
				Ts: 150, Inv: "inv1", Kind: journal.KindResult, Project: "api", Target: "test",
				Status: journal.StatusPass, Ref: "out_abc", DurMs: 1200,
			},
			want: types.StreamEvent{Ts: 150, Workspace: "/repo", Inv: "inv1", Body: types.StreamTarget{
				Project: "api", Target: "test", Status: "ok", CacheHit: false, Ref: "out_abc", DurationMs: 1200,
			}},
		},
		{
			name: "output carries the stream it came from",
			in: journal.Event{
				Ts: 160, Inv: "inv1", Kind: journal.KindOutput, Project: "api", Target: "test",
				Stream: journal.StreamStderr, Text: "FAIL",
			},
			want: types.StreamEvent{Ts: 160, Workspace: "/repo", Inv: "inv1", Body: types.StreamOutput{
				Project: "api", Target: "test", Stream: "stderr", Text: "FAIL",
			}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := FromJournal("/repo", tc.in)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestFromJournalSplitsCachedFromSucceeded is the point of the adapter: the
// journal conflates "succeeded" and "ran" into one three-valued status, and a
// subscriber asks those separately.
func TestFromJournalSplitsCachedFromSucceeded(t *testing.T) {
	got, ok := FromJournal("/repo", journal.Event{Kind: journal.KindResult, Status: journal.StatusCached})
	require.True(t, ok)
	assert.Equal(t, types.StreamTarget{Status: "ok", CacheHit: true}, got.Body)
}

// TestFromJournalUnknownStatusIsNotGreen: a status magus grows later must not
// present as a silent pass to every existing subscriber.
func TestFromJournalUnknownStatusIsNotGreen(t *testing.T) {
	got, ok := FromJournal("/repo", journal.Event{Kind: journal.KindResult, Status: "quarantined"})
	require.True(t, ok)
	assert.Equal(t, types.StreamTarget{Status: "failed"}, got.Body)
}

// TestFromJournalSkipsKindsWithNoStreamEquivalent: skipping is the designed
// behavior, so a kind gaining a mapping by accident should fail here.
func TestFromJournalSkipsKindsWithNoStreamEquivalent(t *testing.T) {
	for _, kind := range []string{journal.KindExec, journal.KindScope, journal.KindWarn, journal.KindSecret} {
		t.Run(kind, func(t *testing.T) {
			_, ok := FromJournal("/repo", journal.Event{Kind: kind, Text: "x"})
			assert.False(t, ok)
		})
	}
}

func TestWriterEmitsOneLinePerEvent(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, types.StreamFilter{})
	require.NoError(t, w.Emit(types.StreamEvent{Ts: 1, Body: types.StreamRun{Phase: "started"}}))
	require.NoError(t, w.Emit(types.StreamEvent{Ts: 2, Body: types.StreamRun{Phase: "finished", Status: "pass"}}))
	require.NoError(t, w.Close())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 2)
	for _, line := range lines {
		_, err := types.DecodeStreamEvent([]byte(line))
		require.NoError(t, err, "line %q", line)
	}
}

// TestWriterDropsFilteredTypes: the default filter excludes target.output, and
// the Writer is where that promise is kept for every transport.
func TestWriterDropsFilteredTypes(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, types.StreamFilter{})
	require.NoError(t, w.Emit(types.StreamEvent{Ts: 1, Body: types.StreamOutput{Stream: "stdout", Text: "noisy"}}))
	require.NoError(t, w.Close())
	assert.Empty(t, buf.String())
}

// TestWriterIsConcurrencySafe: a run fans out one goroutine per project, so an
// unsynchronized write would interleave two objects on one line and every line
// after it would fail to parse.
func TestWriterIsConcurrencySafe(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, types.StreamFilter{})

	const writers, each = 8, 50
	var wg sync.WaitGroup
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				_ = w.Emit(types.StreamEvent{Ts: 1, Body: types.StreamTarget{Project: "p", Target: "build", Status: "ok"}})
			}
		}()
	}
	wg.Wait()
	require.NoError(t, w.Close())

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, writers*each)
	for _, line := range lines {
		_, err := types.DecodeStreamEvent([]byte(line))
		require.NoError(t, err, "interleaved line %q", line)
	}
}

// TestFromJournalCarriesTheFailureReason pins the error text onto the stream. The
// journal puts a failed target's run error in Text, and a subscriber told only
// "failed" has to go fetch the log to learn anything at all.
func TestFromJournalCarriesTheFailureReason(t *testing.T) {
	got, ok := FromJournal("/repo", journal.Event{
		Kind: journal.KindResult, Project: "api", Target: "build",
		Status: journal.StatusFail, Text: "exit status 2",
	})
	require.True(t, ok)
	body, ok := got.Body.(types.StreamTarget)
	require.True(t, ok)
	assert.Equal(t, "failed", body.Status)
	assert.Equal(t, "exit status 2", body.Error)
}
