package file

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type event struct {
	Writer int `json:"writer"`
	N      int `json:"n"`
}

func events(writer, from, to int) []event {
	var out []event
	for n := from; n < to; n++ {
		out = append(out, event{Writer: writer, N: n})
	}
	return out
}

func appendEach(t *testing.T, l Log[event], entries []event) {
	t.Helper()
	for _, e := range entries {
		require.NoError(t, l.Append(t.Context(), e))
	}
}

// A log within Keep plus CompactSlack is left as it is; past it, a compaction keeps the
// newest Keep entries in file order.
func TestLogCompactKeepsTheNewest(t *testing.T) {
	t.Parallel()

	l := Log[event]{Path: filepath.Join(t.TempDir(), "dir", "events.jsonl"), Keep: 3, SkipSync: true}
	appendEach(t, l, events(0, 0, 3+CompactSlack))
	kept, err := l.Compact(t.Context())
	require.NoError(t, err)
	assert.Equal(t, events(0, 0, 3+CompactSlack), kept, "not due yet")

	require.NoError(t, l.Append(t.Context(), event{N: 3 + CompactSlack}))
	kept, err = l.Compact(t.Context())
	require.NoError(t, err)
	want := events(0, 1+CompactSlack, 4+CompactSlack)
	assert.Equal(t, want, kept)
	got, err := l.Load()
	require.NoError(t, err)
	assert.Equal(t, want, got)
}

// Select replaces newest-Keep and sees every complete entry.
func TestLogCompactUsesSelect(t *testing.T) {
	t.Parallel()

	evens := func(all []event, _ int) []event {
		var out []event
		for _, e := range all {
			if e.N%2 == 0 {
				out = append(out, e)
			}
		}
		return out
	}
	l := Log[event]{Path: filepath.Join(t.TempDir(), "events.jsonl"), Keep: 1, Select: evens}
	appendEach(t, l, events(0, 0, 2+CompactSlack))
	kept, err := l.Compact(t.Context())
	require.NoError(t, err)
	assert.Equal(t, evens(events(0, 0, 2+CompactSlack), 1), kept)
}

// A log that does not say how many entries it keeps refuses to compact rather than pick a
// number, and leaves the file alone.
func TestLogCompactWithoutKeepFails(t *testing.T) {
	t.Parallel()

	l := Log[event]{Path: filepath.Join(t.TempDir(), "events.jsonl")}
	require.NoError(t, l.Append(t.Context(), event{N: 1}))
	_, err := l.Compact(t.Context())
	require.ErrorContains(t, err, "set Keep")
	got, err := l.Load()
	require.NoError(t, err)
	assert.Equal(t, []event{{N: 1}}, got)
}

// A line no writer finished is left unread, and the next append rewrites the file without
// it rather than gluing its own line onto it.
func TestLogAppendRewritesPastAnUnfinishedLine(t *testing.T) {
	t.Parallel()

	l := Log[event]{Path: filepath.Join(t.TempDir(), "events.jsonl")}
	require.NoError(t, os.WriteFile(l.Path, []byte(`{"writer":0,"n":1}`+"\n"+`{"writer":0,"n"`), 0o644))
	got, err := l.Load()
	require.NoError(t, err)
	assert.Equal(t, []event{{N: 1}}, got)

	require.NoError(t, l.Append(t.Context(), event{N: 2}))
	raw, err := os.ReadFile(l.Path)
	require.NoError(t, err)
	assert.Equal(t, `{"writer":0,"n":1}`+"\n"+`{"writer":0,"n":2}`+"\n", string(raw))
}

// A complete line that does not decode is corruption, not a torn append: the load names it.
func TestLogNamesTheLineThatDoesNotDecode(t *testing.T) {
	t.Parallel()

	l := Log[event]{Path: filepath.Join(t.TempDir(), "events.jsonl")}
	require.NoError(t, os.WriteFile(l.Path, []byte(`{"n":1}`+"\n"+`{"n":`+"\n"), 0o644))
	_, err := l.Load()
	require.ErrorContains(t, err, l.Path+":2: ")
}

// A compaction that died after writing its temp file and before the rename leaves the old
// log, and the temp is invisible to readers and to the next writer.
func TestLogCrashBeforeTheRenameLeavesTheOldLog(t *testing.T) {
	t.Parallel()

	l := Log[event]{Path: filepath.Join(t.TempDir(), "events.jsonl"), Keep: 1}
	appendEach(t, l, events(0, 0, 2))
	dead := filepath.Join(filepath.Dir(l.Path), fmt.Sprintf(".%s.tmp.12345", filepath.Base(l.Path)))
	require.NoError(t, os.WriteFile(dead, []byte(`{"writer":0,"n":1}`+"\n"), 0o644))

	got, err := l.Load()
	require.NoError(t, err)
	assert.Equal(t, events(0, 0, 2), got)
	require.NoError(t, l.Append(t.Context(), event{N: 2}))
	got, err = l.Load()
	require.NoError(t, err)
	assert.Equal(t, events(0, 0, 3), got)
}

// Appenders in step with compactions lose no line: the compaction's Select keeps every
// entry, so a line missing at the end is one a rewrite dropped from a stale read. Each
// goroutine opens its own lock, as separate processes would.
//
// Compaction runs once per append. A loop retakes the lock as it lets go.
// Appenders polling every lockRetryDelay then starve past LockWait under load.
func TestLogConcurrentAppendersLoseNothingAcrossCompactions(t *testing.T) {
	t.Parallel()

	const writers, each = 8, 40
	path := filepath.Join(t.TempDir(), "events.jsonl")
	keepAll := func(all []event, _ int) []event { return all }
	kick := make(chan struct{}, 1)
	var compactions sync.WaitGroup
	compactions.Go(func() {
		l := Log[event]{Path: path, Keep: 1, Select: keepAll}
		for range kick {
			_, err := l.Compact(t.Context())
			assert.NoError(t, err)
		}
	})
	landed := make([][]event, writers)
	var appends sync.WaitGroup
	for w := range writers {
		appends.Go(func() {
			l := Log[event]{Path: path, SkipSync: true}
			for _, e := range events(w, 0, each) {
				if assert.NoError(t, l.Append(t.Context(), e)) {
					landed[w] = append(landed[w], e)
				}
				select {
				case kick <- struct{}{}:
				default:
				}
			}
		})
	}
	appends.Wait()
	close(kick)
	compactions.Wait()

	got, err := Log[event]{Path: path}.Load()
	require.NoError(t, err)
	byWriter := make([][]event, writers)
	for _, e := range got {
		byWriter[e.Writer] = append(byWriter[e.Writer], e)
	}
	for w := range writers {
		assert.Equal(t, landed[w], byWriter[w], "writer %d: each landed append once, in order", w)
	}
}

// An append that cannot take the lock reports why and writes nothing.
// A writer that carries on leaves a gap, never a duplicate. A cancelled
// wait stands in for a holder past LockWait: neither runs the write.
type logAppendBlockedSuite struct {
	suite.Suite
	log Log[event]
}

func TestLogAppendBlocked(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(logAppendBlockedSuite))
}

func (s *logAppendBlockedSuite) SetupTest() {
	s.log = Log[event]{Path: filepath.Join(s.T().TempDir(), "events.jsonl")}
	s.Require().NoError(s.log.Append(s.T().Context(), event{N: 0}))
}

func (s *logAppendBlockedSuite) appendWhileHeld(e event) error {
	release := hold(s.T(), s.log.Path+".lock")
	defer release()
	ctx, cancel := context.WithCancel(s.T().Context())
	cancel()
	return s.log.Append(ctx, e)
}

func (s *logAppendBlockedSuite) load() []event {
	got, err := s.log.Load()
	s.Require().NoError(err)
	return got
}

func (s *logAppendBlockedSuite) TestFailedAppendReportsTheWait() {
	err := s.appendWhileHeld(event{N: 1})
	s.Require().ErrorIs(err, context.Canceled)
	s.ErrorContains(err, fmt.Sprintf("waiting for the lock at %s.lock", s.log.Path))
}

func (s *logAppendBlockedSuite) TestFailedAppendWritesNothing() {
	s.Require().Error(s.appendWhileHeld(event{N: 1}))
	s.Equal([]event{{N: 0}}, s.load())
}

func (s *logAppendBlockedSuite) TestAppendAfterAFailedOneLandsOnce() {
	s.Require().Error(s.appendWhileHeld(event{N: 1}))
	s.Require().NoError(s.log.Append(s.T().Context(), event{N: 2}))
	s.Equal([]event{{N: 0}, {N: 2}}, s.load())
}
