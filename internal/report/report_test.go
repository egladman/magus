package report

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/egladman/magus/internal/cache"
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
	if err := Record(w, CacheHit{Project: "p", Target: "build"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if err := Record(w, CacheHit{Project: "p", Target: "test"}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	_ = w.Close()
	st := w.Stats()
	if st.LastErr == nil {
		t.Fatalf("Stats.LastErr = nil; want non-nil after Close on failing writer")
	}
}

// TestSchemaFieldOnEveryLine asserts that every emitted JSONL line
// carries "schema":2 as the first key.
func TestSchemaFieldOnEveryLine(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull())
	for i := 0; i < 5; i++ {
		if err := Record(w, CacheHit{Project: "p", Target: "build"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	sc := bufio.NewScanner(&buf)
	for sc.Scan() {
		line := sc.Bytes()
		var head struct {
			Schema int    `json:"schema"`
			Type   string `json:"type"`
		}
		if err := json.Unmarshal(line, &head); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}
		if head.Schema != Schema {
			t.Errorf("schema = %d, want %d on line %q", head.Schema, Schema, line)
		}
		if head.Type != TypeCacheHit {
			t.Errorf("type = %q, want %q", head.Type, TypeCacheHit)
		}
	}
}

// TestRoundTripAllTypes writes one of every registered event type and
// reads them back. Asserts schema + type + body fidelity.
func TestRoundTripAllTypes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	w, err := OpenWriter(path, WithBlockOnFull())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	events := []any{
		CacheHit{Project: "svc-a", Target: "build", DurationMs: 342},
		CacheMiss{Project: "svc-b", Target: "build", DurationMs: 12},
		CacheError{Project: "svc-c", Target: "test", DurationMs: 1053, Message: "exit status 1"},
		GraphBuild{Nodes: 120, DurationMs: 8},
		GraphQuery{Op: "affected", Nodes: 120, Seeds: 3, Strategy: "reverse", ResultCount: 12, DurationMs: 4},
		GraphError{Op: "build", Message: "cycle"},
		FlakeCall{Project: "svc-a", Target: "test", Status: "retried_flake", Attempts: 2, RetryReason: "predicted_flake"},
		ShardSetup{Shard: "0", NShards: 4, DurationMs: 230},
		ShardTotal{Shard: "0", NShards: 4, DurationMs: 78321},
	}
	for _, e := range events {
		if err := recordAny(w, e); err != nil {
			t.Fatalf("Record %T: %v", e, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	wantTypes := []string{
		TypeCacheHit, TypeCacheMiss, TypeCacheError,
		TypeGraphBuild, TypeGraphQuery, TypeGraphError,
		TypeFlake, TypeShardSetup, TypeShardTotal,
	}
	sc := bufio.NewScanner(f)
	for i := 0; sc.Scan(); i++ {
		if i >= len(wantTypes) {
			t.Fatalf("got more lines than expected: %d", i+1)
		}
		var head struct {
			Schema int    `json:"schema"`
			Type   string `json:"type"`
		}
		if err := json.Unmarshal(sc.Bytes(), &head); err != nil {
			t.Fatalf("line %d unmarshal: %v -- %q", i, err, sc.Bytes())
		}
		if head.Schema != Schema {
			t.Errorf("line %d schema: got %d, want %d", i, head.Schema, Schema)
		}
		if head.Type != wantTypes[i] {
			t.Errorf("line %d type: got %q, want %q", i, head.Type, wantTypes[i])
		}
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
		if err != nil {
			t.Fatalf("Open round %d: %v", i, err)
		}
		if err := Record(w, CacheHit{Project: "svc-a", Target: "build", DurationMs: 10}); err != nil {
			t.Fatalf("Record round %d: %v", i, err)
		}
		if err := w.Close(); err != nil {
			t.Fatalf("Close round %d: %v", i, err)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var count int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		count++
	}
	if count != 2 {
		t.Errorf("got %d lines after two opens, want 2", count)
	}
}

// TestConcurrentWrites spawns N goroutines that all Record. With
// blocking enabled, every send lands; counters match.
func TestConcurrentWrites(t *testing.T) {
	t.Parallel()
	const goroutines = 64
	const perG = 16
	path := filepath.Join(t.TempDir(), "run.jsonl")
	w, err := OpenWriter(path, WithBlockOnFull())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perG; j++ {
				_ = Record(w, CacheHit{Project: "svc-a", Target: "build", DurationMs: 1})
			}
		}()
	}
	wg.Wait()
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	st := w.Stats()
	want := uint64(goroutines * perG)
	if st.Recorded != want {
		t.Errorf("Recorded = %d, want %d", st.Recorded, want)
	}
	if st.Flushed != want {
		t.Errorf("Flushed = %d, want %d", st.Flushed, want)
	}
	if st.Dropped != 0 {
		t.Errorf("Dropped = %d, want 0 under WithBlockOnFull", st.Dropped)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var count int
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Errorf("corrupt line %d: %v -- %q", count+1, err, sc.Bytes())
		}
		count++
	}
	if uint64(count) != want {
		t.Errorf("got %d lines, want %d", count, want)
	}
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
		_ = Record(w, CacheHit{Project: "p", Target: "t"})
	}
	// Release the drain goroutine so Close can complete.
	close(bw.ch)
	_ = w.Close()
	st := w.Stats()
	if st.Recorded+st.Dropped != uint64(total) {
		t.Errorf("Recorded(%d) + Dropped(%d) = %d, want %d",
			st.Recorded, st.Dropped, st.Recorded+st.Dropped, total)
	}
	if st.Dropped == 0 {
		t.Errorf("Dropped = 0; want some drops with a 4-slot queue and a stalled writer")
	}
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
	f, err := ParseFilter([]string{"+cache.hit"})
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull(), WithFilter(f))
	if err := Record(w, CacheHit{Project: "p", Target: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := Record(w, CacheMiss{Project: "p", Target: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := Record(w, GraphBuild{Nodes: 10}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	st := w.Stats()
	if st.Recorded != 1 {
		t.Errorf("Recorded = %d, want 1", st.Recorded)
	}
	if st.Filtered != 2 {
		t.Errorf("Filtered = %d, want 2", st.Filtered)
	}
}

// TestFilterExcludeOnly drops listed types, admits the rest.
func TestFilterExcludeOnly(t *testing.T) {
	t.Parallel()
	f, err := ParseFilter([]string{"-graph.build", "-graph.query"})
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull(), WithFilter(f))
	if err := Record(w, CacheHit{Project: "p", Target: "t"}); err != nil {
		t.Fatal(err)
	}
	if err := Record(w, GraphBuild{Nodes: 10}); err != nil {
		t.Fatal(err)
	}
	if err := Record(w, GraphQuery{Op: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := Record(w, GraphError{Message: "boom"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	st := w.Stats()
	if st.Recorded != 2 {
		t.Errorf("Recorded = %d, want 2 (cache.hit + graph.error)", st.Recorded)
	}
	if st.Filtered != 2 {
		t.Errorf("Filtered = %d, want 2 (graph.build + graph.query)", st.Filtered)
	}
}

func TestFilterEmptyAdmitsAll(t *testing.T) {
	t.Parallel()
	f, err := ParseFilter(nil)
	if err != nil {
		t.Fatalf("ParseFilter: %v", err)
	}
	if f != nil {
		t.Errorf("ParseFilter(nil) = %v, want nil filter", f)
	}
	f2, err := ParseFilter([]string{"", " ", "\t"})
	if err != nil {
		t.Fatalf("ParseFilter(blanks): %v", err)
	}
	if f2 != nil {
		t.Errorf("ParseFilter(blanks) = %v, want nil filter", f2)
	}
}

func TestFilterMalformedReturnsError(t *testing.T) {
	t.Parallel()
	_, err := ParseFilter([]string{"+", "-"})
	if err == nil {
		t.Errorf("ParseFilter(only-malformed) = nil error; want error")
	}
}

func TestRecordUnregisteredType(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewWriter(&buf, WithBlockOnFull())
	err := Record(w, struct{ X int }{X: 1})
	if err == nil {
		t.Errorf("Record on unregistered type = nil error; want error")
	}
	_ = w.Close()
}

func TestRecordNilWriterIsNoop(t *testing.T) {
	t.Parallel()
	if err := Record(nil, CacheHit{Project: "p"}); err != nil {
		t.Errorf("Record on nil Writer = %v; want nil", err)
	}
}

func TestCacheRunOptions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	w, err := OpenWriter(path, WithBlockOnFull())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	root := t.TempDir()
	cdir := filepath.Join(t.TempDir(), ".magus")
	src := filepath.Join(root, "pkg", "main.go")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("package main\nfunc main(){}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "pkg", "out.bin")
	spec := cache.Spec{
		ProjectPath:   "pkg",
		Sources:       []string{"pkg/*.go"},
		Outputs:       []string{"pkg/out.bin"},
		WorkspaceRoot: root,
		Target:        "build",
	}

	opts := RunOptions(w)

	c, err := cache.Open(cdir, cache.WithMutable(true))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	if _, err := c.Run(ctx, spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("bin"), 0o755)
	}, opts...); err != nil {
		t.Fatalf("run (miss): %v", err)
	}

	c2, err := cache.Open(cdir, cache.WithMutable(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Run(ctx, spec, func(_ context.Context) error {
		return os.WriteFile(out, []byte("bin"), 0o755)
	}, opts...); err != nil {
		t.Fatalf("run (hit): %v", err)
	}

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var types []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(sc.Bytes(), &head); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		types = append(types, head.Type)
	}
	if len(types) != 2 {
		t.Fatalf("got %d events, want 2: %v", len(types), types)
	}
	if types[0] != TypeCacheMiss {
		t.Errorf("first event type = %q, want %q", types[0], TypeCacheMiss)
	}
	if types[1] != TypeCacheHit {
		t.Errorf("second event type = %q, want %q", types[1], TypeCacheHit)
	}
}

func TestWriterContextRoundTrip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.jsonl")
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	ctx := WithWriter(context.Background(), w)
	got := WriterFromContext(ctx)
	if got != w {
		t.Errorf("FromContext: got %p, want %p", got, w)
	}
	if WriterFromContext(context.Background()) != nil {
		t.Error("FromContext on plain ctx should return nil")
	}
}

// TestCloseIsIdempotent guards against double-close panic on the channel.
func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestConcurrentRecordAndClose verifies that a concurrent Record and Close
// cannot panic. Previously Close closed w.ch directly; any Record blocked on
// a channel send in WithBlockOnFull mode would panic with "send on closed
// channel". The fix uses a separate quit channel so close is never called
// on a channel that producers can still send to.
func TestConcurrentRecordAndClose(t *testing.T) {
	t.Parallel()
	for range 50 {
		var buf bytes.Buffer
		w := NewWriter(&buf, WithBlockOnFull())
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 32 {
					_ = Record(w, CacheHit{Project: "p", Target: "t"})
				}
			}()
		}
		// Close races with the goroutines above; must not panic.
		_ = w.Close()
		wg.Wait()
	}
}

// ensure errors package is referenced in case we add error-matching tests.
var _ = errors.Is
