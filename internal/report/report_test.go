package report

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egladman/magus/internal/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// failWriter returns an error on every Write. Used to verify that the
// drain goroutine surfaces I/O errors via Stats.LastErr.
type failWriter struct{}

func (failWriter) Write(_ []byte) (int, error) { return 0, fmt.Errorf("disk full") }

// TestRecordSurfacesDrainErrorViaStats verifies that an io.Writer
// failure on the drain goroutine ends up in Stats.LastErr after Close.
func TestRecordSurfacesDrainErrorViaStats(t *testing.T) {
	t.Parallel()
	w := NewWriter(failWriter{}, WithBlockOnFull())
	require.NoError(t, Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "p", Target: "build"}))
	require.NoError(t, Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "p", Target: "test"}))
	_ = w.Close()
	st := w.Stats()
	assert.Error(t, st.LastErr, "Stats.LastErr should be non-nil after Close on failing writer")
}

// TestSchemaFieldOnEveryLine asserts that every emitted JSONL line
// carries "schema":3 as the first key.
func TestSchemaFieldOnEveryLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull())
	for i := 0; i < 5; i++ {
		require.NoError(t, Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "p", Target: "build"}))
	}
	require.NoError(t, w.Close())
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		line := sc.Bytes()
		var head struct {
			Schema int    `json:"schema"`
			Type   string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(line, &head), "unmarshal %q", line)
		assert.Equal(t, Schema, head.Schema, "schema on line %q", line)
		assert.Equal(t, TypeTargetResult, head.Type)
	}
}

// TestRoundTripAllTypes writes one of every registered event type and
// reads them back. Asserts schema + type + body fidelity.
func TestRoundTripAllTypes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	w, err := OpenWriter(path, WithBlockOnFull())
	require.NoError(t, err)
	events := []any{
		TargetResult{Status: "ok", CacheHit: true, Project: "svc-a", Target: "build", DurationMs: 342},
		TargetResult{Status: "ok", Project: "svc-b", Target: "build", DurationMs: 12},
		TargetResult{Project: "svc-c", Target: "test", Status: "failed", DurationMs: 1053, Error: "exit status 1"},
		GraphBuild{Nodes: 120, DurationMs: 8},
		GraphQuery{Op: "affected", Nodes: 120, Seeds: 3, Strategy: "reverse", ResultCount: 12, DurationMs: 4},
		GraphError{Op: "build", Message: "cycle"},
		VolatilityCall{Project: "svc-a", Target: "test", Status: "retried_volatile", Attempts: 2, RetryReason: "predicted_volatile"},
		ShardSetup{Shard: "0", NShards: 4, DurationMs: 230},
		ShardTotal{Shard: "0", NShards: 4, DurationMs: 78321},
	}
	for _, e := range events {
		require.NoError(t, recordAny(w, e), "Record %T", e)
	}
	require.NoError(t, w.Close())

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	wantTypes := []string{
		TypeTargetResult, TypeTargetResult, TypeTargetResult,
		TypeGraphBuild, TypeGraphQuery, TypeGraphError,
		TypeVolatility, TypeShardSetup, TypeShardTotal,
	}
	sc := bufio.NewScanner(f)
	for i := 0; sc.Scan(); i++ {
		require.Less(t, i, len(wantTypes), "got more lines than expected")
		var head struct {
			Schema int    `json:"schema"`
			Type   string `json:"type"`
		}
		require.NoError(t, json.Unmarshal(sc.Bytes(), &head), "line %d unmarshal: %q", i, sc.Bytes())
		assert.Equal(t, Schema, head.Schema, "line %d schema", i)
		assert.Equal(t, wantTypes[i], head.Type, "line %d type", i)
	}
}

func recordAny(w *Writer, e any) error {
	return Record(w, e)
}

func TestAppend(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	for i := 0; i < 2; i++ {
		w, err := OpenWriter(path, WithBlockOnFull())
		require.NoError(t, err, "Open round %d", i)
		require.NoError(t, Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "svc-a", Target: "build", DurationMs: 10}), "Record round %d", i)
		require.NoError(t, w.Close(), "Close round %d", i)
	}
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	var count int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		count++
	}
	assert.Equal(t, 2, count, "lines after two opens")
}

// TestConcurrentWrites spawns N goroutines that all Record. With
// blocking enabled, every send lands; counters match.
func TestConcurrentWrites(t *testing.T) {
	t.Parallel()
	const goroutines = 64
	const perG = 16
	path := filepath.Join(t.TempDir(), "run.jsonl")
	w, err := OpenWriter(path, WithBlockOnFull())
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				_ = Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "svc-a", Target: "build", DurationMs: 1})
			}
		}()
	}
	wg.Wait()
	require.NoError(t, w.Close())

	st := w.Stats()
	want := uint64(goroutines * perG)
	assert.Equal(t, want, st.Recorded)
	assert.Equal(t, want, st.Flushed)
	assert.Zero(t, st.Dropped, "Dropped should be 0 under WithBlockOnFull")

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()
	var count int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		assert.NoError(t, json.Unmarshal(sc.Bytes(), &m), "corrupt line %d: %q", count+1, sc.Bytes())
		count++
	}
	assert.Equal(t, want, uint64(count))
}

// TestDropOnFullPolicy verifies the default policy: when the queue is
// full, excess events increment Dropped.
func TestDropOnFullPolicy(t *testing.T) {
	t.Parallel()
	// Slow-writer pattern: a writer that blocks on every Write so the
	// drain goroutine is parked and the channel fills.
	bw := &blockingWriter{ch: make(chan struct{})}
	w := NewWriter(bw, WithQueueSize(4))
	const total = 200
	for i := 0; i < total; i++ {
		_ = Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "p", Target: "t"})
	}
	// Release the drain goroutine so Close can complete.
	close(bw.ch)
	_ = w.Close()
	st := w.Stats()
	assert.Equal(t, uint64(total), st.Recorded+st.Dropped, "Recorded + Dropped should account for every event")
	assert.NotZero(t, st.Dropped, "want some drops with a 4-slot queue and a stalled writer")
}

// blockingWriter blocks every Write until ch is closed.
type blockingWriter struct {
	ch     chan struct{}
	closed atomic.Bool
}

func (b *blockingWriter) Write(p []byte) (int, error) {
	if b.closed.Load() {
		return len(p), nil
	}
	<-b.ch
	b.closed.Store(true)
	return len(p), nil
}

// TestFilterIncludeOnly admits only listed types.
func TestFilterIncludeOnly(t *testing.T) {
	t.Parallel()
	f, err := ParseFilter([]string{"+target.result"})
	require.NoError(t, err)
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull(), WithFilter(f))
	require.NoError(t, Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "p", Target: "t"}))
	require.NoError(t, Record(w, TargetResult{Status: "ok", Project: "p", Target: "t"}))
	require.NoError(t, Record(w, GraphBuild{Nodes: 10}))
	require.NoError(t, w.Close())
	st := w.Stats()
	// hit and miss are both target.result now, so the include filter admits both.
	assert.Equal(t, uint64(2), st.Recorded)
	assert.Equal(t, uint64(1), st.Filtered)
}

// TestFilterExcludeOnly drops listed types, admits the rest.
func TestFilterExcludeOnly(t *testing.T) {
	t.Parallel()
	f, err := ParseFilter([]string{"-graph.build", "-graph.query"})
	require.NoError(t, err)
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull(), WithFilter(f))
	require.NoError(t, Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "p", Target: "t"}))
	require.NoError(t, Record(w, GraphBuild{Nodes: 10}))
	require.NoError(t, Record(w, GraphQuery{Op: "x"}))
	require.NoError(t, Record(w, GraphError{Message: "boom"}))
	require.NoError(t, w.Close())
	st := w.Stats()
	assert.Equal(t, uint64(2), st.Recorded, "want 2 (cache.hit + graph.error)")
	assert.Equal(t, uint64(2), st.Filtered, "want 2 (graph.build + graph.query)")
}

func TestFilterEmptyAdmitsAll(t *testing.T) {
	t.Parallel()
	f, err := ParseFilter(nil)
	require.NoError(t, err)
	assert.Nil(t, f, "ParseFilter(nil) should return nil filter")
	f2, err := ParseFilter([]string{"", " ", "\t"})
	require.NoError(t, err)
	assert.Nil(t, f2, "ParseFilter(blanks) should return nil filter")
}

func TestFilterMalformedReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ParseFilter([]string{"+", "-"})
	assert.Error(t, err, "ParseFilter(only-malformed) should return an error")
}

func TestRecordUnregisteredType(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull())
	err := Record(w, struct{ X int }{X: 1})
	assert.Error(t, err, "Record on unregistered type should return an error")
	_ = w.Close()
}

func TestRecordNilWriterIsNoop(t *testing.T) {
	t.Parallel()
	assert.NoError(t, Record(nil, TargetResult{Status: "ok", CacheHit: true, Project: "p"}), "Record on nil Writer should be a no-op")
}

func TestCacheRunOptions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	w, err := OpenWriter(path, WithBlockOnFull())
	require.NoError(t, err)

	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	src := filepath.Join(root, "pkg", "main.go")
	require.NoError(t, os.MkdirAll(filepath.Dir(src), 0o755))
	require.NoError(t, os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644))
	out := filepath.Join(root, "pkg", "out.bin")
	spec := cache.Step{
		ProjectPath:   "pkg",
		Sources:       []string{"pkg/*.go"},
		Outputs:       []string{"pkg/out.bin"},
		WorkspaceRoot: root,
		Target:        "build",
	}

	opts := RunOptions(w)

	c, err := cache.Open(cdir, cache.WithMutable(true))
	require.NoError(t, err)
	ctx := t.Context()
	_, err = c.Run(ctx, spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("bin"), 0o755)
	}, opts...)
	require.NoError(t, err, "run (miss)")

	c2, err := cache.Open(cdir, cache.WithMutable(true))
	require.NoError(t, err)
	_, err = c2.Run(ctx, spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("bin"), 0o755)
	}, opts...)
	require.NoError(t, err, "run (hit)")

	require.NoError(t, w.Close())

	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	var results []TargetResult
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev TargetResult
		require.NoError(t, json.Unmarshal(sc.Bytes(), &ev), "unmarshal")
		results = append(results, ev)
	}
	require.Len(t, results, 2, "want 2 events: %v", results)
	// Both are target.result; the first run is a miss, the replay a hit.
	assert.False(t, results[0].CacheHit, "first event cache_hit should be false (miss)")
	assert.True(t, results[1].CacheHit, "second event cache_hit should be true (hit)")
}

func TestWriterContextRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	w, err := OpenWriter(path)
	require.NoError(t, err)
	defer w.Close()
	ctx := WithWriter(context.Background(), w)
	got := WriterFromContext(ctx)
	assert.Same(t, w, got, "FromContext should return the stored writer")
	assert.Nil(t, WriterFromContext(context.Background()), "FromContext on plain ctx should return nil")
}

// TestCloseIsIdempotent guards against double-close panic on the channel.
func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	require.NoError(t, w.Close(), "first Close")
	require.NoError(t, w.Close(), "second Close")
}

// TestConcurrentRecordAndClose verifies that a concurrent Record and Close
// cannot panic. Previously Close closed w.ch directly; any Record blocked on
// a channel send in WithBlockOnFull mode would panic with "send on closed
// channel". The fix uses a separate quit channel so close is never called
// on a channel that producers can still send to.
func TestConcurrentRecordAndClose(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		for range 50 {
			var buf bytes.Buffer
			w := NewWriter(&buf, WithBlockOnFull())
			var wg sync.WaitGroup
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for range 32 {
						_ = Record(w, TargetResult{Status: "ok", CacheHit: true, Project: "p", Target: "t"})
					}
				}()
			}
			// Close races with the goroutines above; must not panic.
			_ = w.Close()
			wg.Wait()
		}
	})
}
