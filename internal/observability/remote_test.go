package observability

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/egladman/magus/internal/cache"
)

// fakeBackend is a minimal cache.RemoteBackend whose get/put behaviour the test
// controls directly, so assertions don't depend on a real transport.
type fakeBackend struct {
	data   []byte // artifact returned on get; nil = miss
	getErr error
	putErr error
	put    []byte // captured put payload
	pruned bool
}

func (f *fakeBackend) Active(context.Context) bool { return true }

func (f *fakeBackend) GetArtifact(context.Context, string, string) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	if f.data == nil {
		return nil, nil
	}
	return io.NopCloser(bytes.NewReader(f.data)), nil
}

func (f *fakeBackend) PutArtifact(_ context.Context, _, _ string, r io.Reader) error {
	if f.putErr != nil {
		return f.putErr
	}
	b, err := io.ReadAll(r)
	f.put = b
	return err
}

// fakePruner adds the optional RemotePruner capability.
type fakePruner struct{ *fakeBackend }

func (f *fakePruner) PruneArtifacts(context.Context, cache.RetentionPolicy) error {
	f.pruned = true
	return nil
}

func TestInstrumentRemoteBackend_GetHit(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	b := InstrumentRemoteBackend(&fakeBackend{data: []byte("hello")}, rec)

	rc, err := b.GetArtifact(context.Background(), "p", "h")
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	// Metrics for a hit close with the reader, so the byte count is the artifact size.
	if len(rec.remoteOps) != 0 {
		t.Fatalf("op recorded before reader close: %+v", rec.remoteOps)
	}
	got, _ := io.ReadAll(rc)
	if err := rc.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("read %q, want hello", got)
	}
	if len(rec.remoteOps) != 1 {
		t.Fatalf("remoteOps=%d, want 1", len(rec.remoteOps))
	}
	op := rec.remoteOps[0]
	if op.Op != "get" || op.Outcome != "hit" || op.Bytes != 5 {
		t.Errorf("op = %+v, want {get hit Bytes:5}", op)
	}
	if len(rec.spans) != 1 || rec.spans[0] != "magus.cache.remote.get" {
		t.Errorf("spans = %v, want [magus.cache.remote.get]", rec.spans)
	}
}

func TestInstrumentRemoteBackend_GetMiss(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	b := InstrumentRemoteBackend(&fakeBackend{data: nil}, rec)

	rc, err := b.GetArtifact(context.Background(), "p", "h")
	if err != nil {
		t.Fatalf("GetArtifact: %v", err)
	}
	if rc != nil {
		t.Fatal("expected nil reader on miss")
	}
	if len(rec.remoteOps) != 1 || rec.remoteOps[0].Outcome != "miss" || rec.remoteOps[0].Bytes != 0 {
		t.Errorf("remoteOps = %+v, want one {get miss Bytes:0}", rec.remoteOps)
	}
}

func TestInstrumentRemoteBackend_GetError(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	b := InstrumentRemoteBackend(&fakeBackend{getErr: errors.New("boom")}, rec)

	if _, err := b.GetArtifact(context.Background(), "p", "h"); err == nil {
		t.Fatal("expected error")
	}
	if len(rec.remoteOps) != 1 || rec.remoteOps[0].Outcome != "error" {
		t.Errorf("remoteOps = %+v, want one {get error}", rec.remoteOps)
	}
}

func TestInstrumentRemoteBackend_Put(t *testing.T) {
	t.Parallel()
	rec := &recorder{}
	fb := &fakeBackend{}
	b := InstrumentRemoteBackend(fb, rec)

	if err := b.PutArtifact(context.Background(), "p", "h", bytes.NewReader([]byte("world"))); err != nil {
		t.Fatalf("PutArtifact: %v", err)
	}
	if string(fb.put) != "world" {
		t.Errorf("backend received %q, want world", fb.put)
	}
	if len(rec.remoteOps) != 1 {
		t.Fatalf("remoteOps=%d, want 1", len(rec.remoteOps))
	}
	op := rec.remoteOps[0]
	if op.Op != "put" || op.Outcome != "stored" || op.Bytes != 5 {
		t.Errorf("op = %+v, want {put stored Bytes:5}", op)
	}
	if len(rec.spans) != 1 || rec.spans[0] != "magus.cache.remote.put" {
		t.Errorf("spans = %v, want [magus.cache.remote.put]", rec.spans)
	}
}

// TestInstrumentRemoteBackend_DisabledPassthrough verifies that a disabled
// provider yields the original backend unwrapped — no byte counting overhead on
// the default (telemetry-off) path.
func TestInstrumentRemoteBackend_DisabledPassthrough(t *testing.T) {
	t.Parallel()
	disabled, err := New(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	fb := &fakeBackend{}
	if got := InstrumentRemoteBackend(fb, disabled); got != cache.RemoteBackend(fb) {
		t.Error("disabled provider should return the backend unwrapped")
	}
}

// TestInstrumentRemoteBackend_PrunePreserved verifies the optional RemotePruner
// capability survives wrapping when present, and is absent otherwise.
func TestInstrumentRemoteBackend_PrunePreserved(t *testing.T) {
	t.Parallel()
	rec := &recorder{}

	plain := InstrumentRemoteBackend(&fakeBackend{}, rec)
	if _, ok := plain.(cache.RemotePruner); ok {
		t.Error("plain backend should not gain a prune capability")
	}

	fp := &fakePruner{fakeBackend: &fakeBackend{}}
	wrapped := InstrumentRemoteBackend(fp, rec)
	pr, ok := wrapped.(cache.RemotePruner)
	if !ok {
		t.Fatal("prune capability lost after wrapping")
	}
	if err := pr.PruneArtifacts(context.Background(), cache.RetentionPolicy{}); err != nil {
		t.Fatalf("PruneArtifacts: %v", err)
	}
	if !fp.pruned {
		t.Error("underlying prune not invoked")
	}
	if len(rec.spans) != 1 || rec.spans[0] != "magus.cache.remote.prune" {
		t.Errorf("spans = %v, want [magus.cache.remote.prune]", rec.spans)
	}
}
