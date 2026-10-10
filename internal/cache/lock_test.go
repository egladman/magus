package cache

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingHandler collects the log records a wait emits, so a test can assert on what a
// reader would be told rather than on the fact that something was logged.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return recordingWith{h: h, attrs: attrs}
}

func (h *recordingHandler) WithGroup(string) slog.Handler { return h }

// recordingWith records into h with attrs ahead of each record's own, so a line
// logged through logattr.For carries its component.
type recordingWith struct {
	h     *recordingHandler
	attrs []slog.Attr
}

func (w recordingWith) Enabled(context.Context, slog.Level) bool { return true }

func (w recordingWith) Handle(ctx context.Context, r slog.Record) error {
	nr := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	nr.AddAttrs(w.attrs...)
	r.Attrs(func(a slog.Attr) bool {
		nr.AddAttrs(a)
		return true
	})
	return w.h.Handle(ctx, nr)
}

func (w recordingWith) WithAttrs(attrs []slog.Attr) slog.Handler {
	return recordingWith{h: w.h, attrs: append(slices.Clip(w.attrs), attrs...)}
}

func (w recordingWith) WithGroup(string) slog.Handler { return w }

// lines renders each record as "message key=value ..." for a plain contains assertion.
func (h *recordingHandler) lines() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.records))
	for _, r := range h.records {
		line := r.Message
		r.Attrs(func(a slog.Attr) bool {
			line += " " + a.Key + "=" + a.Value.String()
			return true
		})
		out = append(out, line)
	}
	return out
}

// captureLogs routes the default logger into a recorder for one test. Not parallel: the
// default logger is process state.
func captureLogs(t *testing.T) *recordingHandler {
	t.Helper()
	h := &recordingHandler{}
	prev := slog.Default()
	slog.SetDefault(slog.New(h))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return h
}

// withShortHeartbeat shrinks both wait cadences and the first notice for one test, for
// the reason the production comment gives: a beat nobody spends is a beat nobody covers.
func withShortHeartbeat(t *testing.T, d time.Duration) {
	t.Helper()
	withWaitTimings(t, d, d)
}

func withWaitTimings(t *testing.T, beat, firstNotice time.Duration) {
	t.Helper()
	prevLock, prevUp, prevNotice := lockWaitHeartbeat, upstreamWaitHeartbeat, waitFirstNotice
	lockWaitHeartbeat, upstreamWaitHeartbeat, waitFirstNotice = beat, beat, firstNotice
	t.Cleanup(func() { lockWaitHeartbeat, upstreamWaitHeartbeat, waitFirstNotice = prevLock, prevUp, prevNotice })
}

func TestKeyedLockWaitBeatsAndNamesTheHolder(t *testing.T) {
	withShortHeartbeat(t, 20*time.Millisecond)
	logs := captureLogs(t)

	k := newKeyedLock()
	unlock, err := k.acquireNamed(context.Background(), "hash1", ". generate", nil)
	require.NoError(t, err)

	prog := NewProgress()
	prog.at.Store(time.Now().Add(-time.Hour).UnixNano())
	ctx := ContextWithProgress(context.Background(), prog)

	var blockedOn string
	done := make(chan struct{})
	go func() {
		defer close(done)
		second, err := k.acquireNamed(ctx, "hash1", ". coverage-badge", func(holder string) func() {
			blockedOn = holder
			return func() {}
		})
		assert.NoError(t, err)
		second()
	}()

	// Long enough for several beats, so the wait is proven to keep beating rather than to
	// have beaten once on entry.
	time.Sleep(80 * time.Millisecond)
	assert.Less(t, prog.Idle(), time.Minute,
		"a wait that goes silent is a stall to the watchdog (MGS3012); this one must keep beating")
	unlock()
	<-done

	assert.Equal(t, ". generate", blockedOn, "the mark names the holder, not just the key")
	assert.Contains(t, logs.lines(),
		". coverage-badge is waiting for a cache lock held by . generate (0s so far) component=magus")
	assert.NotContains(t, logs.lines(),
		". coverage-badge is waiting for a cache lock held by . generate component=magus",
		"the moment of queueing is not news; only a wait past the first notice is")
}

// A wait shorter than the first notice says nothing, yet keeps beating: silence to the
// reader must not become silence to the watchdog.
func TestKeyedLockShortWaitBeatsWithoutANotice(t *testing.T) {
	withWaitTimings(t, 20*time.Millisecond, time.Hour)
	logs := captureLogs(t)

	k := newKeyedLock()
	unlock, err := k.acquireNamed(context.Background(), "hash1", ". generate", nil)
	require.NoError(t, err)

	prog := NewProgress()
	prog.at.Store(time.Now().Add(-time.Hour).UnixNano())
	ctx := ContextWithProgress(context.Background(), prog)

	done := make(chan struct{})
	go func() {
		defer close(done)
		second, err := k.acquireNamed(ctx, "hash1", ". coverage-badge", nil)
		assert.NoError(t, err)
		second()
	}()
	time.Sleep(80 * time.Millisecond)
	assert.Less(t, prog.Idle(), time.Minute, "the beat keeps its own cadence while the notice waits")
	unlock()
	<-done

	assert.Empty(t, logs.lines(), "a wait under the first notice has nothing to report")
}

func TestKeyedLockUncontendedNeitherBeatsNorLogs(t *testing.T) {
	withShortHeartbeat(t, 20*time.Millisecond)
	logs := captureLogs(t)

	k := newKeyedLock()
	blocked := false
	unlock, err := k.acquireNamed(context.Background(), "hash1", ". generate", func(string) func() {
		blocked = true
		return func() {}
	})
	require.NoError(t, err)
	unlock()

	assert.False(t, blocked, "nobody held the key, so nothing was waited on")
	assert.Empty(t, logs.lines(), "a lock taken without waiting has nothing to report")
}
